-- name: DeleteJobsForStats :many
DELETE FROM pgqueue_jobs
WHERE id = ANY($1::int[])
RETURNING 
    id,
    priority,
    entrypoint,
    DATE_TRUNC('sec', created at time zone 'UTC') AS created,
    DATE_TRUNC('sec', AGE(updated, created)) AS time_in_queue;

-- name: InsertJobStatistics :exec
INSERT INTO pgqueue_statistics (
    priority,
    entrypoint,
    time_in_queue,
    created,
    status,
    count
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (
    priority,
    DATE_TRUNC('sec', created at time zone 'UTC'),
    DATE_TRUNC('sec', time_in_queue),
    status,
    entrypoint
)
DO UPDATE SET count = pgqueue_statistics.count + EXCLUDED.count;

-- name: GetStatistics :many
SELECT
    count,
    created,
    time_in_queue,
    entrypoint,
    priority,
    status
FROM pgqueue_statistics
ORDER BY id DESC
LIMIT $1;

-- name: ClearStatistics :exec
DELETE FROM pgqueue_statistics WHERE entrypoint = ANY($1::text[]);

-- name: TruncateStatistics :exec
TRUNCATE pgqueue_statistics;