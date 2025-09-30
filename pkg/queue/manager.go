package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/systemquest/pgqueue4go/pkg/db"
	"github.com/systemquest/pgqueue4go/pkg/db/generated"
	"github.com/systemquest/pgqueue4go/pkg/listener"
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

	// Initialize statistics buffer
	qm.buffer = NewStatisticsBuffer(10, 100*time.Millisecond, qm.flushStatistics)

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

	params := generated.EnqueueJobParams{
		Priority:   priority,
		Entrypoint: entrypoint,
		Payload:    payload,
	}

	err := qm.db.Queries().EnqueueJob(ctx, params)
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

	// For now, do individual operations - we'll optimize with batch later
	for i, job := range jobs {
		if job.Priority == 0 {
			job.Priority = 0 // Default priority
		}

		params := generated.EnqueueJobParams{
			Priority:   job.Priority,
			Entrypoint: job.Entrypoint,
			Payload:    job.Payload,
		}

		err := qm.db.Queries().EnqueueJob(ctx, params)
		if err != nil {
			qm.logger.Error("Failed to enqueue job in batch",
				"error", err,
				"entrypoint", jobs[i].Entrypoint,
				"index", i)
			return fmt.Errorf("failed to enqueue job at index %d: %w", i, err)
		}
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

	var allJobs []*Job

	// First, try to get retry jobs if retry timer is specified
	if retryTimer != nil {
		retryParams := generated.DequeueRetryJobsAtomicParams{
			Column1: entrypoints,
			Column2: pgtype.Interval{Microseconds: retryTimer.Microseconds(), Valid: true},
			Limit:   batchSize,
		}

		retryPgJobs, err := qm.db.Queries().DequeueRetryJobsAtomic(ctx, retryParams)
		if err != nil {
			qm.logger.Error("Failed to dequeue retry jobs atomically", "error", err)
		} else {
			// Convert retry jobs
			for _, pgJob := range retryPgJobs {
				job, err := qm.convertAtomicRetryJob(pgJob)
				if err != nil {
					qm.logger.Error("Failed to convert retry job", "error", err, "job_id", pgJob.ID)
					continue
				}
				allJobs = append(allJobs, job)
			}

			qm.logger.Debug("Atomically dequeued retry jobs", "count", len(retryPgJobs))
		}
	}

	// If we don't have enough jobs, get regular queued jobs atomically
	remainingBatchSize := batchSize - int32(len(allJobs))
	if remainingBatchSize > 0 {
		params := generated.DequeueJobsAtomicParams{
			Column1: entrypoints,
			Limit:   remainingBatchSize,
		}

		pgJobs, err := qm.db.Queries().DequeueJobsAtomic(ctx, params)
		if err != nil {
			qm.logger.Error("Failed to dequeue jobs atomically", "error", err)
			return allJobs, fmt.Errorf("failed to dequeue jobs atomically: %w", err)
		}

		// Convert regular jobs (already marked as picked by atomic query)
		for _, pgJob := range pgJobs {
			job, err := qm.convertAtomicDequeueJob(pgJob)
			if err != nil {
				qm.logger.Error("Failed to convert job", "error", err, "job_id", pgJob.ID)
				continue
			}
			allJobs = append(allJobs, job)
		}

		qm.logger.Debug("Atomically dequeued regular jobs", "count", len(pgJobs))
	}

	qm.logger.Debug("Dequeued jobs", "count", len(allJobs))
	return allJobs, nil
}

// flushStatistics flushes buffered statistics to the database
func (qm *QueueManager) flushStatistics(ctx context.Context, stats []generated.InsertJobStatisticsParams) error {
	if len(stats) == 0 {
		return nil
	}

	qm.logger.Debug("Flushing job statistics", "count", len(stats))

	for _, stat := range stats {
		if err := qm.db.Queries().InsertJobStatistics(ctx, stat); err != nil {
			qm.logger.Error("Failed to insert job statistics", "error", err)
			return err
		}
	}

	return nil
}

// GetQueueStatistics returns queue size statistics
func (qm *QueueManager) GetQueueStatistics(ctx context.Context) ([]*QueueStatistics, error) {
	rows, err := qm.db.Queries().GetQueueSize(ctx)
	if err != nil {
		qm.logger.Error("Failed to get queue statistics", "error", err)
		return nil, fmt.Errorf("failed to get queue statistics: %w", err)
	}

	stats := make([]*QueueStatistics, len(rows))
	for i, row := range rows {
		stats[i] = &QueueStatistics{
			Count:      row.Count,
			Priority:   row.Priority,
			Entrypoint: row.Entrypoint,
			Status:     JobStatus(row.Status),
		}
	}

	return stats, nil
}

// IsAlive returns whether the queue manager is still running
func (qm *QueueManager) IsAlive() bool {
	qm.aliveMu.RLock()
	defer qm.aliveMu.RUnlock()
	return qm.alive
}

// Stop gracefully stops the queue manager
func (qm *QueueManager) Stop() {
	qm.aliveMu.Lock()
	defer qm.aliveMu.Unlock()
	qm.alive = false

	// Stop the statistics buffer
	if qm.buffer != nil {
		qm.buffer.Stop()
	}

	qm.logger.Info("Queue manager stopped")
}

// convertPgJob converts sqlc generated job to our Job type
func (qm *QueueManager) convertPgJob(pgJob generated.PgqueueJobs) (*Job, error) {
	job := &Job{
		ID:         pgJob.ID,
		Priority:   pgJob.Priority,
		Status:     JobStatus(pgJob.Status),
		Entrypoint: pgJob.Entrypoint,
		Payload:    pgJob.Payload,
	}

	// Convert timestamps
	if pgJob.Created.Valid {
		job.Created = pgJob.Created.Time
	}

	if pgJob.Updated.Valid {
		job.Updated = pgJob.Updated.Time
	}

	return job, nil
}

// convertAtomicDequeueJob converts atomic dequeue row to our Job type
func (qm *QueueManager) convertAtomicDequeueJob(row generated.DequeueJobsAtomicRow) (*Job, error) {
	job := &Job{
		ID:         row.ID,
		Priority:   row.Priority,
		Status:     JobStatus(row.Status),
		Entrypoint: row.Entrypoint,
		Payload:    row.Payload,
	}

	// Convert timestamps
	if row.Created.Valid {
		job.Created = row.Created.Time
	}

	if row.Updated.Valid {
		job.Updated = row.Updated.Time
	}

	return job, nil
}

// convertAtomicRetryJob converts atomic retry dequeue row to our Job type
func (qm *QueueManager) convertAtomicRetryJob(row generated.DequeueRetryJobsAtomicRow) (*Job, error) {
	job := &Job{
		ID:         row.ID,
		Priority:   row.Priority,
		Status:     JobStatus(row.Status),
		Entrypoint: row.Entrypoint,
		Payload:    row.Payload,
	}

	// Convert timestamps
	if row.Created.Valid {
		job.Created = row.Created.Time
	}

	if row.Updated.Valid {
		job.Updated = row.Updated.Time
	}

	return job, nil
}

// HasUpdatedColumn checks if the jobs table has the updated column (pgqueuer compatibility)
func (qm *QueueManager) HasUpdatedColumn(ctx context.Context) (bool, error) {
	return qm.db.Queries().HasUpdatedColumn(ctx, generated.HasUpdatedColumnParams{
		TableName:  "pgqueue_jobs",
		ColumnName: "updated",
	})
}

// GetSchemaVersion returns the current schema version
func (qm *QueueManager) GetSchemaVersion(ctx context.Context) (int32, error) {
	return qm.db.Queries().GetSchemaVersion(ctx)
}

// EnqueueJobRequest represents a job to be enqueued
type EnqueueJobRequest struct {
	Entrypoint string
	Payload    []byte
	Priority   int32
}
