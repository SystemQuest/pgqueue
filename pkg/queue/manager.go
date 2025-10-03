package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/systemquest/pgqueue/pkg/db"
	"github.com/systemquest/pgqueue/pkg/listener"
	"github.com/systemquest/pgqueue/pkg/queries"
)

// EntrypointFunc represents a job processing function
type EntrypointFunc func(ctx context.Context, job *Job) error

// QueueManager manages job queues and dispatches jobs to registered entrypoints
type QueueManager struct {
	db         *db.DB
	logger     *slog.Logger
	channel    string
	alive      bool
	aliveMu    sync.RWMutex
	registry   map[string]EntrypointFunc
	registryMu sync.RWMutex
	listener   *listener.Listener
	buffer     *StatisticsBuffer
}

// NewQueueManager creates a new queue manager instance
func NewQueueManager(database *db.DB, logger *slog.Logger) *QueueManager {
	if logger == nil {
		logger = slog.Default()
	}

	qm := &QueueManager{
		db:       database,
		logger:   logger,
		channel:  "pgqueue_events", // Default channel name, matches trigger
		alive:    true,
		registry: make(map[string]EntrypointFunc),
	}

	// Initialize event listener
	qm.listener = listener.NewListener(database.Pool(), qm.channel, logger)

	// Initialize statistics buffer with proper callback
	// Similar to PgQueuer: JobBuffer(max_size=10, timeout=0.01s, flush_callback=queries.log_jobs)
	qm.buffer = NewStatisticsBuffer(10, 100*time.Millisecond, qm.flushStatistics, logger)

	return qm
}

// SetChannel sets the PostgreSQL notification channel
func (qm *QueueManager) SetChannel(channel string) {
	qm.channel = channel
}

// Entrypoint registers a function as an entrypoint for handling specific job types
func (qm *QueueManager) Entrypoint(name string, fn EntrypointFunc) error {
	qm.registryMu.Lock()
	defer qm.registryMu.Unlock()

	if _, exists := qm.registry[name]; exists {
		return fmt.Errorf("%s already in registry, name must be unique", name)
	}

	qm.registry[name] = fn
	qm.logger.Debug("Registered entrypoint", "name", name)
	return nil
}

// EnqueueJob adds a new job to the queue
func (qm *QueueManager) EnqueueJob(ctx context.Context, entrypoint string, payload []byte, priority int32) error {
	if priority == 0 {
		priority = 0 // Default priority
	}

	err := qm.db.Queries().EnqueueJob(ctx, int(priority), entrypoint, payload)
	if err != nil {
		qm.logger.Error("Failed to enqueue job", "error", err, "entrypoint", entrypoint)
		return fmt.Errorf("failed to enqueue job: %w", err)
	}

	qm.logger.Debug("Enqueued job", "entrypoint", entrypoint, "priority", priority)
	return nil
}

// EnqueueJobs adds multiple jobs to the queue in a batch
func (qm *QueueManager) EnqueueJobs(ctx context.Context, jobs []EnqueueJobRequest) error {
	if len(jobs) == 0 {
		return nil
	}

	// Convert to the new Queries API format
	var params []queries.EnqueueJobParams
	for _, job := range jobs {
		if job.Priority == 0 {
			job.Priority = 0 // Default priority
		}
		params = append(params, queries.EnqueueJobParams{
			Priority:   int(job.Priority),
			Entrypoint: job.Entrypoint,
			Payload:    job.Payload,
		})
	}

	err := qm.db.Queries().EnqueueJobs(ctx, params)
	if err != nil {
		qm.logger.Error("Failed to enqueue jobs batch", "error", err)
		return fmt.Errorf("failed to enqueue jobs batch: %w", err)
	}

	qm.logger.Debug("Enqueued jobs batch", "count", len(jobs))
	return nil
}

// DequeueJobs retrieves jobs from the queue for processing
func (qm *QueueManager) DequeueJobs(ctx context.Context, batchSize int32, entrypoints []string) ([]*Job, error) {
	return qm.DequeueJobsWithRetry(ctx, batchSize, entrypoints, nil)
}

// DequeueJobsWithRetry retrieves jobs from the queue for processing, including retry logic
func (qm *QueueManager) DequeueJobsWithRetry(ctx context.Context, batchSize int32, entrypoints []string, retryTimer *time.Duration) ([]*Job, error) {
	if batchSize <= 0 {
		batchSize = 10 // Default batch size
	}

	if len(entrypoints) == 0 {
		// Get all registered entrypoints if none specified
		qm.registryMu.RLock()
		for name := range qm.registry {
			entrypoints = append(entrypoints, name)
		}
		qm.registryMu.RUnlock()
	}

	if len(entrypoints) == 0 {
		return nil, fmt.Errorf("no entrypoints registered or specified")
	}

	// Use the new Queries API
	params := queries.DequeueJobsParams{
		BatchSize:    int(batchSize),
		Entrypoints:  entrypoints,
		RetryTimeout: retryTimer,
	}

	jobs, err := qm.db.Queries().DequeueJobs(ctx, params)
	if err != nil {
		qm.logger.Error("Failed to dequeue jobs", "error", err)
		return nil, fmt.Errorf("failed to dequeue jobs: %w", err)
	}

	// Convert to pointer slice
	result := make([]*Job, len(jobs))
	for i, job := range jobs {
		result[i] = &Job{
			ID:         int32(job.ID),
			Priority:   int32(job.Priority),
			Created:    job.Created,
			Updated:    job.Updated,
			Status:     JobStatus(job.Status),
			Entrypoint: job.Entrypoint,
			Payload:    job.Payload,
		}
	}

	qm.logger.Debug("Dequeued jobs", "count", len(result))
	return result, nil
}

// flushStatistics flushes buffered statistics to the database
// This implements PgQueuer's queries.log_jobs functionality with batch aggregation
func (qm *QueueManager) flushStatistics(ctx context.Context, completions []JobCompletion) error {
	if len(completions) == 0 {
		return nil
	}

	qm.logger.Debug("Flushing statistics", "count", len(completions))

	// Convert to queries.JobStatus format
	jobStatuses := make([]queries.JobStatus, len(completions))
	for i, completion := range completions {
		jobStatuses[i] = queries.JobStatus{
			JobID:  int(completion.Job.ID),
			Status: string(completion.Status),
		}
	}

	// Batch log jobs to statistics table
	// This aligns with PgQueuer's log_jobs which:
	// 1. Deletes jobs from queue
	// 2. Aggregates statistics
	// 3. Uses ON CONFLICT to update counts
	if err := qm.db.Queries().CompleteJobs(ctx, jobStatuses); err != nil {
		qm.logger.Error("Failed to complete jobs", "error", err)
		return fmt.Errorf("failed to complete jobs: %w", err)
	}

	qm.logger.Debug("Successfully flushed statistics", "count", len(completions))
	return nil
}

// GetQueueStatistics returns queue size statistics
func (qm *QueueManager) GetQueueStatistics(ctx context.Context) ([]*QueueStatistics, error) {
	stats, err := qm.db.Queries().QueueSize(ctx)
	if err != nil {
		qm.logger.Error("Failed to get queue statistics", "error", err)
		return nil, fmt.Errorf("failed to get queue statistics: %w", err)
	}

	result := make([]*QueueStatistics, len(stats))
	for i, stat := range stats {
		result[i] = &QueueStatistics{
			Count:      int64(stat.Count),
			Priority:   int32(stat.Priority),
			Entrypoint: stat.Entrypoint,
			Status:     JobStatus(stat.Status),
		}
	}

	return result, nil
}

// IsAlive returns whether the queue manager is still running
func (qm *QueueManager) IsAlive() bool {
	qm.aliveMu.RLock()
	defer qm.aliveMu.RUnlock()
	return qm.alive
}

// Stop immediately stops accepting new jobs
// For graceful shutdown, use Shutdown() instead
func (qm *QueueManager) Stop() {
	qm.aliveMu.Lock()
	qm.alive = false
	qm.aliveMu.Unlock()

	qm.logger.Info("Queue manager stopped")
}

// Shutdown performs graceful shutdown of the queue manager
// This waits for in-flight jobs to complete and flushes statistics buffer
// Similar to PgQueuer's cleanup on context cancellation
func (qm *QueueManager) Shutdown(ctx context.Context) error {
	qm.logger.Info("Starting graceful shutdown")

	// Stop accepting new jobs
	qm.Stop()

	// Stop the event listener first to prevent new job events
	if qm.listener != nil {
		if err := qm.listener.Stop(ctx); err != nil {
			qm.logger.Warn("Error stopping listener", "error", err)
		}
	}

	// Flush statistics buffer and wait for completion
	// This aligns with PgQueuer's buffer.alive = False behavior
	if qm.buffer != nil {
		if err := qm.buffer.Stop(); err != nil {
			qm.logger.Error("Error stopping statistics buffer", "error", err)
			return fmt.Errorf("failed to stop statistics buffer: %w", err)
		}
	}

	qm.logger.Info("Graceful shutdown completed")
	return nil
}

// HasUpdatedColumn checks if the jobs table has the updated column (pgqueuer compatibility)
func (qm *QueueManager) HasUpdatedColumn(ctx context.Context) (bool, error) {
	return qm.db.Queries().HasUpdatedColumn(ctx)
}

// EnqueueJobRequest represents a job to be enqueued
type EnqueueJobRequest struct {
	Entrypoint string
	Payload    []byte
	Priority   int32
}
