-- Create queue status enum
CREATE TYPE queue_status AS ENUM ('queued', 'picked');

-- Create statistics status enum  
CREATE TYPE statistics_status AS ENUM ('exception', 'successful');

-- Create main queue table
CREATE TABLE pgqueue_jobs (
    id SERIAL PRIMARY KEY,
    priority INT NOT NULL,
    created TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    updated TIMESTAMP WITH TIME ZONE DEFAULT NOW() NOT NULL,
    status queue_status NOT NULL DEFAULT 'queued',
    entrypoint TEXT NOT NULL,
    payload BYTEA
);

-- Create indexes for performance
CREATE INDEX pgqueue_jobs_priority_id_idx ON pgqueue_jobs (priority DESC, id ASC)
    WHERE status = 'queued';
    
CREATE INDEX pgqueue_jobs_updated_id_idx ON pgqueue_jobs (updated ASC, id DESC)
    WHERE status = 'picked';

-- Create statistics table
CREATE TABLE pgqueue_statistics (
    id SERIAL PRIMARY KEY,
    created TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT DATE_TRUNC('sec', NOW() at time zone 'UTC'),
    count BIGINT NOT NULL,
    priority INT NOT NULL,
    time_in_queue INTERVAL NOT NULL,
    status statistics_status NOT NULL,
    entrypoint TEXT NOT NULL
);

-- Create unique index for statistics aggregation
CREATE UNIQUE INDEX pgqueue_statistics_unique_idx ON pgqueue_statistics (
    priority,
    DATE_TRUNC('sec', created at time zone 'UTC'),
    DATE_TRUNC('sec', time_in_queue),
    status,
    entrypoint
);

-- Create notification function
CREATE OR REPLACE FUNCTION pgqueue_notify() RETURNS TRIGGER AS $$
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
            'pgqueue_events',
            json_build_object(
                'channel', 'pgqueue_events',
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
CREATE TRIGGER pgqueue_jobs_notify_trigger
    AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON pgqueue_jobs
    EXECUTE FUNCTION pgqueue_notify();

-- Create schema version table for upgrade management
CREATE TABLE pgqueue_schema_version (
    version INTEGER PRIMARY KEY,
    installed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    description TEXT
);

-- Insert initial version
INSERT INTO pgqueue_schema_version (version, description) 
VALUES (1, 'Initial schema with jobs, statistics, and notification system');