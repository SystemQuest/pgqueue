# PgQueue4Go Test Migration Plan - TODO List

## Overview
基于对pgqueuer测试用例的分析，制定从Python测试到Go测试的迁移计划。pgqueuer有完整的测试覆盖，包括单元测试、集成测试、并发测试和性能测试。

## 📊 pgqueuer测试用例分析

### 现有测试文件分析
| 文件 | 主要测试内容 | 测试数量 | 复杂度 |
|------|-------------|----------|--------|
| `test_qm.py` | QueueManager核心功能 | 6个测试函数 | ⭐⭐⭐⭐ |
| `test_queries.py` | 数据库查询操作 | 8个测试函数 | ⭐⭐⭐⭐⭐ |
| `test_drivers.py` | 数据库驱动层 | 4个测试函数 | ⭐⭐⭐ |
| `test_buffers.py` | 缓冲区和任务管理 | 3个测试函数 | ⭐⭐⭐ |
| `test_tm.py` | TaskManager测试 | 2个测试函数 | ⭐⭐ |
| `conftest.py` | 测试配置和fixtures | - | ⭐⭐ |

### 测试覆盖领域
1. **队列管理 (QueueManager)**
   - 单个/批量作业入队出队
   - 并发处理 (1-16 workers)
   - 同步/异步entrypoint
   - 作业优先级处理

2. **数据库查询 (Queries)**
   - 作业CRUD操作
   - 队列大小统计
   - 批量操作
   - 并发安全性
   - 日志记录

3. **数据库驱动 (Drivers)**
   - AsyncPG和Psycopg驱动
   - LISTEN/NOTIFY机制
   - 连接管理

4. **缓冲区管理 (Buffers)**
   - JobBuffer大小限制
   - 超时刷新机制
   - TaskManager生命周期

## 🎯 迁移计划 (按优先级)

### ⭐⭐⭐⭐⭐ Priority 1: 核心功能测试 (必须实现)

#### 1.1 Queue Manager Core Tests
**目标文件**: `pkg/queue/manager_test.go`
**基于**: `test_qm.py`

**测试用例**:
```go
// 迁移 test_job_queing
func TestJobQueuing(t *testing.T)
func TestJobQueuingParameterized(t *testing.T) // N=1,2,32

// 迁移 test_job_fetch  
func TestJobFetchConcurrent(t *testing.T) // concurrency=1,2,3,4

// 迁移 test_sync_entrypoint
func TestSyncEntrypoint(t *testing.T)
```

**实现复杂度**: 高 - 需要Go的goroutine并发模型
**预计工时**: 16-20小时

#### 1.2 Database Queries Tests
**目标文件**: `pkg/queue/queries_test.go`
**基于**: `test_queries.py`

**测试用例**:
```go
// 迁移 test_queries_put
func TestEnqueueJobs(t *testing.T)

// 迁移 test_queries_next_jobs
func TestDequeueJobs(t *testing.T)

// 迁移 test_queries_next_jobs_concurrent
func TestDequeueJobsConcurrent(t *testing.T) // concurrency=1,2,4,16

// 迁移 test_queries_clear
func TestClearQueue(t *testing.T)

// 迁移其他查询测试
func TestQueueStatistics(t *testing.T)
func TestJobLogging(t *testing.T)
```

**实现复杂度**: 高 - 复杂的数据库操作和并发控制
**预计工时**: 20-24小时

### ⭐⭐⭐⭐ Priority 2: 数据库层测试

#### 2.1 Database Driver Tests
**目标文件**: `pkg/db/db_test.go`
**基于**: `test_drivers.py`

**测试用例**:
```go
func TestDatabaseConnection(t *testing.T)
func TestQueryExecution(t *testing.T)
func TestFetchOperations(t *testing.T)
func TestNotificationSystem(t *testing.T) // LISTEN/NOTIFY
```

**实现复杂度**: 中 - pgx驱动的特定功能测试
**预计工时**: 12-16小时

#### 2.2 Event Listener Tests
**目标文件**: `pkg/listener/listener_test.go`
**基于**: `test_drivers.py` 的通知测试

**测试用例**:
```go
func TestEventListener(t *testing.T)
func TestEventParsing(t *testing.T)
func TestListenerReconnection(t *testing.T)
func TestEventHandlerChain(t *testing.T)
```

**实现复杂度**: 中高 - 事件系统和连接恢复
**预计工时**: 14-18小时

### ⭐⭐⭐ Priority 3: 高级功能测试

#### 3.1 Buffer and Task Management Tests
**目标文件**: `pkg/buffer/buffer_test.go` (如果实现buffer功能)
**基于**: `test_buffers.py`, `test_tm.py`

**测试用例**:
```go
func TestJobBuffer(t *testing.T)
func TestBufferTimeout(t *testing.T)
func TestBufferSizeLimit(t *testing.T)
func TestTaskManager(t *testing.T)
```

**实现复杂度**: 中 - 需要先实现buffer功能
**预计工时**: 10-14小时

#### 3.2 CLI Commands Tests
**目标文件**: `cmd/pgqueue/cli_test.go`

**测试用例**:
```go
func TestInstallCommand(t *testing.T)
func TestUninstallCommand(t *testing.T)
func TestHealthCommand(t *testing.T)
func TestDashboardCommand(t *testing.T)
func TestListenCommand(t *testing.T)
```

**实现复杂度**: 中 - CLI测试框架
**预计工时**: 8-12小时

### ⭐⭐ Priority 4: 性能和压力测试

#### 4.1 Performance Benchmarks
**目标文件**: `pkg/queue/benchmark_test.go`

**基准测试**:
```go
func BenchmarkEnqueueJob(b *testing.B)
func BenchmarkDequeueJob(b *testing.B)
func BenchmarkConcurrentProcessing(b *testing.B)
func BenchmarkEventProcessing(b *testing.B)
```

**实现复杂度**: 中 - Go benchmark框架
**预计工时**: 6-10小时

#### 4.2 Integration Tests
**目标文件**: `test/integration/integration_test.go`

**集成测试**:
```go
func TestEndToEndJobProcessing(t *testing.T)
func TestFailureRecovery(t *testing.T)
func TestLongRunningJobs(t *testing.T)
```

**实现复杂度**: 中高 - 需要真实数据库环境
**预计工时**: 12-16小时

### ⭐ Priority 5: 测试基础设施

#### 5.1 Test Infrastructure
**目标文件**: `test/testutil/testutil.go`, `test/testutil/db.go`
**基于**: `conftest.py`

**测试基础设施**:
```go
// 数据库测试工具
func SetupTestDB(t *testing.T) *db.DB
func TeardownTestDB(t *testing.T, db *db.DB)
func CreateTempDatabase(t *testing.T) string

// 测试数据工具
func CreateTestJobs(t *testing.T, n int) []*queue.Job  
func AssertJobsProcessed(t *testing.T, expected []int, actual []int)
```

**实现复杂度**: 中 - 测试工具和辅助函数
**预计工时**: 8-12小时

#### 5.2 Docker Test Environment
**目标文件**: `test/docker/Dockerfile`, `test/docker/docker-compose.yml`
**基于**: `test/db/Dockerfile`, `test/db/init_db.sh`

**Docker测试环境**:
```yaml
# docker-compose.test.yml
services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: testuser
      POSTGRES_PASSWORD: testpassword
      POSTGRES_DB: testdb
    volumes:
      - ./init_test_db.sh:/docker-entrypoint-initdb.d/init_test_db.sh
```

**实现复杂度**: 低 - Docker配置
**预计工时**: 4-6小时

## 📋 实现阶段计划

### Phase 1: 测试基础设施 (1-2周)
- [ ] 创建测试工具包 (`test/testutil/`)
- [ ] 设置Docker测试环境
- [ ] 实现数据库测试fixtures
- [ ] 配置CI/CD测试流水线

### Phase 2: 核心功能测试 (3-4周)
- [ ] Queue Manager测试 (Priority 1.1)
- [ ] Database Queries测试 (Priority 1.2)
- [ ] 数据库驱动测试 (Priority 2.1)

### Phase 3: 高级功能测试 (2-3周)
- [ ] Event Listener测试 (Priority 2.2)
- [ ] CLI命令测试 (Priority 3.2)
- [ ] Buffer功能测试 (Priority 3.1，如果实现)

### Phase 4: 性能和集成测试 (1-2周)
- [ ] 性能基准测试 (Priority 4.1)
- [ ] 端到端集成测试 (Priority 4.2)
- [ ] 压力测试和负载测试

## 🛠 技术实现要点

### Go测试框架选择
```go
// 推荐使用标准testing包 + testify
import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/stretchr/testify/suite"
)
```

### 数据库测试模式
```go
// 每个测试使用独立的数据库
func setupTestDB(t *testing.T) *db.DB {
    dbName := fmt.Sprintf("test_%s_%d", 
        strings.ReplaceAll(t.Name(), "/", "_"), 
        time.Now().UnixNano())
    
    // 创建临时数据库
    // 安装schema
    // 返回连接
}
```

### 并发测试模式
```go
// 参数化并发测试
func TestConcurrentProcessing(t *testing.T) {
    testCases := []struct {
        name        string
        numJobs     int
        concurrency int
    }{
        {"Single", 10, 1},
        {"Dual", 20, 2}, 
        {"Quad", 40, 4},
        {"High", 100, 16},
    }
    
    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            // 并发测试逻辑
        })
    }
}
```

## 📊 预计总工时和里程碑

| 阶段 | 预计工时 | 优先级 | 里程碑 |
|------|----------|--------|--------|
| Phase 1 | 20-30小时 | ⭐⭐⭐⭐⭐ | 测试基础设施完成 |
| Phase 2 | 48-60小时 | ⭐⭐⭐⭐⭐ | 核心功能测试覆盖80% |
| Phase 3 | 34-46小时 | ⭐⭐⭐⭐ | 高级功能测试完成 |
| Phase 4 | 18-26小时 | ⭐⭐⭐ | 性能测试和集成测试 |
| **总计** | **120-162小时** | | **完整测试套件** |

## 🎯 成功标准

### 代码覆盖率目标
- **核心包覆盖率**: ≥85% (`pkg/queue/`, `pkg/db/`)
- **整体覆盖率**: ≥75%
- **关键路径覆盖率**: 100% (入队、出队、事件处理)

### 性能基准
- **单作业处理**: <1ms (本地PostgreSQL)
- **批量作业处理**: >1000 jobs/sec
- **并发处理**: 支持16个并发worker无竞态条件
- **内存使用**: 合理的内存占用，无内存泄漏

### 质量门禁
- [ ] 所有测试通过 (unit + integration)
- [ ] 代码覆盖率达标
- [ ] 性能基准达标  
- [ ] 无竞态条件 (`go test -race`)
- [ ] 内存泄漏检查通过
- [ ] Docker测试环境正常运行

## 🚀 开始实施建议

1. **立即开始**: Phase 1 (测试基础设施)
2. **核心优先**: Priority 1 的测试用例最关键
3. **渐进迁移**: 一次迁移一个测试文件
4. **持续集成**: 每完成一个阶段就集成到CI/CD
5. **文档同步**: 测试用例即文档，保持同步更新

---

**总结**: 这是一个全面的测试迁移计划，将pgqueuer的Python测试体系完整迁移到pgqueue4go的Go测试体系。按优先级实施可以确保核心功能首先得到测试覆盖，然后逐步完善高级功能和性能测试。