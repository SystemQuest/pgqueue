package queries

import (
	"fmt"
	"strings"
)

// DBSettings holds database configuration settings
type DBSettings struct {
	QueueTable                string
	StatisticsTable           string
	QueueStatusType           string
	StatisticsTableStatusType string
	Function                  string
	Trigger                   string
	Channel                   string
	SchemaVersionTable        string
}

// NewDBSettings creates default database settings
func NewDBSettings() *DBSettings {
	return &DBSettings{
		QueueTable:                "pgqueue_jobs",
		StatisticsTable:           "pgqueue_statistics",
		QueueStatusType:           "queue_status",
		StatisticsTableStatusType: "statistics_status",
		Function:                  "pgqueue_notify",
		Trigger:                   "pgqueue_jobs_notify_trigger",
		Channel:                   "pgqueue_events",
		SchemaVersionTable:        "pgqueue_schema_version",
	}
}

// NewDBSettingsWithPrefix creates database settings with a prefix
func NewDBSettingsWithPrefix(prefix string) *DBSettings {
	if prefix == "" {
		return NewDBSettings()
	}

	return &DBSettings{
		QueueTable:                prefix + "pgqueue_jobs",
		StatisticsTable:           prefix + "pgqueue_statistics",
		QueueStatusType:           prefix + "queue_status",
		StatisticsTableStatusType: prefix + "statistics_status",
		Function:                  prefix + "pgqueue_notify",
		Trigger:                   prefix + "pgqueue_jobs_notify_trigger",
		Channel:                   prefix + "pgqueue_events",
		SchemaVersionTable:        prefix + "pgqueue_schema_version",
	}
} // QueryBuilder generates SQL queries for job queuing operations
type QueryBuilder struct {
	Settings *DBSettings
}

// NewQueryBuilder creates a new query builder with default settings
func NewQueryBuilder() *QueryBuilder {
	return &QueryBuilder{
		Settings: NewDBSettings(),
	}
}

// NewQueryBuilderWithSettings creates a new query builder with custom settings
func NewQueryBuilderWithSettings(settings *DBSettings) *QueryBuilder {
	return &QueryBuilder{
		Settings: settings,
	}
}

// NewQueryBuilderWithPrefix creates a new query builder with prefix
func NewQueryBuilderWithPrefix(prefix string) *QueryBuilder {
	return &QueryBuilder{
		Settings: NewDBSettingsWithPrefix(prefix),
	}
}

// CreateInstallQuery generates the SQL query to create all database objects
func (qb *QueryBuilder) CreateInstallQuery() string {
	return fmt.Sprintf(`
-- Create queue status enum
CREATE TYPE %s AS ENUM ('queued', 'picked');

-- Create statistics status enum  
CREATE TYPE %s AS ENUM ('exception', 'successful');

-- Create main queue table
CREATE TABLE %s (
    id SERIAL PRIMARY KEY,
    priority INT NOT NULL,
    created TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    updated TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    status %s NOT NULL DEFAULT 'queued',
    entrypoint TEXT NOT NULL,
    payload BYTEA
);

-- Create indexes for performance
CREATE INDEX %s_priority_id_idx ON %s (priority DESC, id ASC)
    WHERE status = 'queued';
    
CREATE INDEX %s_updated_id_idx ON %s (updated ASC, id DESC)
    WHERE status = 'picked';

-- Create statistics table
CREATE TABLE %s (
    id SERIAL PRIMARY KEY,
    created TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT DATE_TRUNC('sec', NOW() at time zone 'UTC'),
    count BIGINT NOT NULL,
    priority INT NOT NULL,
    time_in_queue INTERVAL NOT NULL,
    status %s NOT NULL,
    entrypoint TEXT NOT NULL
);

-- Create unique index for statistics aggregation
CREATE UNIQUE INDEX %s_unique_idx ON %s (
    priority,
    DATE_TRUNC('sec', created at time zone 'UTC'),
    DATE_TRUNC('sec', time_in_queue),
    status,
    entrypoint
);

-- Create notification function
CREATE OR REPLACE FUNCTION %s() RETURNS TRIGGER AS $$
DECLARE
    to_emit BOOLEAN := false;
BEGIN
    -- Check operation type and set the emit flag accordingly
    IF TG_OP = 'UPDATE' AND OLD IS DISTINCT FROM NEW THEN
        to_emit := true;
    ELSIF TG_OP = 'DELETE' THEN
        to_emit := true;
    ELSIF TG_OP = 'INSERT' THEN
        to_emit := true;
    ELSIF TG_OP = 'TRUNCATE' THEN
        to_emit := true;
    END IF;

    -- Perform notification if the emit flag is set
    IF to_emit THEN
        PERFORM pg_notify(
            '%s',
            json_build_object(
                'channel', '%s',
                'operation', lower(TG_OP),
                'sent_at', NOW(),
                'table', TG_TABLE_NAME
            )::text
        );
    END IF;

    -- Return appropriate value based on the operation
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        RETURN NEW;
    ELSIF TG_OP = 'DELETE' THEN
        RETURN OLD;
    ELSE
        RETURN NULL; -- For TRUNCATE and other non-row-specific contexts
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Create trigger
CREATE TRIGGER %s
    AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON %s
    EXECUTE FUNCTION %s();

-- Create schema version table for upgrade management
CREATE TABLE %s (
    version INTEGER PRIMARY KEY,
    installed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    description TEXT
);

-- Insert initial version
INSERT INTO %s (version, description) 
VALUES (1, 'Initial schema with jobs, statistics, and notification system');
`,
		qb.Settings.QueueStatusType,                    // queue status enum
		qb.Settings.StatisticsTableStatusType,          // statistics status enum
		qb.Settings.QueueTable,                         // queue table
		qb.Settings.QueueStatusType,                    // queue table status type
		qb.Settings.QueueTable, qb.Settings.QueueTable, // priority index
		qb.Settings.QueueTable, qb.Settings.QueueTable, // updated index
		qb.Settings.StatisticsTable,                              // statistics table
		qb.Settings.StatisticsTableStatusType,                    // statistics table status type
		qb.Settings.StatisticsTable, qb.Settings.StatisticsTable, // statistics unique index
		qb.Settings.Function,                     // notification function
		qb.Settings.Channel, qb.Settings.Channel, // notification channels
		qb.Settings.Trigger, qb.Settings.QueueTable, qb.Settings.Function, // trigger
		qb.Settings.SchemaVersionTable, qb.Settings.SchemaVersionTable, // schema version table
	)
}

// CreateUninstallQuery generates the SQL query to drop all database objects
func (qb *QueryBuilder) CreateUninstallQuery() string {
	return fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s;
DROP FUNCTION IF EXISTS %s();
DROP TABLE IF EXISTS %s CASCADE;
DROP TABLE IF EXISTS %s CASCADE;
DROP TABLE IF EXISTS %s CASCADE;
DROP TYPE IF EXISTS %s CASCADE;
DROP TYPE IF EXISTS %s CASCADE;`,
		qb.Settings.Trigger, qb.Settings.QueueTable,
		qb.Settings.Function,
		qb.Settings.StatisticsTable,
		qb.Settings.QueueTable,
		qb.Settings.SchemaVersionTable,
		qb.Settings.StatisticsTableStatusType,
		qb.Settings.QueueStatusType,
	)
}

// CreateUpgradeQueries generates upgrade SQL queries
func (qb *QueryBuilder) CreateUpgradeQueries() []string {
	return []string{
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS updated TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW();", qb.Settings.QueueTable),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s_updated_id_idx ON %s (updated ASC, id DESC) WHERE status = 'picked';", qb.Settings.QueueTable, qb.Settings.QueueTable),
	}
}

// CreateEnqueueQuery generates the SQL query to insert a new job into the queue
func (qb *QueryBuilder) CreateEnqueueQuery() string {
	return fmt.Sprintf(`
		INSERT INTO %s (priority, entrypoint, payload, status)
		VALUES ($1, $2, $3, 'queued')`, qb.Settings.QueueTable)
}

// CreateEnqueueBatchQuery generates the SQL query to insert a job (used in batch operations)
func (qb *QueryBuilder) CreateEnqueueBatchQuery() string {
	return qb.CreateEnqueueQuery()
}

// CreateDequeueQuery generates the SQL query to dequeue jobs
func (qb *QueryBuilder) CreateDequeueQuery() string {
	return fmt.Sprintf(`
		WITH next_job_queued AS (
			SELECT id, priority, created, updated, status, entrypoint, payload
			FROM %s
			WHERE
				entrypoint = ANY($2)
				AND status = 'queued'
			ORDER BY priority DESC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		),
		next_job_retry AS (
			SELECT id, priority, created, updated, status, entrypoint, payload
			FROM %s
			WHERE
				entrypoint = ANY($2)
				AND status = 'picked'
				AND ($3::interval IS NOT NULL AND updated < NOW() - $3::interval)
			ORDER BY updated DESC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		),
		combined_jobs AS (
			SELECT DISTINCT id, priority, created, updated, status, entrypoint, payload
			FROM (
				SELECT id, priority, created, updated, status, entrypoint, payload FROM next_job_queued
				UNION ALL
				SELECT id, priority, created, updated, status, entrypoint, payload FROM next_job_retry WHERE $3::interval IS NOT NULL
			) AS combined
		),
		updated AS (
			UPDATE %s
			SET status = 'picked', updated = NOW()
			WHERE id = ANY(SELECT id FROM combined_jobs)
			RETURNING id, priority, created, updated, status, entrypoint, payload
		)
		SELECT id, priority, created, updated, status, entrypoint, payload FROM updated ORDER BY priority DESC, id ASC`,
		qb.Settings.QueueTable, qb.Settings.QueueTable, qb.Settings.QueueTable)
}

// CreateCompleteJobQuery generates the SQL query to complete a job and log statistics
// Note: This is for single job completion. For batch operations, use CreateBatchCompleteJobsQuery
func (qb *QueryBuilder) CreateCompleteJobQuery() string {
	return fmt.Sprintf(`
		WITH deleted AS (
			DELETE FROM %s
			WHERE id = $1
			RETURNING id, priority, entrypoint,
				DATE_TRUNC('sec', created at time zone 'UTC') AS created,
				DATE_TRUNC('sec', AGE(updated, created)) AS time_in_queue
		)
		INSERT INTO %s (priority, entrypoint, time_in_queue, status, created, count)
		SELECT priority, entrypoint, time_in_queue, $2::%s, created, 1
		FROM deleted
		ON CONFLICT (
			priority,
			entrypoint,
			DATE_TRUNC('sec', created at time zone 'UTC'),
			DATE_TRUNC('sec', time_in_queue),
			status
		)
		DO UPDATE
		SET count = %s.count + EXCLUDED.count`,
		qb.Settings.QueueTable, qb.Settings.StatisticsTable, qb.Settings.StatisticsTableStatusType, qb.Settings.StatisticsTable)
}

// CreateBatchCompleteJobsQuery generates the SQL query to complete multiple jobs and log statistics in a single operation
// This is aligned with PgQueuer's log_jobs() implementation which uses batch processing with SQL-level aggregation
// to minimize database round-trips and reduce lock contention on the statistics table.
//
// The query performs:
// 1. Batch delete from queue table using ANY($1::integer[])
// 2. Map job IDs to their statuses using unnest()
// 3. Aggregate jobs by dimensions (priority, entrypoint, time_in_queue, created, status) using GROUP BY
// 4. Insert aggregated counts into statistics table with ON CONFLICT handling
//
// Example: 10 jobs with same dimensions -> 1 INSERT with count=10 (instead of 10 INSERTs with count=1)
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
		SET count = %s.count + EXCLUDED.count`,
		qb.Settings.QueueTable,
		qb.Settings.StatisticsTableStatusType,
		qb.Settings.StatisticsTable,
		qb.Settings.StatisticsTable,
	)
}

// CreateQueueSizeQuery generates the SQL query to count jobs in the queue
func (qb *QueryBuilder) CreateQueueSizeQuery() string {
	return fmt.Sprintf(`
		SELECT
			count(*) AS count,
			priority,
			entrypoint,
			status
		FROM %s
		GROUP BY entrypoint, priority, status
		ORDER BY count, entrypoint, priority, status`,
		qb.Settings.QueueTable)
}

// CreateDeleteFromQueueQuery generates the SQL query to delete jobs by entrypoint
func (qb *QueryBuilder) CreateDeleteFromQueueQuery() string {
	return fmt.Sprintf("DELETE FROM %s WHERE entrypoint = ANY($1)", qb.Settings.QueueTable)
}

// CreateTruncateQueueQuery generates the SQL query to truncate the queue table
func (qb *QueryBuilder) CreateTruncateQueueQuery() string {
	return fmt.Sprintf("TRUNCATE %s", qb.Settings.QueueTable)
}

// CreateLogStatisticsQuery generates the SQL query to get log statistics
func (qb *QueryBuilder) CreateLogStatisticsQuery() string {
	return fmt.Sprintf(`
		SELECT
			count,
			created,
			priority,
			time_in_queue,
			status,
			entrypoint
		FROM %s
		ORDER BY created DESC
		LIMIT $1`,
		qb.Settings.StatisticsTable)
}

// CreateDeleteFromLogQuery generates the SQL query to delete log entries by entrypoint
func (qb *QueryBuilder) CreateDeleteFromLogQuery() string {
	return fmt.Sprintf("DELETE FROM %s WHERE entrypoint = ANY($1)", qb.Settings.StatisticsTable)
}

// CreateTruncateLogQuery generates the SQL query to truncate the statistics table
func (qb *QueryBuilder) CreateTruncateLogQuery() string {
	return fmt.Sprintf("TRUNCATE %s", qb.Settings.StatisticsTable)
}

// CreateHasColumnQuery generates the SQL query to check if a column exists
func (qb *QueryBuilder) CreateHasColumnQuery() string {
	return `
		SELECT EXISTS (
			SELECT 1 
			FROM information_schema.columns 
			WHERE table_name = $1 
			AND column_name = $2
		)`
}

// FormatQuery formats a multi-line SQL query for better readability
func FormatQuery(query string) string {
	lines := strings.Split(query, "\n")
	var formatted []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			formatted = append(formatted, trimmed)
		}
	}

	return strings.Join(formatted, "\n")
}
