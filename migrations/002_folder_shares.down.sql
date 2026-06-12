DELETE FROM shares WHERE folder_id IS NOT NULL;

DROP INDEX IF EXISTS idx_shares_folder_id;

ALTER TABLE shares DROP CONSTRAINT IF EXISTS shares_one_target;

ALTER TABLE shares DROP COLUMN IF EXISTS folder_id;

ALTER TABLE shares ALTER COLUMN file_id SET NOT NULL;
