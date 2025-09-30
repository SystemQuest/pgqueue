-- name: EnqueueJob :exec
INSERT INTO pgqueue_jobs (priority, entrypoint, payload, status)
VALUES ($1, $2, $3, 'queued');

-- name: EnqueueJobBatch :batchexec
INSERT INTO pgqueue_jobs (priority, entrypoint, payload, status)
VALUES ($1, $2, $3, 'queued');

-- name: DequeueJobsByEntrypoint :many
SELECT id, priority, created, updated, status, entrypoint, payload
FROM pgqueue_jobs
WHERE entrypoint = ANY($1::text[])
  AND status = 'queued'
ORDER BY priority DESC, id ASC
FOR UPDATE SKIP LOCKED
LIMIT $2;

-- name: DequeueJobsAtomic :many
WITH selected_jobs AS (
    SELECT id, priority, created, updated, status, entrypoint, payload
    FROM pgqueue_jobs
    WHERE entrypoint = ANY($1::text[])
      AND status = 'queued'
    ORDER BY priority DESC, id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT $2
),
updated_jobs AS (
    UPDATE pgqueue_jobs
    SET status = 'picked', updated = NOW()
    WHERE id IN (SELECT id FROM selected_jobs)
    RETURNING id, priority, created, updated, status, entrypoint, payload
)
SELECT * FROM updated_jobs
ORDER BY priority DESC, id ASC;

-- name: GetRetryJobs :many
SELECT id, priority, created, updated, status, entrypoint, payload
FROM pgqueue_jobs
WHERE entrypoint = ANY($1::text[])
  AND status = 'picked'
  AND updated < NOW() - $2::interval
ORDER BY updated ASC, id ASC
FOR UPDATE SKIP LOCKED
LIMIT $3;

-- name: DequeueRetryJobsAtomic :many
WITH selected_jobs AS (
    SELECT id, priority, created, updated, status, entrypoint, payload
    FROM pgqueue_jobs
    WHERE entrypoint = ANY($1::text[])
      AND status = 'picked'
      AND updated < NOW() - $2::interval
    ORDER BY updated ASC, id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT $3
),
updated_jobs AS (
    UPDATE pgqueue_jobs
    SET status = 'picked', updated = NOW()
    WHERE id IN (SELECT id FROM selected_jobs)
    RETURNING id, priority, created, updated, status, entrypoint, payload
)
SELECT * FROM updated_jobs
ORDER BY updated ASC, id ASC;

-- name: MarkJobsAsPicked :exec
UPDATE pgqueue_jobs
SET status = 'picked', updated = NOW()
WHERE id = ANY($1::int[]);

-- name: GetJob :one
SELECT * FROM pgqueue_jobs WHERE id = $1;

-- name: UpdateJobStatus :exec
UPDATE pgqueue_jobs 
SET status = $2, updated = NOW()
WHERE id = $1;

-- name: DeleteJob :exec
DELETE FROM pgqueue_jobs WHERE id = $1;

-- name: DeleteJobs :exec
DELETE FROM pgqueue_jobs WHERE id = ANY($1::int[]);

-- name: GetQueueSize :many
SELECT
    count(*) AS count,
    priority,
    entrypoint,
    status
FROM pgqueue_jobs
GROUP BY entrypoint, priority, status
ORDER BY count DESC, entrypoint, priority, status;

-- name: ClearQueue :exec
DELETE FROM pgqueue_jobs 
WHERE (
    CASE 
        WHEN array_length($1::text[], 1) IS NULL OR array_length($1::text[], 1) = 0 
        THEN TRUE 
        ELSE entrypoint = ANY($1::text[])
    END
);

-- name: TruncateQueue :exec
TRUNCATE pgqueue_jobs;