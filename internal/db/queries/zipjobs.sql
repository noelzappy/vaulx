-- name: CreateZipJob :one
INSERT INTO zip_jobs (folder_id, share_id, file_count, content_bytes)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetZipJob :one
SELECT * FROM zip_jobs WHERE id = $1;

-- name: GetReusableZipJob :one
SELECT * FROM zip_jobs
WHERE folder_id = $1
  AND (
    status IN ('pending', 'running')
    OR (
      status = 'ready'
      AND expires_at > NOW()
      AND file_count = $2
      AND content_bytes = $3
      AND created_at > $4
    )
  )
ORDER BY created_at DESC
LIMIT 1;

-- name: ClaimNextPendingZipJob :one
UPDATE zip_jobs SET status = 'running'
WHERE id = (
  SELECT id FROM zip_jobs
  WHERE status = 'pending'
  ORDER BY created_at
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: MarkZipJobReady :exec
UPDATE zip_jobs
SET status = 'ready', s3_key = $2, size_bytes = $3, expires_at = NOW() + INTERVAL '24 hours'
WHERE id = $1;

-- name: MarkZipJobFailed :exec
UPDATE zip_jobs SET status = 'failed', error = $2 WHERE id = $1;

-- name: FailStaleRunningZipJobs :exec
UPDATE zip_jobs SET status = 'failed', error = 'interrupted by server restart'
WHERE status = 'running';

-- name: ListExpiredReadyZipJobs :many
SELECT * FROM zip_jobs WHERE status = 'ready' AND expires_at < NOW();

-- name: MarkZipJobExpired :exec
UPDATE zip_jobs SET status = 'failed', error = 'expired' WHERE id = $1;
