# PgQueuer 死锁解决方案深度分析

## 执行日期：2025-10-05

## 一、Python PgQueuer 的架构设计

### 1. 核心策略：独立连接 + 批量 SQL + 应用层锁

```
┌─────────────────────────────────────────────────────────────┐
│                    PgQueuer Architecture                     │
└─────────────────────────────────────────────────────────────┘

每个 Consumer/Producer Worker:
┌──────────────────────────────────┐
│  QueueManager (独立实例)          │
│  ├─ AsyncpgDriver                │
│  │  ├─ asyncpg.Connection (独立)  │  ← 每个 worker 独立连接
│  │  └─ asyncio.Lock              │  ← 单连接内串行化
│  └─ JobBuffer                    │
│     ├─ asyncio.Lock              │  ← buffer 并发保护
│     └─ flush_callback            │
│        └─ queries.log_jobs()     │  ← 批量 SQL
└──────────────────────────────────┘
```

### 2. 关键设计原则

#### 原则 1：独立连接隔离
```python
# benchmark.py - 每个 worker 创建独立连接
for _ in range(args.dequeue):
    driver = AsyncpgDriver(await asyncpg.connect())  # 独立连接
    queries.append((driver, Queries(driver)))

qms = [QueueManager(d) for d, _ in queries]  # 独立 QueueManager
```

**好处**：
- ✅ 每个 worker 有自己的数据库会话
- ✅ 不共享连接池，避免跨 worker 的锁竞争
- ✅ 连接级别的隔离，天然避免跨连接死锁

#### 原则 2：应用层串行化（单连接内）
```python
# db.py - AsyncpgDriver
class AsyncpgDriver:
    def __init__(self, connection: asyncpg.Connection):
        self.lock = asyncio.Lock()  # 应用层锁
        self.connection = connection
    
    async def execute(self, query: str, *args: Any) -> str:
        async with self.lock:  # 串行化同一连接的所有操作
            return await self.connection.execute(query, *args)
```

**好处**：
- ✅ 同一连接的所有 SQL 按顺序执行
- ✅ 避免单连接内的并发冲突
- ✅ asyncio.Lock 轻量级，无性能损失

#### 原则 3：批量 SQL + SQL 层预聚合
```python
# queries.py - create_log_job_query()
async def log_jobs(self, job_status: list[tuple[Job, STATUS_LOG]]) -> None:
    await self.driver.execute(
        self.qb.create_log_job_query(),
        [j.id for j, _ in job_status],  # 所有 job IDs 作为数组
        [s for _, s in job_status],      # 所有 statuses 作为数组
    )
```

**SQL 查询分析**：
```sql
WITH deleted AS (
    -- 1️⃣ 批量删除：一次删除多个 jobs
    DELETE FROM pgqueuer
    WHERE id = ANY($1::integer[])
    RETURNING id, priority, entrypoint, created, time_in_queue
), 
job_status AS (
    -- 2️⃣ 构建 job-status 映射
    SELECT
        unnest($1::integer[]) AS id,
        unnest($2::pgqueuer_statistics_status[]) AS status
), 
grouped_data AS (
    -- 3️⃣ SQL 层预聚合：减少 INSERT 行数
    SELECT
        priority, entrypoint, time_in_queue, created, status,
        count(*)  -- 相同维度的 jobs 合并计数
    FROM deleted JOIN job_status ON job_status.id = deleted.id
    GROUP BY priority, entrypoint, time_in_queue, created, status
)
-- 4️⃣ 批量插入聚合后的数据
INSERT INTO pgqueuer_statistics (...)
SELECT ... FROM grouped_data
ON CONFLICT (...) DO UPDATE
SET count = pgqueuer_statistics.count + EXCLUDED.count
```

**关键优势**：
- ✅ **单次 SQL 调用**：不是循环执行 N 次
- ✅ **SQL 层预聚合**：10 个相同维度的 job → 1 行统计（count=10）
- ✅ **减少 INSERT 行数**：大幅降低锁竞争
- ✅ **减少网络往返**：1 次 vs N 次

#### 原则 4：Buffer 锁保护
```python
# buffers.py - JobBuffer
class JobBuffer:
    lock: asyncio.Lock = dataclasses.field(default_factory=asyncio.Lock)
    
    async def add_job(self, job: Job, status: STATUS_LOG) -> None:
        async with self.lock:  # 保护 buffer 操作
            self.events.append((job, status))
            if len(self.events) >= self.max_size:
                await self.flush_jobs()  # 在锁内调用 flush
    
    async def flush_jobs(self) -> None:
        if self.events:
            await self.flush_callback(self.events)  # 批量处理
            self.events.clear()
```

**好处**：
- ✅ 防止并发添加导致的 buffer 数据竞争
- ✅ flush 操作原子化
- ✅ 批量提交减少数据库调用

### 3. 为什么 Python 版本不会死锁？

#### 场景分析：4 个 Consumer 同时完成 jobs

```
时间线                Worker 1          Worker 2          Worker 3          Worker 4
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
T1: Buffer 满     → flush()
                    conn1.lock ✓
                    BEGIN TX1
                    
T2: Buffer 满                       → flush()
                                      conn2.lock ✓
                                      BEGIN TX2
                                      
T3: Buffer 满                                          → flush()
                                                         conn3.lock ✓
                                                         BEGIN TX3
                                                         
T4: Buffer 满                                                             → flush()
                                                                            conn4.lock ✓
                                                                            BEGIN TX4

T5: SQL 执行      DELETE + INSERT    DELETE + INSERT    DELETE + INSERT    DELETE + INSERT
                  (批量, 预聚合)      (批量, 预聚合)      (批量, 预聚合)      (批量, 预聚合)
                  
T6: 统计表更新    UPDATE row A       UPDATE row B       UPDATE row C       UPDATE row D
                  (少量行)           (少量行)           (少量行)           (少量行)
                  
T7: 提交          COMMIT TX1         COMMIT TX2         COMMIT TX3         COMMIT TX4
                  conn1.lock ✗       conn2.lock ✗       conn3.lock ✗       conn4.lock ✗
```

**不死锁的原因**：

1. **独立连接** → TX1、TX2、TX3、TX4 完全独立
2. **应用层串行化** → 每个连接内部无并发
3. **SQL 预聚合** → 每个 TX 只更新少量统计行（而不是 N 行）
4. **锁持有时间短** → 单次 SQL，快速提交

**即使冲突，也只是 ON CONFLICT**：
```sql
-- 如果 Worker 1 和 Worker 2 更新同一统计行
TX1: INSERT ... ON CONFLICT UPDATE count = count + 10  -- 先执行
TX2: INSERT ... ON CONFLICT UPDATE count = count + 15  -- 等待 TX1
-- TX1 COMMIT → TX2 继续 → 无死锁
```

**死锁需要循环等待，但这里不会发生**：
- ❌ 循环等待：TX1 等 TX2，TX2 等 TX1
- ✅ 实际情况：TX2 等 TX1 COMMIT，TX1 不等任何人

---

## 二、Go pgqueue-go 的问题分析

### 1. 当前架构（有问题）

```
┌─────────────────────────────────────────────────────────────┐
│                    Current Go Architecture                   │
└─────────────────────────────────────────────────────────────┘

共享连接池:
┌──────────────────────────────────┐
│  pgxpool.Pool (25 connections)    │  ← 所有 worker 共享
│  ├─ conn1                         │
│  ├─ conn2                         │
│  └─ ...                           │
└──────────────────────────────────┘
         ↑          ↑          ↑
         │          │          │
    Worker 1    Worker 2    Worker 3
```

### 2. 死锁发生机制

```go
// 当前实现 - queries_ops.go
func (q *Queries) CompleteJobs(ctx context.Context, jobStatuses []JobStatus) error {
    tx, _ := q.db.Begin(ctx)  // 从连接池获取连接
    defer tx.Rollback(ctx)
    
    // 🔒 获取 Advisory Lock（临时方案）
    tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", 8102199229687136800)
    
    query := q.qb.CreateCompleteJobQuery()
    for _, js := range jobStatuses {  // ❌ 循环执行
        // 每次执行：DELETE 1 job + INSERT 1 统计行
        tx.Exec(ctx, query, js.JobID, js.Status)
    }
    
    return tx.Commit(ctx)
}
```

**问题点**：

#### 问题 1：循环执行单个 job
```go
for _, js := range jobStatuses {
    tx.Exec(ctx, query, js.JobID, js.Status)  // N 次数据库调用
}
```

**对比 Python**：
```python
await self.driver.execute(
    query,
    [j.id for j, _ in job_status],    # 1 次数据库调用
    [s for _, s in job_status],
)
```

#### 问题 2：SQL 未预聚合
```sql
-- 当前 SQL (每次循环)
WITH deleted AS (
    DELETE FROM pgqueue_jobs WHERE id = $1  -- 删除 1 个 job
    RETURNING ...
)
INSERT INTO pgqueue_statistics (...)
SELECT ..., 1 AS count  -- 插入 1 行（count=1）
FROM deleted
ON CONFLICT DO UPDATE ...
```

**如果 10 个 job 有相同维度**：
- ❌ Go: 执行 10 次 SQL → 10 次 ON CONFLICT UPDATE
- ✅ Python: 执行 1 次 SQL → 1 次 INSERT (count=10)

#### 问题 3：Advisory Lock 只是绕过问题
```go
// 当前的"修复"
tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", advisoryLockID)
```

**效果**：
- ✅ 确实避免了死锁（串行化所有统计更新）
- ⚠️ 但牺牲了并发性能
- ⚠️ 所有 worker 排队等待锁

**性能影响**：
```
无 Advisory Lock (有死锁):     1,120 jobs/sec ❌
有 Advisory Lock (无死锁):     1,120 jobs/sec ✓ (4 worker → 单线程化)
理想批量 SQL (无死锁):         3,000+ jobs/sec? ✨
```

---

## 三、Go 版本的对齐方案

### 方案对比

| 方案 | 描述 | 优点 | 缺点 | 推荐度 |
|------|------|------|------|--------|
| **方案 A：当前 Advisory Lock** | 继续使用 pg_advisory_xact_lock | ✅ 简单<br>✅ 已实现 | ⚠️ 串行化<br>⚠️ 性能受限 | ⭐⭐ 临时方案 |
| **方案 B：批量 SQL（推荐）** | 实现 Python 的批量 SQL | ✅ 高性能<br>✅ 完全对齐<br>✅ 可能不需要锁 | ⚠️ 需要重构 | ⭐⭐⭐⭐⭐ 最佳 |
| **方案 C：混合方案** | 批量 SQL + Advisory Lock | ✅ 最安全<br>✅ 高性能 | ⚠️ 实现复杂度 | ⭐⭐⭐⭐ 稳妥 |

### 推荐：方案 B（批量 SQL）

#### 步骤 1：修改 SQL 查询生成器

```go
// queries.go - 新增批量完成 SQL
func (qb *QueryBuilder) CreateBatchCompleteJobsQuery() string {
    return fmt.Sprintf(`
        WITH deleted AS (
            DELETE FROM %s
            WHERE id = ANY($1::integer[])
            RETURNING 
                id,
                priority,
                entrypoint,
                DATE_TRUNC('sec', created at time zone 'UTC') AS created,
                DATE_TRUNC('sec', AGE(updated, created)) AS time_in_queue
        ),
        job_status AS (
            SELECT
                unnest($1::integer[]) AS id,
                unnest($2::%s[]) AS status
        ),
        grouped_data AS (
            SELECT
                priority,
                entrypoint,
                time_in_queue,
                created,
                status,
                count(*) AS count
            FROM deleted 
            JOIN job_status ON job_status.id = deleted.id
            GROUP BY priority, entrypoint, time_in_queue, created, status
        )
        INSERT INTO %s (priority, entrypoint, time_in_queue, created, status, count)
        SELECT 
            priority,
            entrypoint,
            time_in_queue,
            created,
            status,
            count
        FROM grouped_data
        ON CONFLICT (
            priority,
            entrypoint,
            DATE_TRUNC('sec', created at time zone 'UTC'),
            DATE_TRUNC('sec', time_in_queue),
            status
        )
        DO UPDATE
        SET count = %s.count + EXCLUDED.count
    `, 
        qb.Settings.QueueTable,
        qb.Settings.StatisticsTableStatusType,
        qb.Settings.StatisticsTable,
        qb.Settings.StatisticsTable,
    )
}
```

#### 步骤 2：修改 CompleteJobs 实现

```go
// queries_ops.go - 批量完成版本
func (q *Queries) CompleteJobs(ctx context.Context, jobStatuses []JobStatus) error {
    if len(jobStatuses) == 0 {
        return nil
    }

    // 提取 job IDs 和 statuses 数组
    jobIDs := make([]int, len(jobStatuses))
    statuses := make([]string, len(jobStatuses))
    
    for i, js := range jobStatuses {
        jobIDs[i] = js.JobID
        statuses[i] = js.Status
    }

    // 单次批量 SQL 执行（像 Python 一样）
    query := q.qb.CreateBatchCompleteJobsQuery()
    _, err := q.db.Exec(ctx, query, jobIDs, statuses)
    
    return err
}
```

**关键改进**：
- ✅ 单次 SQL 调用（不是循环）
- ✅ SQL 层预聚合（GROUP BY）
- ✅ 减少数据库往返
- ✅ 减少锁竞争
- ✅ 可能不再需要 Advisory Lock

#### 步骤 3：性能测试对比

```bash
# 测试 1：当前 Advisory Lock 版本
./bin/benchmark -t 15 -dq 4 -dqbs 10 -eq 2 -eqbs 30
# 预期：~1,000 jobs/sec

# 测试 2：批量 SQL 版本（无 Advisory Lock）
./bin/benchmark -t 15 -dq 4 -dqbs 10 -eq 2 -eqbs 30
# 预期：2,000+ jobs/sec

# 测试 3：极限并发
./bin/benchmark -t 15 -dq 8 -dqbs 20 -eq 4 -eqbs 50
# 测试是否还有死锁
```

---

## 四、实施计划

### Phase 1：实现批量 SQL（1-2 小时）
- [ ] 创建 `CreateBatchCompleteJobsQuery()` 方法
- [ ] 重构 `CompleteJobs()` 使用批量 SQL
- [ ] 保留旧的 `CreateCompleteJobQuery()` 作为单个 job 版本

### Phase 2：测试验证（30 分钟）
- [ ] 单元测试：验证 SQL 正确性
- [ ] 集成测试：多 worker 并发测试
- [ ] 压力测试：极限并发场景
- [ ] 对比测试：批量 vs Advisory Lock

### Phase 3：性能优化（可选）
- [ ] 如果仍有死锁，考虑混合方案（批量 + Advisory Lock）
- [ ] 调优 buffer 参数（max_size, timeout）
- [ ] 监控统计表大小和查询性能

### Phase 4：文档更新
- [ ] 更新 README 说明批量 SQL 实现
- [ ] 添加性能基准测试结果
- [ ] 对比 Python PgQueuer 的性能

---

## 五、预期效果

### 性能提升预测

| 场景 | 当前 (Advisory Lock) | 批量 SQL | 提升 |
|------|---------------------|----------|------|
| 2 consumers | 1,120 jobs/sec | 2,000+ jobs/sec | +78% |
| 4 consumers | 996 jobs/sec | 2,500+ jobs/sec | +151% |
| 8 consumers | N/A (未测试) | 3,000+ jobs/sec | N/A |

### 架构对齐度

| 维度 | Python PgQueuer | Go pgqueue (当前) | Go pgqueue (批量 SQL) |
|------|----------------|-------------------|---------------------|
| 独立连接 | ✅ | ❌ (共享池) | ❌ (共享池，但影响小) |
| 批量 SQL | ✅ | ❌ | ✅ |
| SQL 预聚合 | ✅ | ❌ | ✅ |
| 应用层锁 | ✅ | ❌ | N/A |
| Advisory Lock | ❌ | ✅ (临时) | ❌ (不需要) |
| 并发性能 | 高 | 中 (串行化) | 高 |

---

## 六、风险评估

### 低风险
- ✅ SQL 逻辑简单，易于验证
- ✅ 保持向后兼容（保留单个 job API）
- ✅ 可以逐步迁移

### 中风险
- ⚠️ pgx/v5 对数组参数的支持需要测试
- ⚠️ 需要充分的并发测试

### 缓解措施
- 先实现并通过单元测试
- 使用 benchmark tool 充分压测
- 保留 Advisory Lock 作为后备方案

---

## 七、总结

### Python PgQueuer 成功的关键

1. **独立连接隔离** - 每个 worker 独立数据库会话
2. **应用层串行化** - asyncio.Lock 保护单连接操作
3. **批量 SQL** - 一次处理多个 jobs
4. **SQL 预聚合** - GROUP BY 减少 INSERT 行数
5. **短事务** - 快速提交，减少锁持有时间

### Go 版本应该学习

1. **批量 SQL 处理**（最重要）
   - 使用数组参数：`ANY($1::integer[])`
   - SQL 层 GROUP BY 预聚合
   - 单次数据库调用

2. **可选：连接池策略调整**
   - 考虑增大连接池
   - 或为统计更新使用专用连接

3. **不需要完全照搬**
   - Go 的 pgxpool 已经很好
   - 不需要应用层锁（批量 SQL 足够）

### 下一步行动

**立即实施**：实现批量 SQL 版本

**理由**：
- ✅ 性能提升明显（预期 2-3 倍）
- ✅ 完全对齐 Python 版本设计
- ✅ 可能彻底消除死锁（无需 Advisory Lock）
- ✅ 实现难度适中（2 小时工作量）

---

## 附录：代码对比

### Python (正确实现)
```python
# 1 次 SQL 调用
await self.driver.execute(
    query,
    [j.id for j, _ in job_status],
    [s for _, s in job_status],
)
```

### Go (当前 - 有问题)
```go
// N 次 SQL 调用
for _, js := range jobStatuses {
    tx.Exec(ctx, query, js.JobID, js.Status)
}
```

### Go (改进 - 对齐 Python)
```go
// 1 次 SQL 调用
query := q.qb.CreateBatchCompleteJobsQuery()
_, err := q.db.Exec(ctx, query, jobIDs, statuses)
```

**差异显而易见！** 🎯
