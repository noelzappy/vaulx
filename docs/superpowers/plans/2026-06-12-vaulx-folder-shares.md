# Folder Shares Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Share a whole folder (and its nested contents) via a public link, with revoke/expiry identical to file shares.

**Architecture:** Extend the existing `shares` table to point at either a file or a folder (CHECK: exactly one). Public resolver branches on share type: file shares keep the presign-redirect flow; folder shares render a standalone read-only browser page. Subfolder navigation and file downloads are validated against the shared root with a recursive-CTE ancestor check so a slug can never escape its tree.

**Tech Stack:** golang-migrate (embedded), sqlc, Chi, templ.

---

### Task 1: Migration 002 + SQL queries

**Files:**
- Create: `migrations/002_folder_shares.up.sql`, `migrations/002_folder_shares.down.sql`
- Modify: `internal/db/queries/shares.sql`

- [ ] **Step 1: up migration**

```sql
ALTER TABLE shares ALTER COLUMN file_id DROP NOT NULL;
ALTER TABLE shares ADD COLUMN folder_id UUID REFERENCES folders(id) ON DELETE CASCADE;
ALTER TABLE shares ADD CONSTRAINT shares_one_target CHECK (
  (file_id IS NOT NULL AND folder_id IS NULL) OR
  (file_id IS NULL AND folder_id IS NOT NULL)
);
CREATE INDEX idx_shares_folder_id ON shares(folder_id) WHERE folder_id IS NOT NULL;
```

- [ ] **Step 2: down migration** (delete folder shares, drop constraint/column, restore NOT NULL)

- [ ] **Step 3: queries**

```sql
-- name: CreateFolderShare :one
INSERT INTO shares (folder_id, slug, share_type, expires_at, created_by)
VALUES ($1, $2, 'public', $3, $4)
RETURNING *;

-- name: GetActiveShareByFolderID :one
SELECT * FROM shares
WHERE folder_id = $1 AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY created_at DESC LIMIT 1;

-- name: IsFolderInTree :one
WITH RECURSIVE chain AS (
  SELECT id, parent_id FROM folders WHERE id = $1
  UNION ALL
  SELECT f.id, f.parent_id FROM folders f JOIN chain c ON f.id = c.parent_id
)
SELECT EXISTS(SELECT 1 FROM chain WHERE id = $2) AS in_tree;
```

($1 = candidate folder, $2 = shared root; true when root is ancestor-or-self.)

Update `ListAllShares` / `ListSharesByCreator` to LEFT JOIN both targets:

```sql
SELECT s.*,
  COALESCE(f.name, fo.name) AS target_name,
  CASE WHEN s.file_id IS NOT NULL THEN 'file' ELSE 'folder' END AS target_type,
  COALESCE(f.status, 'active') AS target_status,
  u.name AS creator_name
FROM shares s
LEFT JOIN files f ON f.id = s.file_id
LEFT JOIN folders fo ON fo.id = s.folder_id
LEFT JOIN users u ON u.id = s.created_by
ORDER BY s.created_at DESC;
```

- [ ] **Step 4: `sqlc generate`, `go build ./...` (expect handler breakage on renamed row fields — fix in Task 4), commit**

---

### Task 2: Create-folder-share endpoint + folder menu button

**Files:**
- Modify: `internal/handler/share.go` (CreateFolderShare)
- Modify: `web/templates/folder_card.templ` (Copy link menu item)
- Modify: `web/templates/layout.templ` (copyFolderShareLink JS)
- Modify: `cmd/server/main.go` (route)

- [ ] **Step 1: handler** — mirror of CreateShare: CanEdit gate, `GetFolder` 404 check, idempotent via `GetActiveShareByFolderID`, random 32-byte hex slug, 7-day expiry, audit `share.create`, return `{"url": "/s/" + slug}`.

- [ ] **Step 2: route** `r.Post("/files/folders/{folderID}/share", shareHandler.CreateFolderShare)`

- [ ] **Step 3: layout JS** — `copyFolderShareLink(folderID)` mirroring `copyShareLink` against the new endpoint.

- [ ] **Step 4: folder context menu** — add "Copy link" item above Rename calling `copyFolderShareLink(this.dataset.folderId)` (read ID from card dataset, no Go vars in JS).

- [ ] **Step 5: generate, build, commit**

---

### Task 3: Public folder browser + scoped downloads

**Files:**
- Modify: `internal/handler/share.go` (ResolveShare branch, SharedFileDownload)
- Create: `web/templates/shared_folder.templ`
- Modify: `cmd/server/main.go` (route)

- [ ] **Step 1: ResolveShare** — after expiry/max-views checks: if `share.FileID.Valid` keep existing flow. Else:
  - root = `share.FolderID`; target = `?folder=` param if present, validated with `IsFolderInTree(target, root)` → 404 on false.
  - `GetFolder(target)`, `ListFoldersByParent(target)`, `ListFilesByFolder(target)`.
  - Increment view count; render `SharedFolderPage(slug, rootName, folder, folders, files)`.

- [ ] **Step 2: SharedFileDownload** — GET `/s/{slug}/file/{fileID}`: validate share (slug, expiry, is folder share); `GetFile` must be active; file's `folder_id` must be valid and `IsFolderInTree(file.FolderID, root)`; presign + redirect.

- [ ] **Step 3: `shared_folder.templ`** — standalone page (own `<html>`, app.css, no Layout): header with folder name + "Shared via Vaulx", grid of subfolder cards linking `/s/{slug}?folder={id}`, file rows with size + Download button, back link when inside a subfolder.

- [ ] **Step 4: route** `r.Get("/s/{slug}/file/{fileID}", shareHandler.SharedFileDownload)` next to the existing public share route.

- [ ] **Step 5: generate, build, commit**

---

### Task 4: Shares page shows both kinds

**Files:**
- Modify: `internal/handler/share.go` (SharesPage mapping — renamed row fields)
- Modify: `internal/viewmodel/models.go` (ShareView.TargetType)
- Modify: `web/templates/shares_page.templ` (type badge, copy works for both)

- [ ] **Step 1:** `ShareView` gains `TargetType string`; `shareViewFrom` takes targetName/targetType/targetStatus.
- [ ] **Step 2:** template: badge `File`/`Folder` next to name; "File deleted" badge only applies to file shares.
- [ ] **Step 3: generate, build, commit**

---

### Task 5: Tests + verification

- [ ] `TestCreateFolderShare_ViewerForbidden` (403), `TestSharedFileDownload_BadSlug` (404 via nil queries path)
- [ ] `go build ./... && go test ./...`
- [ ] `docker compose up -d --build` — verify migration 002 applies cleanly on the existing DB
- [ ] Browser smoke test: share folder → open slug in incognito → navigate subfolder → download file → revoke → 404
- [ ] Final push
