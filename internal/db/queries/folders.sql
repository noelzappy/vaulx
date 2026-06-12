-- name: ListRootFolders :many
SELECT * FROM folders
WHERE parent_id IS NULL
ORDER BY name ASC;

-- name: ListRootFoldersForUser :many
SELECT DISTINCT f.* FROM folders f
LEFT JOIN permissions p
  ON p.resource_type = 'folder' AND p.resource_id = f.id AND p.user_id = $1
WHERE f.parent_id IS NULL
  AND (f.owner_id = $1 OR p.id IS NOT NULL)
ORDER BY f.name ASC;

-- name: ListFoldersByParent :many
SELECT * FROM folders
WHERE parent_id = $1
ORDER BY name ASC;

-- name: ListFoldersByParentForUser :many
SELECT DISTINCT f.* FROM folders f
LEFT JOIN permissions p
  ON p.resource_type = 'folder' AND p.resource_id = f.id AND p.user_id = $2
WHERE f.parent_id = $1
  AND (f.owner_id = $2 OR p.id IS NOT NULL)
ORDER BY f.name ASC;

-- name: GetFolder :one
SELECT * FROM folders WHERE id = $1;

-- name: CreateFolder :one
INSERT INTO folders (name, parent_id, owner_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteFolder :exec
DELETE FROM folders WHERE id = $1;

-- name: UpdateFolderName :one
UPDATE folders SET name = $1 WHERE id = $2 RETURNING *;

-- name: ListAllFolders :many
SELECT * FROM folders ORDER BY name ASC;

-- name: MoveFileToFolder :one
UPDATE files SET folder_id = $1 WHERE id = $2 AND status = 'active' RETURNING *;

-- name: CountFolderItems :one
SELECT COUNT(*) FROM (
  SELECT id FROM folders WHERE parent_id = $1
  UNION ALL
  SELECT id FROM files WHERE folder_id = $1 AND status = 'active'
) sub;
