-- name: CreateShare :one
INSERT INTO shares (file_id, slug, share_type, expires_at, created_by)
VALUES ($1, $2, 'public', $3, $4)
RETURNING *;

-- name: GetShareBySlug :one
SELECT * FROM shares WHERE slug = $1;

-- name: IncrementShareViewCount :exec
UPDATE shares SET view_count = view_count + 1 WHERE id = $1;
