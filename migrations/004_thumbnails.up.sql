ALTER TABLE files
  ADD COLUMN thumb_s3_key TEXT,
  ADD COLUMN thumb_width INT,
  ADD COLUMN thumb_height INT,
  ADD COLUMN thumb_status TEXT NOT NULL DEFAULT 'none'
    CHECK (thumb_status IN ('none', 'pending', 'ready', 'failed')),
  ADD COLUMN thumb_generated_at TIMESTAMPTZ,
  ADD COLUMN thumb_error TEXT;

-- Help the worker claim and the backfill find raster images that still need a thumbnail.
CREATE INDEX idx_files_thumb_pending
  ON files(created_at)
  WHERE status = 'active'
    AND mime_type IN ('image/jpeg', 'image/png', 'image/gif', 'image/webp')
    AND thumb_status = 'none';
