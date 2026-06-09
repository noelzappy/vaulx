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
