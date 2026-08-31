-- name: ClaimPendingThumbnail :one
UPDATE files
SET thumb_status = 'pending'
WHERE id = (
  SELECT id FROM files
  WHERE status = 'active'
    AND mime_type IN ('image/jpeg', 'image/png', 'image/gif', 'image/webp')
    AND thumb_status = 'none'
  ORDER BY created_at ASC
  LIMIT 1
)
  AND thumb_status = 'none'
RETURNING *;

-- name: MarkThumbnailReady :exec
UPDATE files
SET thumb_s3_key = $1,
    thumb_width = $2,
    thumb_height = $3,
    thumb_generated_at = NOW(),
    thumb_status = 'ready',
    thumb_error = NULL
WHERE id = $4;

-- name: MarkThumbnailFailed :exec
UPDATE files
SET thumb_status = 'failed',
    thumb_error = $1
WHERE id = $2;

-- name: ResetPendingThumbnails :exec
UPDATE files SET thumb_status = 'none' WHERE thumb_status = 'pending';

-- name: ListFilesMissingThumbnails :many
SELECT * FROM files
WHERE status = 'active'
  AND mime_type IN ('image/jpeg', 'image/png', 'image/gif', 'image/webp')
  AND thumb_status <> 'ready'
ORDER BY created_at ASC
LIMIT $1;
