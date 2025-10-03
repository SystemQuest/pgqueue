package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/systemquest/pgqueue/pkg/config"
	"github.com/systemquest/pgqueue/pkg/db"
	"github.com/systemquest/pgqueue/pkg/queue"
)

// TaskPayload represents a simple task payload
type TaskPayload struct {
	UserID  int    `json:"user_id"`
	Action  string `json:"action"`
	Message string `json:"message"`
}

func main() {
	// Setup logging
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Database configuration
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:password@localhost:5432/pgqueue?sslmode=disable"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("Received shutdown signal")
		cancel()
	}()

	if err := run(ctx, dbURL); err != nil {
		slog.Error("Application failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dbURL string) error {
	slog.Info("Starting PgQueue4Go example")

	// Setup database connection
	cfg := &config.DatabaseConfig{
		URL:            dbURL,
		MaxConnections: 10,
		MaxIdleTime:    5 * time.Minute,
		MaxLifetime:    1 * time.Hour,
		ConnectTimeout: 10 * time.Second,
	}

	database, err := db.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer database.Close()

	// Create queue manager
	qm := queue.NewQueueManager(database, slog.Default())

	// Register job handlers
	if err := registerHandlers(qm); err != nil {
		return fmt.Errorf("failed to register handlers: %w", err)
	}

	// Enqueue some example jobs
	if err := enqueueExampleJobs(ctx, qm); err != nil {
		return fmt.Errorf("failed to enqueue example jobs: %w", err)
	}

	// Show queue statistics
	if err := showQueueStats(ctx, qm); err != nil {
		return fmt.Errorf("failed to show queue stats: %w", err)
	}

	// Start processing jobs with event-driven approach
	slog.Info("Starting event-driven job processing...")

	opts := &queue.RunOptions{
		DequeueTimeout: 30 * time.Second,
		BatchSize:      5,
		WorkerPoolSize: 3,
		RetryTimer:     nil,
	}

	return qm.RunWithEvents(ctx, opts)
}

func registerHandlers(qm *queue.QueueManager) error {
	// Email notification handler
	err := qm.Entrypoint("send_email", func(ctx context.Context, job *queue.Job) error {
		var payload TaskPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("invalid payload: %w", err)
		}

		slog.Info("Sending email",
			"job_id", job.ID,
			"user_id", payload.UserID,
			"message", payload.Message)

		// Simulate email sending
		time.Sleep(500 * time.Millisecond)

		slog.Info("Email sent successfully", "job_id", job.ID)
		return nil
	})
	if err != nil {
		return err
	}

	// Push notification handler
	err = qm.Entrypoint("send_push", func(ctx context.Context, job *queue.Job) error {
		var payload TaskPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("invalid payload: %w", err)
		}

		slog.Info("Sending push notification",
			"job_id", job.ID,
			"user_id", payload.UserID,
			"message", payload.Message)

		// Simulate push sending
		time.Sleep(200 * time.Millisecond)

		slog.Info("Push notification sent successfully", "job_id", job.ID)
		return nil
	})
	if err != nil {
		return err
	}

	// Data processing handler
	err = qm.Entrypoint("process_data", func(ctx context.Context, job *queue.Job) error {
		var payload TaskPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("invalid payload: %w", err)
		}

		slog.Info("Processing data",
			"job_id", job.ID,
			"user_id", payload.UserID,
			"action", payload.Action)

		// Simulate data processing
		time.Sleep(1 * time.Second)

		slog.Info("Data processing completed", "job_id", job.ID)
		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func enqueueExampleJobs(ctx context.Context, qm *queue.QueueManager) error {
	jobs := []queue.EnqueueJobRequest{
		{
			Entrypoint: "send_email",
			Priority:   5, // High priority
			Payload:    mustMarshal(TaskPayload{UserID: 1, Action: "welcome", Message: "Welcome to our service!"}),
		},
		{
			Entrypoint: "send_push",
			Priority:   3, // Medium priority
			Payload:    mustMarshal(TaskPayload{UserID: 1, Action: "reminder", Message: "Don't forget to complete your profile"}),
		},
		{
			Entrypoint: "process_data",
			Priority:   1, // Low priority
			Payload:    mustMarshal(TaskPayload{UserID: 1, Action: "analytics", Message: "Process user analytics"}),
		},
		{
			Entrypoint: "send_email",
			Priority:   4,
			Payload:    mustMarshal(TaskPayload{UserID: 2, Action: "notification", Message: "You have a new message"}),
		},
		{
			Entrypoint: "send_push",
			Priority:   2,
			Payload:    mustMarshal(TaskPayload{UserID: 2, Action: "update", Message: "App update available"}),
		},
	}

	return qm.EnqueueJobs(ctx, jobs)
}

func showQueueStats(ctx context.Context, qm *queue.QueueManager) error {
	stats, err := qm.GetQueueStatistics(ctx)
	if err != nil {
		return err
	}

	slog.Info("Current queue statistics:")
	for _, stat := range stats {
		slog.Info("Queue status",
			"entrypoint", stat.Entrypoint,
			"status", stat.Status,
			"priority", stat.Priority,
			"count", stat.Count)
	}

	return nil
}

func mustMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
