-- Drop trigger
DROP TRIGGER IF EXISTS pgqueue_jobs_notify_trigger ON pgqueue_jobs;

-- Drop function
DROP FUNCTION IF EXISTS pgqueue_notify();

-- Drop tables
DROP TABLE IF EXISTS pgqueue_statistics;
DROP TABLE IF EXISTS pgqueue_jobs;
DROP TABLE IF EXISTS pgqueue_schema_version;

-- Drop types
DROP TYPE IF EXISTS statistics_status;
DROP TYPE IF EXISTS queue_status;