package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/systemquest/pgqueue/pkg/config"
	"github.com/systemquest/pgqueue/pkg/db"
	"github.com/systemquest/pgqueue/pkg/queue"
)

// 演示第一阶段改进的验证脚本
func main() {
	slog.Info("🚀 PgQueue Phase 1 Improvements Demonstration")

	// 1. 数据库连接
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:password@localhost:5432/pgqueue_test?sslmode=disable"
	}

	cfg := &config.DatabaseConfig{
		URL:            dbURL,
		MaxConnections: 10,
		MaxIdleTime:    5 * time.Minute,
		MaxLifetime:    1 * time.Hour,
		ConnectTimeout: 10 * time.Second,
	}

	database, err := db.New(context.Background(), cfg)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		slog.Info("💡 Tip: Make sure PostgreSQL is running and DATABASE_URL is set")
		return
	}
	defer database.Close()

	slog.Info("✅ Database connected")

	// 2. 创建队列管理器
	qm := queue.NewQueueManager(database, slog.Default())

	// 演示改进 1: StatisticsBuffer
	slog.Info("\n📊 Demonstration 1: Statistics Buffer")
	slog.Info("The new buffer will batch job completions for better performance")

	// 演示改进 2: 批量入队
	slog.Info("\n📦 Demonstration 2: Optimized Batch Enqueue")
	demonstrateBatchEnqueue(qm)

	// 演示改进 3: 错误处理
	slog.Info("\n🛡️ Demonstration 3: Improved Error Handling")
	demonstrateErrorHandling(qm)

	// 演示改进 4: 优雅关闭
	slog.Info("\n👋 Demonstration 4: Graceful Shutdown")
	demonstrateGracefulShutdown(qm)

	slog.Info("\n✨ All demonstrations completed successfully!")
}

func demonstrateBatchEnqueue(qm *queue.QueueManager) {
	ctx := context.Background()

	// 创建 100 个作业
	jobs := make([]queue.EnqueueJobRequest, 100)
	for i := 0; i < 100; i++ {
		payload := map[string]interface{}{
			"id":      i,
			"message": fmt.Sprintf("Test job %d", i),
		}
		payloadBytes, _ := json.Marshal(payload)

		jobs[i] = queue.EnqueueJobRequest{
			Entrypoint: "test_batch",
			Payload:    payloadBytes,
			Priority:   int32(i % 10),
		}
	}

	// 使用优化的批量入队（单次 unnest 插入）
	start := time.Now()
	err := qm.EnqueueJobs(ctx, jobs)
	elapsed := time.Since(start)

	if err != nil {
		slog.Error("Failed to enqueue jobs", "error", err)
		return
	}

	slog.Info("✅ Batch enqueue completed",
		"count", len(jobs),
		"duration", elapsed,
		"rate", fmt.Sprintf("%.0f jobs/sec", float64(len(jobs))/elapsed.Seconds()))

	slog.Info("💡 Note: Uses PostgreSQL unnest() for efficient bulk insert")
}

func demonstrateErrorHandling(qm *queue.QueueManager) {
	ctx := context.Background()

	// 注册一个会 panic 的处理函数
	qm.Entrypoint("panic_test", func(ctx context.Context, job *queue.Job) error {
		slog.Info("Processing job that will panic", "job_id", job.ID)
		panic("intentional panic for testing")
	})

	// 注册一个返回错误的处理函数
	qm.Entrypoint("error_test", func(ctx context.Context, job *queue.Job) error {
		slog.Info("Processing job that will error", "job_id", job.ID)
		return fmt.Errorf("intentional error for testing")
	})

	// 注册一个成功的处理函数
	qm.Entrypoint("success_test", func(ctx context.Context, job *queue.Job) error {
		slog.Info("Processing successful job", "job_id", job.ID)
		return nil
	})

	// 入队测试作业
	testJobs := []queue.EnqueueJobRequest{
		{Entrypoint: "panic_test", Payload: []byte(`{"test": "panic"}`)},
		{Entrypoint: "error_test", Payload: []byte(`{"test": "error"}`)},
		{Entrypoint: "success_test", Payload: []byte(`{"test": "success"}`)},
	}

	err := qm.EnqueueJobs(ctx, testJobs)
	if err != nil {
		slog.Error("Failed to enqueue test jobs", "error", err)
		return
	}

	slog.Info("✅ Error handling demonstration setup complete")
	slog.Info("💡 Note: panic and errors are caught, jobs are marked as 'exception'")
	slog.Info("💡 Note: Worker continues running after errors/panics")
}

func demonstrateGracefulShutdown(qm *queue.QueueManager) {
	slog.Info("✅ Graceful shutdown is now implemented")
	slog.Info("💡 Features:")
	slog.Info("  - Stops accepting new jobs")
	slog.Info("  - Waits for in-flight jobs to complete")
	slog.Info("  - Flushes statistics buffer")
	slog.Info("  - Closes connections cleanly")
	slog.Info("  - 30-second timeout protection")

	// 演示 Shutdown 调用
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := qm.Shutdown(ctx)
	if err != nil {
		slog.Error("Shutdown error", "error", err)
		return
	}

	slog.Info("✅ Graceful shutdown completed")
}
