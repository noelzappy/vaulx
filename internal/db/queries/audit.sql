-- name: CreateAuditLog :one
INSERT INTO audit_log (user_id, action, resource_type, resource_id, meta)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListRecentAuditLog :many
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
LIMIT $1;

-- name: ListAuditLogByAction :many
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
WHERE al.action = $1
ORDER BY al.created_at DESC
LIMIT $2;
