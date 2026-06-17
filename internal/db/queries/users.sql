-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1 AND active = true
LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (email, name, role, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC;

-- name: UpdateUserRole :one
UPDATE users SET role = $1 WHERE id = $2 RETURNING *;

-- name: DeactivateUser :one
UPDATE users SET active = $1 WHERE id = $2 RETURNING *;

-- name: UpdateUserName :one
UPDATE users SET name = $1 WHERE id = $2 RETURNING *;

-- name: UpdateUserPassword :one
UPDATE users SET password_hash = $1 WHERE id = $2 RETURNING *;
