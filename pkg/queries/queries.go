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
