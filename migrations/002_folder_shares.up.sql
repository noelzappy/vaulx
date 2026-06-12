ALTER TABLE shares ALTER COLUMN file_id DROP NOT NULL;

ALTER TABLE shares ADD COLUMN folder_id UUID REFERENCES folders(id) ON DELETE CASCADE;

ALTER TABLE shares ADD CONSTRAINT shares_one_target CHECK (
  (file_id IS NOT NULL AND folder_id IS NULL) OR
  (file_id IS NULL AND folder_id IS NOT NULL)
);

CREATE INDEX idx_shares_folder_id ON shares(folder_id) WHERE folder_id IS NOT NULL;
