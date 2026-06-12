-- name: CreateShare :one
INSERT INTO shares (file_id, slug, share_type, expires_at, created_by)
VALUES ($1, $2, 'public', $3, $4)
RETURNING *;

-- name: CreateFolderShare :one
INSERT INTO shares (folder_id, slug, share_type, expires_at, created_by)
VALUES ($1, $2, 'public', $3, $4)
RETURNING *;

-- name: GetShareBySlug :one
SELECT * FROM shares WHERE slug = $1;

-- name: IncrementShareViewCount :exec
UPDATE shares SET view_count = view_count + 1 WHERE id = $1;

-- name: GetActiveShareByFileID :one
SELECT * FROM shares
WHERE file_id = $1
  AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY created_at DESC
LIMIT 1;

-- name: GetActiveShareByFolderID :one
SELECT * FROM shares
WHERE folder_id = $1
  AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY created_at DESC
LIMIT 1;

-- name: ListAllShares :many
SELECT s.*,
  COALESCE(f.name, fo.name) AS target_name,
  CASE WHEN s.file_id IS NOT NULL THEN 'file' ELSE 'folder' END AS target_type,
  COALESCE(f.status, 'active') AS target_status,
  u.name AS creator_name
FROM shares s
LEFT JOIN files f ON f.id = s.file_id
LEFT JOIN folders fo ON fo.id = s.folder_id
LEFT JOIN users u ON u.id = s.created_by
ORDER BY s.created_at DESC;

-- name: ListSharesByCreator :many
SELECT s.*,
  COALESCE(f.name, fo.name) AS target_name,
  CASE WHEN s.file_id IS NOT NULL THEN 'file' ELSE 'folder' END AS target_type,
  COALESCE(f.status, 'active') AS target_status,
  u.name AS creator_name
FROM shares s
LEFT JOIN files f ON f.id = s.file_id
LEFT JOIN folders fo ON fo.id = s.folder_id
LEFT JOIN users u ON u.id = s.created_by
WHERE s.created_by = $1
ORDER BY s.created_at DESC;

-- name: GetShare :one
SELECT * FROM shares WHERE id = $1;

-- name: RevokeShare :exec
DELETE FROM shares WHERE id = $1;
