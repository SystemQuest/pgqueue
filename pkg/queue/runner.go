package queue

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/systemquest/pgqueue4go/pkg/db/generated"
	"github.com/systemquest/pgqueue4go/pkg/listener"
)

// RunOptions contains configuration for running the queue manager
type RunOptions struct {
	DequeueTimeout time.Duration  // Timeout for waiting for jobs
	BatchSize      int32          // Number of jobs to process in each batch
	WorkerPoolSize int            // Number of concurrent workers
	RetryTimer     *time.Duration // If set, retry jobs that have been in 'picked' status too long
}

// DefaultRunOptions returns sensible default options
func DefaultRunOptions() *RunOptions {
	return &RunOptions{
		DequeueTimeout: 30 * time.Second,
		BatchSize:      10,
		WorkerPoolSize: 5,
		RetryTimer:     nil, // Disabled by default
	}
}

// RunWithEvents starts the queue manager with event-driven job processing
func (qm *QueueManager) RunWithEvents(ctx context.Context, opts *RunOptions) error {
	if opts == nil {
		opts = DefaultRunOptions()
	}

	qm.logger.Info("Starting event-driven queue manager",
		"dequeue_timeout", opts.DequeueTimeout,
		"batch_size", opts.BatchSize,
		"worker_pool_size", opts.WorkerPoolSize,
		"channel", qm.channel)

	// Check if we have any registered entrypoints
	qm.registryMu.RLock()
	if len(qm.registry) == 0 {
		qm.registryMu.RUnlock()
		return fmt.Errorf("no entrypoints registered")
	}
	entrypoints := make([]string, 0, len(qm.registry))
	for name := range qm.registry {
		entrypoints = append(entrypoints, name)
	}
	qm.registryMu.RUnlock()

	// Create worker pool
	workerPool := make(chan *Job, opts.WorkerPoolSize*2)
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < opts.WorkerPoolSize; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			qm.worker(ctx, workerID, workerPool)
		}(i)
	}

	// Setup event handlers
	qm.listener.AddHandler(listener.OperationInsert, func(event *listener.Event) error {
		qm.logger.Debug("Received insert event", "latency", event.Latency())

		// Process jobs when new ones are inserted
		jobs, err := qm.DequeueJobsWithRetry(ctx, opts.BatchSize, entrypoints, opts.RetryTimer)
		if err != nil {
			qm.logger.Error("Failed to dequeue jobs on insert event", "error", err)
			return err
		}

		// Send jobs to worker pool
		for _, job := range jobs {
			select {
			case workerPool <- job:
				// Job sent to worker
			case <-ctx.Done():
				return ctx.Err()
			default:
				// Worker pool is full, jobs will be processed later
				qm.logger.Debug("Worker pool full, job will be processed later", "job_id", job.ID)
			}
		}

		return nil
	})

	// Start the event listener
	if err := qm.listener.Start(ctx); err != nil {
		return fmt.Errorf("failed to start event listener: %w", err)
	}
	defer qm.listener.Stop(ctx)

	// Initial processing of existing jobs
	qm.logger.Info("Processing existing jobs")
	jobs, err := qm.DequeueJobsWithRetry(ctx, opts.BatchSize, entrypoints, opts.RetryTimer)
	if err != nil {
		qm.logger.Error("Failed to dequeue initial jobs", "error", err)
	} else {
		for _, job := range jobs {
			select {
			case workerPool <- job:
				// Job sent to worker
			case <-ctx.Done():
				qm.Stop()
				// Close worker pool and wait for workers to finish
				close(workerPool)
				wg.Wait()
				qm.logger.Info("Event-driven queue manager stopped")
				return ctx.Err()
			}
		}
	}

	// Main loop - just wait for context cancellation or periodic processing
	ticker := time.NewTicker(opts.DequeueTimeout)
	defer ticker.Stop()

	for qm.IsAlive() {
		select {
		case <-ctx.Done():
			qm.logger.Info("Context cancelled, stopping event-driven queue manager")
			qm.Stop()
			// Close worker pool and wait for workers to finish
			close(workerPool)
			wg.Wait()
			qm.logger.Info("Event-driven queue manager stopped")
			return ctx.Err()

		case <-ticker.C:
			// Periodic processing in case events are missed
			qm.logger.Debug("Periodic job processing check")
			jobs, err := qm.DequeueJobsWithRetry(ctx, opts.BatchSize, entrypoints, opts.RetryTimer)
			if err != nil {
				qm.logger.Error("Failed to dequeue jobs during periodic check", "error", err)
				continue
			}

			for _, job := range jobs {
				select {
				case workerPool <- job:
					// Job sent to worker
				case <-ctx.Done():
					qm.Stop()
					// Close worker pool and wait for workers to finish
					close(workerPool)
					wg.Wait()
					qm.logger.Info("Event-driven queue manager stopped")
					return ctx.Err()
				default:
					// Worker pool is full, skip for now
				}
			}
		}
	}

	// This should not be reached under normal circumstances
	// Close worker pool and wait for workers to finish
	close(workerPool)
	wg.Wait()

	qm.logger.Info("Event-driven queue manager stopped")
	return nil
}

// Run starts the queue manager and continuously processes jobs
func (qm *QueueManager) Run(ctx context.Context, opts *RunOptions) error {
	if opts == nil {
		opts = DefaultRunOptions()
	}

	// Check schema version for compatibility (pgqueuer alignment)
	version, err := qm.GetSchemaVersion(ctx)
	if err != nil {
		qm.logger.Warn("Could not retrieve schema version", "error", err)
	} else {
		qm.logger.Info("Schema version", "version", version)
	}

	// Check for updated column (pgqueuer compatibility)
	hasUpdated, err := qm.HasUpdatedColumn(ctx)
	if err != nil {
		qm.logger.Warn("Could not check updated column", "error", err)
	} else if hasUpdated {
		qm.logger.Info("Jobs table has updated column (pgqueuer compatible)")
	}

	qm.logger.Info("Starting queue manager",
		"dequeue_timeout", opts.DequeueTimeout,
		"batch_size", opts.BatchSize,
		"worker_pool_size", opts.WorkerPoolSize)

	// Check if we have any registered entrypoints
	qm.registryMu.RLock()
	if len(qm.registry) == 0 {
		qm.registryMu.RUnlock()
		return fmt.Errorf("no entrypoints registered")
	}
	entrypoints := make([]string, 0, len(qm.registry))
	for name := range qm.registry {
		entrypoints = append(entrypoints, name)
	}
	qm.registryMu.RUnlock()

	// Create worker pool
	workerPool := make(chan *Job, opts.WorkerPoolSize*2) // Buffer for jobs
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < opts.WorkerPoolSize; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			qm.worker(ctx, workerID, workerPool)
		}(i)
	}

	// Main processing loop
	for qm.IsAlive() {
		select {
		case <-ctx.Done():
			qm.logger.Info("Context cancelled, stopping queue manager")
			qm.Stop()
			goto cleanup

		default:
			// Dequeue jobs
			jobs, err := qm.DequeueJobsWithRetry(ctx, opts.BatchSize, entrypoints, opts.RetryTimer)
			if err != nil {
				qm.logger.Error("Failed to dequeue jobs", "error", err)
				// Continue processing on error
				time.Sleep(1 * time.Second)
				continue
			}

			if len(jobs) == 0 {
				// No jobs available, wait for timeout
				qm.logger.Debug("No jobs available, waiting", "timeout", opts.DequeueTimeout)
				select {
				case <-ctx.Done():
					qm.Stop()
				case <-time.After(opts.DequeueTimeout):
					// Continue processing
				}
				continue
			}

			// Send jobs to worker pool
			for _, job := range jobs {
				select {
				case workerPool <- job:
					// Job sent to worker
				case <-ctx.Done():
					qm.Stop()
					goto cleanup
				}
			}
		}
	}

cleanup:
	// Close worker pool and wait for workers to finish
	close(workerPool)
	wg.Wait()

	qm.logger.Info("Queue manager stopped")
	return nil
}

// worker processes jobs from the worker pool
func (qm *QueueManager) worker(ctx context.Context, workerID int, jobPool <-chan *Job) {
	qm.logger.Debug("Worker started", "worker_id", workerID)
	defer qm.logger.Debug("Worker stopped", "worker_id", workerID)

	for {
		select {
		case job, ok := <-jobPool:
			if !ok {
				// Channel closed, worker should exit
				return
			}
			qm.processJob(ctx, workerID, job)

		case <-ctx.Done():
			return
		}
	}
}

// processJob handles a single job execution
func (qm *QueueManager) processJob(ctx context.Context, workerID int, job *Job) {
	logger := qm.logger.With(
		"worker_id", workerID,
		"job_id", job.ID,
		"entrypoint", job.Entrypoint)

	logger.Debug("Processing job")

	// Get the entrypoint function
	qm.registryMu.RLock()
	fn, exists := qm.registry[job.Entrypoint]
	qm.registryMu.RUnlock()

	if !exists {
		logger.Error("Entrypoint not found", "entrypoint", job.Entrypoint)
		qm.logJobResult(ctx, job, "exception")
		return
	}

	// Execute the job with timeout and error handling
	jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute) // Default job timeout
	defer cancel()

	// Update job status to 'picked'
	if err := qm.updateJobStatus(ctx, job.ID, JobStatusPicked); err != nil {
		logger.Error("Failed to update job status to picked", "error", err)
		return
	}
	job.Status = JobStatusPicked

	var jobErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("Job panicked", "panic", r)
				jobErr = fmt.Errorf("job panicked: %v", r)
			}
		}()

		jobErr = fn(jobCtx, job)
	}()

	// Log job result and clean up
	if jobErr != nil {
		logger.Error("Job execution failed", "error", jobErr)
		qm.logJobResult(ctx, job, "exception")
	} else {
		logger.Debug("Job executed successfully")
		qm.logJobResult(ctx, job, "successful")
	}

	// Delete the completed job
	if err := qm.db.Queries().DeleteJob(ctx, job.ID); err != nil {
		logger.Error("Failed to delete completed job", "error", err)
	}
}

// updateJobStatus updates the status of a job
func (qm *QueueManager) updateJobStatus(ctx context.Context, jobID int32, status JobStatus) error {
	return qm.db.Queries().UpdateJobStatus(ctx, generated.UpdateJobStatusParams{
		ID:     jobID,
		Status: generated.QueueStatus(status),
	})
}

// logJobResult logs the execution result for statistics
func (qm *QueueManager) logJobResult(ctx context.Context, job *Job, status string) {
	// Calculate time in queue
	timeInQueue := time.Since(job.Created)

	// Convert time.Duration to pgtype.Interval
	var interval pgtype.Interval
	interval.Microseconds = int64(timeInQueue.Microseconds())
	interval.Valid = true

	// Get current time as pgtype.Timestamptz
	var created pgtype.Timestamptz
	created.Time = time.Now()
	created.Valid = true

	params := generated.InsertJobStatisticsParams{
		Priority:    job.Priority,
		TimeInQueue: interval,
		Entrypoint:  job.Entrypoint,
		Created:     created,
		Status:      generated.StatisticsStatus(status),
		Count:       1, // Single job processed
	}

	if err := qm.db.Queries().InsertJobStatistics(ctx, params); err != nil {
		qm.logger.Error("Failed to log job result", "error", err, "job_id", job.ID)
	}
}

// ClearQueue removes all jobs for specified entrypoints
func (qm *QueueManager) ClearQueue(ctx context.Context, entrypoints []string) error {
	if len(entrypoints) == 0 {
		return fmt.Errorf("no entrypoints specified")
	}

	err := qm.db.Queries().ClearQueue(ctx, entrypoints)
	if err != nil {
		return fmt.Errorf("failed to clear queue: %w", err)
	}

	qm.logger.Info("Cleared queue", "entrypoints", entrypoints)
	return nil
}

// GetJob retrieves a specific job by ID
func (qm *QueueManager) GetJob(ctx context.Context, jobID int32) (*Job, error) {
	pgJob, err := qm.db.Queries().GetJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	return qm.convertPgJob(pgJob)
}

// DeleteJob removes a specific job from the queue
func (qm *QueueManager) DeleteJob(ctx context.Context, jobID int32) error {
	err := qm.db.Queries().DeleteJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}

	qm.logger.Debug("Deleted job", "job_id", jobID)
	return nil
}
