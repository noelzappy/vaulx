-- name: ListRootFiles :many
SELECT * FROM files
WHERE folder_id IS NULL AND status = 'active'
ORDER BY name ASC;

-- name: ListRootFilesForUser :many
SELECT DISTINCT f.* FROM files f
LEFT JOIN permissions p
  ON p.resource_type = 'file' AND p.resource_id = f.id AND p.user_id = $1
WHERE f.folder_id IS NULL
  AND f.status = 'active'
  AND (f.uploaded_by = $1 OR p.id IS NOT NULL)
ORDER BY f.name ASC;

-- name: ListFilesByFolder :many
SELECT * FROM files
WHERE folder_id = $1 AND status = 'active'
ORDER BY name ASC;

-- name: ListFilesByFolderForUser :many
SELECT DISTINCT f.* FROM files f
LEFT JOIN permissions p
  ON p.resource_type = 'file' AND p.resource_id = f.id AND p.user_id = $2
WHERE f.folder_id = $1
  AND f.status = 'active'
  AND (f.uploaded_by = $2 OR p.id IS NOT NULL)
ORDER BY f.name ASC;

-- name: GetFile :one
SELECT * FROM files WHERE id = $1;

-- name: CreateFile :one
INSERT INTO files (folder_id, name, s3_key, size_bytes, mime_type, uploaded_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ActivateFile :one
UPDATE files SET status = 'active'
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: SoftDeleteFile :exec
UPDATE files SET status = 'deleted' WHERE id = $1;

-- name: UpdateFileName :one
UPDATE files SET name = $1 WHERE id = $2 AND status = 'active' RETURNING *;

-- name: SearchFiles :many
SELECT * FROM files
WHERE status = 'active' AND name ILIKE '%' || $1 || '%'
ORDER BY name ASC
LIMIT 100;

-- name: ListDeletedFiles :many
SELECT f.*, u.name AS uploader_name
FROM files f
LEFT JOIN users u ON u.id = f.uploaded_by
WHERE f.status = 'deleted'
ORDER BY f.created_at DESC;

-- name: RestoreFile :one
UPDATE files SET status = 'active'
WHERE id = $1 AND status = 'deleted'
RETURNING *;

-- name: HardDeleteFile :exec
DELETE FROM files WHERE id = $1;
