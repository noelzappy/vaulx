CREATE TABLE zip_jobs (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  folder_id     UUID NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  share_id      UUID REFERENCES shares(id) ON DELETE SET NULL,
  status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'ready', 'failed')),
  s3_key        TEXT,
  size_bytes    BIGINT NOT NULL DEFAULT 0,
  file_count    INT NOT NULL DEFAULT 0,
  content_bytes BIGINT NOT NULL DEFAULT 0,
  error         TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at    TIMESTAMPTZ
);
CREATE INDEX idx_zip_jobs_folder_id ON zip_jobs(folder_id);
CREATE INDEX idx_zip_jobs_status ON zip_jobs(status);
