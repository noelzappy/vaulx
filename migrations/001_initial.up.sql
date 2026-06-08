CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email         TEXT UNIQUE NOT NULL,
  name          TEXT NOT NULL,
  role          TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
  password_hash TEXT NOT NULL,
  active        BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE folders (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  parent_id  UUID REFERENCES folders(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  owner_id   UUID NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE files (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  folder_id   UUID REFERENCES folders(id) ON DELETE SET NULL,
  name        TEXT NOT NULL,
  s3_key      TEXT NOT NULL UNIQUE,
  size_bytes  BIGINT NOT NULL DEFAULT 0,
  mime_type   TEXT,
  uploaded_by UUID NOT NULL REFERENCES users(id),
  status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'deleted')),
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE permissions (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('file', 'folder')),
  resource_id   UUID NOT NULL,
  level         TEXT NOT NULL CHECK (level IN ('view', 'edit', 'manage')),
  granted_by    UUID REFERENCES users(id),
  created_at    TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE (user_id, resource_type, resource_id)
);

CREATE TABLE shares (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  file_id       UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  slug          TEXT NOT NULL UNIQUE,
  share_type    TEXT NOT NULL CHECK (share_type IN ('public', 'private')),
  password_hash TEXT,
  expires_at    TIMESTAMPTZ,
  max_views     INT,
  view_count    INT NOT NULL DEFAULT 0,
  created_by    UUID NOT NULL REFERENCES users(id),
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE audit_log (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID REFERENCES users(id),
  action        TEXT NOT NULL,
  resource_type TEXT,
  resource_id   UUID,
  meta          JSONB,
  created_at    TIMESTAMPTZ DEFAULT NOW()
);
