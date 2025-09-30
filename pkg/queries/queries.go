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
	}
}

// QueryBuilder generates SQL queries for job queuing operations
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

// CreateInstallQuery reads and returns the schema installation SQL from migration files
// This ensures single source of truth for database schema
func (qb *QueryBuilder) CreateInstallQuery() (string, error) {
	// TODO: Read from sql/migrations/001_initial_schema.up.sql
	// For now, return empty string to indicate schema should be installed via migrations
	return "", fmt.Errorf("schema installation should use migration files in sql/migrations/")
}

// CreateUninstallQuery generates the SQL query to drop all database objects
func (qb *QueryBuilder) CreateUninstallQuery() string {
	return fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s;
DROP FUNCTION IF EXISTS %s();
DROP TABLE IF EXISTS %s CASCADE;
DROP TABLE IF EXISTS %s CASCADE;
DROP TYPE IF EXISTS %s CASCADE;
DROP TYPE IF EXISTS %s CASCADE;`,
		qb.Settings.Trigger, qb.Settings.QueueTable,
		qb.Settings.Function,
		qb.Settings.StatisticsTable,
		qb.Settings.QueueTable,
		qb.Settings.StatisticsTableStatusType,
		qb.Settings.QueueStatusType,
	)
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
