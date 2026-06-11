-- name: GrantPermission :one
INSERT INTO permissions (user_id, resource_type, resource_id, level, granted_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, resource_type, resource_id) DO UPDATE
  SET level      = EXCLUDED.level,
      granted_by = EXCLUDED.granted_by
RETURNING *;

-- name: RevokePermission :exec
DELETE FROM permissions WHERE id = $1;

-- name: ListPermissionsForResource :many
SELECT
  p.id,
  p.user_id,
  p.resource_type,
  p.resource_id,
  p.level,
  p.created_at,
  u.name  AS grantee_name,
  u.email AS grantee_email
FROM permissions p
JOIN users u ON u.id = p.user_id
WHERE p.resource_type = $1 AND p.resource_id = $2
ORDER BY u.name ASC;

-- name: CheckPermission :one
SELECT EXISTS(
  SELECT 1 FROM permissions
  WHERE user_id       = $1
    AND resource_type = $2
    AND resource_id   = $3
);
