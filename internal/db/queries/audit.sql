-- name: CreateAuditLog :one
INSERT INTO audit_log (user_id, action, resource_type, resource_id, meta)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;
