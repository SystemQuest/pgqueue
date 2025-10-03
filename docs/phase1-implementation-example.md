# Phase 1 Implementation Example - Test Infrastructure

## 立即可开始的实施示例

### 1. 创建测试基础设施

#### 创建测试工具包
```bash
# 创建测试目录结构
mkdir -p test/testutil
mkdir -p test/integration  
mkdir -p test/docker
```

#### test/testutil/db.go
```go
package testutil

import (
    "context"
    "fmt"
    "os"
    "strings"
    "testing"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/systemquest/pgqueue/pkg/config"
    "github.com/systemquest/pgqueue/pkg/db"
    "github.com/systemquest/pgqueue/pkg/queries"
)

// TestDBConfig returns test database configuration
func TestDBConfig() *config.DatabaseConfig {
    return &config.DatabaseConfig{
        URL:            TestDSN("postgres"),
        MaxConnections: 5,
        MaxIdleTime:    time.Minute,
        MaxLifetime:    time.Hour,
        ConnectTimeout: 10 * time.Second,
    }
}

// TestDSN generates test database connection string
func TestDSN(dbName string) string {
    host := getEnv("PGHOST", "localhost")
    user := getEnv("PGUSER", "testuser")
    password := getEnv("PGPASSWORD", "testpassword")
    port := getEnv("PGPORT", "5432")
    
    if dbName == "" {
        dbName = "testdb"
    }
    
    return fmt.Sprintf("postgres://%s:%s@%s:%s/%s", 
        user, password, host, port, dbName)
}

// SetupTestDB creates a temporary test database
func SetupTestDB(t *testing.T) *db.DB {
    // Generate unique database name
    dbName := fmt.Sprintf("test_%s_%d",
        strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_"),
        time.Now().UnixNano())
    
    // Connect to postgres to create test database
    adminConn, err := pgx.Connect(context.Background(), TestDSN("postgres"))
    if err != nil {
        t.Fatalf("Failed to connect to admin database: %v", err)
    }
    defer adminConn.Close(context.Background())
    
    // Create test database
    _, err = adminConn.Exec(context.Background(), 
        fmt.Sprintf("CREATE DATABASE %s TEMPLATE testdb", dbName))
    if err != nil {
        t.Fatalf("Failed to create test database: %v", err)
    }
    
    // Connect to test database
    testDB, err := db.New(context.Background(), &config.DatabaseConfig{
        URL:            TestDSN(dbName),
        MaxConnections: 5,
        MaxIdleTime:    time.Minute,
        MaxLifetime:    time.Hour,
        ConnectTimeout: 10 * time.Second,
    })
    if err != nil {
        t.Fatalf("Failed to connect to test database: %v", err)
    }
    
    // Install schema
    qb := queries.NewQueryBuilder()
    _, err = testDB.Pool().Exec(context.Background(), qb.CreateInstallQuery())
    if err != nil {
        t.Fatalf("Failed to install schema: %v", err)
    }
    
    // Register cleanup
    t.Cleanup(func() {
        testDB.Close()
        adminConn, err := pgx.Connect(context.Background(), TestDSN("postgres"))
        if err == nil {
            adminConn.Exec(context.Background(), 
                fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", dbName))
            adminConn.Close(context.Background())
        }
    })
    
    return testDB
}

func getEnv(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}
```

#### test/testutil/jobs.go
```go
package testutil

import (
    "fmt"
    "testing"
    "time"

    "github.com/systemquest/pgqueue/pkg/queue"
    "github.com/stretchr/testify/assert"
)

// CreateTestJobs creates test jobs for testing
func CreateTestJobs(n int) []*queue.Job {
    jobs := make([]*queue.Job, n)
    for i := 0; i < n; i++ {
        jobs[i] = &queue.Job{
            Priority:   int32(i % 10),
            Entrypoint: "test.job",
            Payload:    []byte(fmt.Sprintf(`{"id": %d, "data": "test_%d"}`, i, i)),
        }
    }
    return jobs
}

// AssertJobsEqual checks if job lists are equal (ignoring ID and timestamps)
func AssertJobsEqual(t *testing.T, expected, actual []*queue.Job) {
    assert.Equal(t, len(expected), len(actual))
    
    for i := range expected {
        assert.Equal(t, expected[i].Priority, actual[i].Priority)
        assert.Equal(t, expected[i].Entrypoint, actual[i].Entrypoint)
        assert.Equal(t, expected[i].Payload, actual[i].Payload)
    }
}

// WaitForCondition waits for a condition to be true with timeout
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, message string) {
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        if condition() {
            return
        }
        time.Sleep(10 * time.Millisecond)
    }
    t.Fatalf("Timeout waiting for condition: %s", message)
}
```

### 2. 第一个测试用例迁移示例

#### pkg/queue/manager_test.go (开始迁移 test_qm.py)
```go
package queue

import (
    "context"
    "sync"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/systemquest/pgqueue/test/testutil"
)

// 迁移 test_job_queing - 基础作业排队测试
func TestJobQueuing(t *testing.T) {
    testCases := []struct {
        name string
        N    int
    }{
        {"Single", 1},
        {"Dual", 2}, 
        {"Many", 32},
    }
    
    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            // Setup test database
            testDB := testutil.SetupTestDB(t)
            
            // Create queue manager
            qm := NewQueueManager(testDB, nil)
            
            // Track processed jobs
            var processedJobs []int
            var mu sync.Mutex
            
            // Register entrypoint
            err := qm.Entrypoint("test.fetch", func(ctx context.Context, job *Job) error {
                mu.Lock()
                defer mu.Unlock()
                
                if job.Payload == nil {
                    // Stop signal
                    qm.Stop()
                    return nil
                }
                
                var jobData struct {
                    ID int `json:"id"`
                }
                if err := json.Unmarshal(job.Payload, &jobData); err != nil {
                    return err
                }
                
                processedJobs = append(processedJobs, jobData.ID)
                return nil
            })
            require.NoError(t, err)
            
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            
            // Enqueue test jobs
            for i := 0; i < tc.N; i++ {
                payload := fmt.Sprintf(`{"id": %d}`, i)
                err := qm.EnqueueJob(ctx, "test.fetch", []byte(payload), 0)
                require.NoError(t, err)
            }
            
            // Enqueue stop signal
            err = qm.EnqueueJob(ctx, "test.fetch", nil, 0)
            require.NoError(t, err)
            
            // Start processing (should stop automatically)
            done := make(chan struct{})
            go func() {
                defer close(done)
                // Run queue manager until stop signal
                for qm.IsAlive() {
                    time.Sleep(10 * time.Millisecond)
                }
            }()
            
            // Wait for completion
            select {
            case <-done:
                // Success
            case <-ctx.Done():
                t.Fatal("Test timed out")
            }
            
            // Verify results
            mu.Lock()
            defer mu.Unlock()
            
            assert.Len(t, processedJobs, tc.N)
            
            // Check all jobs were processed (order may vary)
            expected := make([]int, tc.N)
            for i := 0; i < tc.N; i++ {
                expected[i] = i
            }
            
            assert.ElementsMatch(t, expected, processedJobs)
        })
    }
}

// 迁移 test_job_fetch - 并发获取测试
func TestJobFetchConcurrent(t *testing.T) {
    testCases := []struct {
        name        string
        N           int
        concurrency int
    }{
        {"1x1", 1, 1},
        {"2x2", 2, 2},
        {"32x4", 32, 4},
    }
    
    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            testDB := testutil.SetupTestDB(t)
            
            // Create multiple queue managers for concurrency
            var qmPool []*QueueManager
            for i := 0; i < tc.concurrency; i++ {
                qm := NewQueueManager(testDB, nil)
                qmPool = append(qmPool, qm)
            }
            
            var processedJobs []int
            var mu sync.Mutex
            
            // Register entrypoint for all managers
            for _, qm := range qmPool {
                err := qm.Entrypoint("test.fetch", func(ctx context.Context, job *Job) error {
                    mu.Lock()
                    defer mu.Unlock()
                    
                    if job.Payload == nil {
                        // Stop all managers
                        for _, manager := range qmPool {
                            manager.Stop()
                        }
                        return nil
                    }
                    
                    var jobData struct {
                        ID int `json:"id"`
                    }
                    if err := json.Unmarshal(job.Payload, &jobData); err != nil {
                        return err
                    }
                    
                    processedJobs = append(processedJobs, jobData.ID)
                    return nil
                })
                require.NoError(t, err)
            }
            
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            defer cancel()
            
            // Enqueue test jobs using first manager
            qm := qmPool[0]
            for i := 0; i < tc.N; i++ {
                payload := fmt.Sprintf(`{"id": %d}`, i)
                err := qm.EnqueueJob(ctx, "test.fetch", []byte(payload), 0)
                require.NoError(t, err)
            }
            
            // Enqueue stop signal
            err := qm.EnqueueJob(ctx, "test.fetch", nil, 0)
            require.NoError(t, err)
            
            // Start all managers
            var wg sync.WaitGroup
            for _, manager := range qmPool {
                wg.Add(1)
                go func(qm *QueueManager) {
                    defer wg.Done()
                    for qm.IsAlive() {
                        time.Sleep(10 * time.Millisecond)
                    }
                }(manager)
            }
            
            // Wait for completion
            done := make(chan struct{})
            go func() {
                wg.Wait()
                close(done)
            }()
            
            select {
            case <-done:
                // Success  
            case <-ctx.Done():
                t.Fatal("Test timed out")
            }
            
            // Verify results
            mu.Lock()
            defer mu.Unlock()
            
            assert.Len(t, processedJobs, tc.N)
            
            // Check all jobs were processed
            expected := make([]int, tc.N)
            for i := 0; i < tc.N; i++ {
                expected[i] = i
            }
            
            assert.ElementsMatch(t, expected, processedJobs)
        })
    }
}
```

### 3. Docker测试环境

#### test/docker/docker-compose.test.yml
```yaml
version: '3.8'

services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: testuser
      POSTGRES_PASSWORD: testpassword
      POSTGRES_DB: postgres
    ports:
      - "5432:5432"
    volumes:
      - ./init_test_db.sh:/docker-entrypoint-initdb.d/init_test_db.sh
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U testuser"]
      interval: 5s
      timeout: 5s
      retries: 5

  test-runner:
    build:
      context: ../..
      dockerfile: test/docker/Dockerfile.test
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      PGHOST: postgres
      PGUSER: testuser
      PGPASSWORD: testpassword
      PGDATABASE: testdb
      PGPORT: 5432
    volumes:
      - ../..:/workspace
    working_dir: /workspace
    command: ["go", "test", "-v", "./..."]
```

#### test/docker/init_test_db.sh
```bash
#!/bin/bash
set -e

# Create testdb template database
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" <<-EOSQL
    CREATE DATABASE testdb;
EOSQL

echo "Test database template created successfully"
```

### 4. Makefile测试命令

#### Makefile (添加测试相关命令)
```makefile
# Test commands
.PHONY: test test-unit test-integration test-docker test-coverage

test: test-unit

test-unit:
	go test -v -race ./pkg/...

test-integration:
	go test -v -race ./test/integration/...

test-docker:
	docker-compose -f test/docker/docker-compose.test.yml up --build --abort-on-container-exit

test-coverage:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

test-clean:
	docker-compose -f test/docker/docker-compose.test.yml down -v
	rm -f coverage.out coverage.html
```

### 5. GitHub Actions CI配置

#### .github/workflows/test.yml
```yaml
name: Tests

on:
  push:
    branches: [ main, develop ]
  pull_request:
    branches: [ main ]

jobs:
  test:
    runs-on: ubuntu-latest
    
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: testuser
          POSTGRES_PASSWORD: testpassword
          POSTGRES_DB: postgres
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
        ports:
          - 5432:5432

    steps:
    - uses: actions/checkout@v4
    
    - name: Set up Go
      uses: actions/setup-go@v4
      with:
        go-version: '1.21'
        
    - name: Setup test database
      run: |
        PGPASSWORD=testpassword psql -h localhost -U testuser -d postgres -c "CREATE DATABASE testdb;"
      env:
        PGHOST: localhost
        PGPORT: 5432
        
    - name: Download dependencies
      run: go mod download
      
    - name: Run tests
      run: go test -v -race -coverprofile=coverage.out ./...
      env:
        PGHOST: localhost
        PGUSER: testuser
        PGPASSWORD: testpassword
        PGDATABASE: testdb
        PGPORT: 5432
        
    - name: Upload coverage reports
      uses: codecov/codecov-action@v3
      with:
        file: ./coverage.out
```

## 立即行动项

1. **创建目录结构**:
```bash
mkdir -p test/testutil test/integration test/docker
```

2. **创建基础测试工具** (复制上面的 `test/testutil/db.go` 和 `test/testutil/jobs.go`)

3. **创建第一个测试** (复制上面的 `pkg/queue/manager_test.go`)

4. **设置Docker环境** (复制上面的docker配置)

5. **运行第一个测试**:
```bash
# 启动PostgreSQL (如果需要)
docker-compose -f test/docker/docker-compose.test.yml up -d postgres

# 运行测试
go test -v ./pkg/queue/
```

这样就可以开始Phase 1的实施，为后续的测试迁移奠定基础。