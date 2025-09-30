-- name: HasUpdatedColumn :one
SELECT EXISTS (
    SELECT 1 
    FROM information_schema.columns 
    WHERE table_schema = current_schema()
    AND table_name = $1
    AND column_name = $2
);

-- name: GetSchemaVersion :one
SELECT version 
FROM pgqueue_schema_version 
ORDER BY version DESC 
LIMIT 1;

-- name: InsertSchemaVersion :exec
INSERT INTO pgqueue_schema_version (version, description) 
VALUES ($1, $2);