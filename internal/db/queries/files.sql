-- name: ListRootFiles :many
SELECT * FROM files
WHERE folder_id IS NULL AND status = 'active'
ORDER BY name ASC;

-- name: ListRootFilesForUser :many
SELECT DISTINCT f.* FROM files f
LEFT JOIN permissions p
  ON p.resource_type = 'file' AND p.resource_id = f.id AND p.user_id = $1
WHERE f.folder_id IS NULL
  AND f.status = 'active'
  AND (f.uploaded_by = $1 OR p.id IS NOT NULL)
ORDER BY f.name ASC;

-- name: ListFilesByFolder :many
SELECT * FROM files
WHERE folder_id = $1 AND status = 'active'
ORDER BY name ASC;

-- name: ListFilesByFolderForUser :many
SELECT DISTINCT f.* FROM files f
LEFT JOIN permissions p
  ON p.resource_type = 'file' AND p.resource_id = f.id AND p.user_id = $2
WHERE f.folder_id = $1
  AND f.status = 'active'
  AND (f.uploaded_by = $2 OR p.id IS NOT NULL)
ORDER BY f.name ASC;

-- name: GetFile :one
SELECT * FROM files WHERE id = $1;
