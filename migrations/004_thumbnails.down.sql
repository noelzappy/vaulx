DROP INDEX IF EXISTS idx_files_thumb_pending;

ALTER TABLE files
  DROP COLUMN IF EXISTS thumb_s3_key,
  DROP COLUMN IF EXISTS thumb_width,
  DROP COLUMN IF EXISTS thumb_height,
  DROP COLUMN IF EXISTS thumb_status,
  DROP COLUMN IF EXISTS thumb_generated_at,
  DROP COLUMN IF EXISTS thumb_error;
