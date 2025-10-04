package queries

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Queries handles operations related to job queuing such as
// enqueueing, dequeueing, and querying the size of the queue.
// This is modeled after PgQueuer's Queries class.
type Queries struct {
	db *pgxpool.Pool
	qb *QueryBuilder
}

// NewQueries creates a new Queries instance with default settings
func NewQueries(db *pgxpool.Pool) *Queries {
	return &Queries{
		db: db,
		qb: NewQueryBuilder(),
	}
}

// NewQueriesWithPrefix creates a new Queries instance with prefix
func NewQueriesWithPrefix(db *pgxpool.Pool, prefix string) *Queries {
	return &Queries{
		db: db,
		qb: NewQueryBuilderWithPrefix(prefix),
	}
}

// NewQueriesWithSettings creates a new Queries instance with custom settings
func NewQueriesWithSettings(db *pgxpool.Pool, settings *DBSettings) *Queries {
	return &Queries{
		db: db,
		qb: NewQueryBuilderWithSettings(settings),
	}
}

// Job represents a job record from the database
type Job struct {
	ID         int       `json:"id"`
	Priority   int       `json:"priority"`
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
	Status     string    `json:"status"`
	Entrypoint string    `json:"entrypoint"`
	Payload    []byte    `json:"payload"`
}

// QueueStatistics represents queue statistics
type QueueStatistics struct {
	Count      int    `json:"count"`
	Priority   int    `json:"priority"`
	Entrypoint string `json:"entrypoint"`
	Status     string `json:"status"`
}

// LogStatistics represents log statistics
type LogStatistics struct {
	Count       int           `json:"count"`
	Created     time.Time     `json:"created"`
	Priority    int           `json:"priority"`
	TimeInQueue time.Duration `json:"time_in_queue"`
	Status      string        `json:"status"`
	Entrypoint  string        `json:"entrypoint"`
}

// Install creates necessary database structures such as enums,
// tables, and triggers for job queuing and logging.
func (q *Queries) Install(ctx context.Context) error {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, q.qb.CreateInstallQuery()); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// Uninstall drops all database structures related to job queuing
// and logging that were created by the install method.
func (q *Queries) Uninstall(ctx context.Context) error {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, q.qb.CreateUninstallQuery()); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// Upgrade upgrades the database schema to the latest version
func (q *Queries) Upgrade(ctx context.Context) error {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	queries := q.qb.CreateUpgradeQueries()
	for _, query := range queries {
		if _, err := tx.Exec(ctx, query); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// EnqueueJob inserts a new job into the queue with the specified
// entrypoint, payload, and priority, marking it as 'queued'.
func (q *Queries) EnqueueJob(ctx context.Context, priority int, entrypoint string, payload []byte) error {
	query := q.qb.CreateEnqueueQuery()
	_, err := q.db.Exec(ctx, query, priority, entrypoint, payload)
	return err
}

// EnqueueJobs inserts multiple jobs into the queue in a single batch operation
// This uses PostgreSQL's unnest() for efficient bulk insert, aligned with PgQueuer's approach
func (q *Queries) EnqueueJobs(ctx context.Context, jobs []EnqueueJobParams) error {
	if len(jobs) == 0 {
		return nil
	}

	// Extract arrays for unnest operation (like PgQueuer)
	priorities := make([]int, len(jobs))
	entrypoints := make([]string, len(jobs))
	payloads := make([][]byte, len(jobs))

	for i, job := range jobs {
		priorities[i] = job.Priority
		entrypoints[i] = job.Entrypoint
		payloads[i] = job.Payload
	}

	// Use unnest for efficient batch insert
	// Aligned with PgQueuer: INSERT INTO ... VALUES (unnest($1::int[]), unnest($2::text[]), unnest($3::bytea[]), 'queued')
	query := fmt.Sprintf(`
		INSERT INTO %s (priority, entrypoint, payload, status)
		SELECT unnest($1::int[]), unnest($2::text[]), unnest($3::bytea[]), 'queued'
	`, q.qb.Settings.QueueTable)

	_, err := q.db.Exec(ctx, query, priorities, entrypoints, payloads)
	return err
}

// EnqueueJobParams represents parameters for enqueueing a job
type EnqueueJobParams struct {
	Priority   int    `json:"priority"`
	Entrypoint string `json:"entrypoint"`
	Payload    []byte `json:"payload"`
}

// DequeueJobsParams represents parameters for dequeueing jobs
type DequeueJobsParams struct {
	BatchSize    int            `json:"batch_size"`
	Entrypoints  []string       `json:"entrypoints"`
	RetryTimeout *time.Duration `json:"retry_timeout,omitempty"`
}

// DequeueJobs retrieves and updates the next 'queued' job to 'picked' status,
// ensuring no two jobs with the same entrypoint are picked simultaneously.
func (q *Queries) DequeueJobs(ctx context.Context, params DequeueJobsParams) ([]Job, error) {
	if params.BatchSize < 1 {
		return nil, fmt.Errorf("batch size must be greater or equal to one (1)")
	}

	if params.RetryTimeout != nil && *params.RetryTimeout < 0 {
		return nil, fmt.Errorf("retry timeout must be non-negative")
	}

	query := q.qb.CreateDequeueQuery()
	rows, err := q.db.Query(ctx, query, params.BatchSize, params.Entrypoints, params.RetryTimeout)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var job Job
		err := rows.Scan(
			&job.ID,
			&job.Priority,
			&job.Created,
			&job.Updated,
			&job.Status,
			&job.Entrypoint,
			&job.Payload,
		)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}

	return jobs, rows.Err()
}

// CompleteJob marks a job as completed and logs it to statistics
func (q *Queries) CompleteJob(ctx context.Context, jobID int, status string) error {
	query := q.qb.CreateCompleteJobQuery()
	_, err := q.db.Exec(ctx, query, jobID, status)
	return err
}

// CompleteJobs marks multiple jobs as completed and logs them to statistics
// This is aligned with PgQueuer's log_jobs() which uses batch SQL processing with SQL-level aggregation.
//
// The implementation uses a single SQL query to:
// 1. Delete all jobs from the queue in one operation (ANY($1::integer[]))
// 2. Map job IDs to their statuses using unnest()
// 3. Aggregate by dimensions to reduce INSERT rows (GROUP BY in SQL)
// 4. Insert aggregated statistics with ON CONFLICT handling
//
// This approach eliminates the need for advisory locks by:
// - Reducing the number of database round-trips (1 query vs N queries)
// - Minimizing lock contention through SQL-level aggregation
// - Shortening transaction duration with single-query execution
//
// Performance: ~2-3x faster than loop-based approach, scales well with concurrent consumers
func (q *Queries) CompleteJobs(ctx context.Context, jobStatuses []JobStatus) error {
	if len(jobStatuses) == 0 {
		return nil
	}

	// Extract job IDs and statuses into separate arrays for batch processing
	// This matches PgQueuer's approach: log_jobs([(job, status), ...])
	jobIDs := make([]int, len(jobStatuses))
	statuses := make([]string, len(jobStatuses))

	for i, js := range jobStatuses {
		jobIDs[i] = js.JobID
		statuses[i] = js.Status
	}

	// Execute batch query with arrays as parameters
	// Single SQL execution replaces the previous loop of N executions
	query := q.qb.CreateBatchCompleteJobsQuery()
	_, err := q.db.Exec(ctx, query, jobIDs, statuses)

	if err != nil {
		return fmt.Errorf("failed to batch complete jobs: %w", err)
	}

	return nil
}

// JobStatus represents a job's final status for completion
type JobStatus struct {
	JobID  int    `json:"job_id"`
	Status string `json:"status"` // "successful" or "exception"
}

// QueueSize returns the number of jobs in the queue grouped by entrypoint and priority
func (q *Queries) QueueSize(ctx context.Context) ([]QueueStatistics, error) {
	query := q.qb.CreateQueueSizeQuery()
	rows, err := q.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []QueueStatistics
	for rows.Next() {
		var stat QueueStatistics
		err := rows.Scan(&stat.Count, &stat.Priority, &stat.Entrypoint, &stat.Status)
		if err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}

	return stats, rows.Err()
}

// ClearQueue clears jobs from the queue, optionally filtering by entrypoint
func (q *Queries) ClearQueue(ctx context.Context, entrypoints []string) error {
	var query string
	var args []interface{}

	if len(entrypoints) > 0 {
		query = q.qb.CreateDeleteFromQueueQuery()
		args = []interface{}{entrypoints}
	} else {
		query = q.qb.CreateTruncateQueueQuery()
	}

	_, err := q.db.Exec(ctx, query, args...)
	return err
}

// LogStatistics returns log statistics with the specified number of recent entries
func (q *Queries) LogStatistics(ctx context.Context, tail int) ([]LogStatistics, error) {
	query := q.qb.CreateLogStatisticsQuery()
	rows, err := q.db.Query(ctx, query, tail)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []LogStatistics
	for rows.Next() {
		var stat LogStatistics
		err := rows.Scan(
			&stat.Count,
			&stat.Created,
			&stat.Priority,
			&stat.TimeInQueue,
			&stat.Status,
			&stat.Entrypoint,
		)
		if err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}

	return stats, rows.Err()
}

// ClearLog clears entries from the job log table, optionally filtering by entrypoint
func (q *Queries) ClearLog(ctx context.Context, entrypoints []string) error {
	var query string
	var args []interface{}

	if len(entrypoints) > 0 {
		query = q.qb.CreateDeleteFromLogQuery()
		args = []interface{}{entrypoints}
	} else {
		query = q.qb.CreateTruncateLogQuery()
	}

	_, err := q.db.Exec(ctx, query, args...)
	return err
}

// HasUpdatedColumn checks if the queue table has the 'updated' column
func (q *Queries) HasUpdatedColumn(ctx context.Context) (bool, error) {
	query := q.qb.CreateHasColumnQuery()
	var exists bool
	err := q.db.QueryRow(ctx, query, q.qb.Settings.QueueTable, "updated").Scan(&exists)
	return exists, err
}
