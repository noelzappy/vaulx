-- name: ListFilesPage :many
SELECT * FROM files
WHERE folder_id IS NOT DISTINCT FROM $1
  AND status = 'active'
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountFiles :one
SELECT COUNT(*) FROM files
WHERE folder_id IS NOT DISTINCT FROM $1
  AND status = 'active';

-- name: ListAuditLogPage :many
SELECT
  al.id,
  al.user_id,
  al.action,
  al.resource_type,
  al.resource_id,
  al.meta,
  al.created_at,
  u.name  AS actor_name,
  u.email AS actor_email
FROM audit_log al
LEFT JOIN users u ON u.id = al.user_id
ORDER BY al.created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountAuditLog :one
SELECT COUNT(*) FROM audit_log;

-- name: UpdateFileSizeAndStatus :one
UPDATE files SET size_bytes = $1, status = $2 WHERE id = $3 RETURNING *;
