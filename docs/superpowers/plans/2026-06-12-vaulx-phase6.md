# Vaulx Phase 6 Implementation Plan — Search, Trash, Share Management

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add file search, an admin trash page with restore, and a share-management page with revoke — the three walls users hit in week one.

**Architecture:** Same stack and patterns as Phases 1–5: sqlc queries → handler methods → templ components, HTMX swaps into `#browser-content` or table rows, Chi routes wired in `cmd/server/main.go`. No schema migration needed — `files.status='deleted'` and the `shares` table already exist.

**Tech Stack:** Go 1.25, Chi v5, sqlc (pgx/v5), a-h/templ, HTMX 2 + Alpine.js 3.

**Policy constraints (from project memory):** Hard delete is admin-only. Soft delete by file author. Viewers cannot delete. Trash and restore are admin-only ("can be recovered by an admin").

**Bug fixed en route:** `DeleteFile` with `?permanent=true` currently soft-deletes the row and removes the S3 object — the DB row is never deleted, so it would appear in trash as a ghost whose object is gone. Permanent delete must remove the row.

---

### Task 1: SQL queries + sqlc generate

**Files:**
- Modify: `internal/db/queries/files.sql` (search, restore, hard delete, list deleted)
- Modify: `internal/db/queries/folders.sql` (search folders)
- Modify: `internal/db/queries/shares.sql` (list shares, revoke)

- [ ] **Step 1: Add to `files.sql`**

```sql
-- name: SearchFiles :many
SELECT * FROM files
WHERE status = 'active' AND name ILIKE '%' || $1 || '%'
ORDER BY name ASC
LIMIT 100;

-- name: ListDeletedFiles :many
SELECT f.*, u.name AS uploader_name
FROM files f
LEFT JOIN users u ON u.id = f.uploaded_by
WHERE f.status = 'deleted'
ORDER BY f.created_at DESC;

-- name: RestoreFile :one
UPDATE files SET status = 'active'
WHERE id = $1 AND status = 'deleted'
RETURNING *;

-- name: HardDeleteFile :exec
DELETE FROM files WHERE id = $1;
```

- [ ] **Step 2: Add to `folders.sql`**

```sql
-- name: SearchFolders :many
SELECT * FROM folders
WHERE name ILIKE '%' || $1 || '%'
ORDER BY name ASC
LIMIT 50;
```

- [ ] **Step 3: Add to `shares.sql`**

```sql
-- name: ListAllShares :many
SELECT s.*, f.name AS file_name, f.status AS file_status, u.name AS creator_name
FROM shares s
JOIN files f ON f.id = s.file_id
LEFT JOIN users u ON u.id = s.created_by
ORDER BY s.created_at DESC;

-- name: ListSharesByCreator :many
SELECT s.*, f.name AS file_name, f.status AS file_status, u.name AS creator_name
FROM shares s
JOIN files f ON f.id = s.file_id
LEFT JOIN users u ON u.id = s.created_by
WHERE s.created_by = $1
ORDER BY s.created_at DESC;

-- name: GetShare :one
SELECT * FROM shares WHERE id = $1;

-- name: RevokeShare :exec
DELETE FROM shares WHERE id = $1;
```

- [ ] **Step 4: Run `sqlc generate` (or `make sqlc`), verify `go build ./...` passes**

- [ ] **Step 5: Commit** `feat: phase 6 SQL queries — search, trash, shares`

---

### Task 2: Fix permanent delete to remove DB row

**Files:**
- Modify: `internal/handler/files.go` (DeleteFile)

- [ ] **Step 1: In `DeleteFile`, branch on `permanent` before deleting**

Replace the unconditional `SoftDeleteFile` call: when `permanent`, call `storage.DeleteObject` first, then `h.queries.HardDeleteFile(r.Context(), fileUUID)`; audit action `"file.hard_delete"`. Non-permanent path keeps `SoftDeleteFile` and `"file.delete"`.

```go
if permanent {
    if err := storage.DeleteObject(r.Context(), file.S3Key); err != nil {
        _ = err // S3 object may already be gone; still remove the record
    }
    if err := h.queries.HardDeleteFile(r.Context(), fileUUID); err != nil {
        http.Error(w, "failed to delete file", http.StatusInternalServerError)
        return
    }
} else {
    if err := h.queries.SoftDeleteFile(r.Context(), fileUUID); err != nil {
        http.Error(w, "failed to delete file", http.StatusInternalServerError)
        return
    }
}
```

Audit log action: `action := "file.delete"; if permanent { action = "file.hard_delete" }`.

- [ ] **Step 2: `go build ./...` + `go test ./internal/handler/`**

- [ ] **Step 3: Commit** `fix: permanent delete removes DB row, logs file.hard_delete`

---

### Task 3: Search

**Files:**
- Modify: `internal/handler/files.go` (Search handler)
- Modify: `web/templates/file_browser.templ` (search box in toolbar, SearchResults templ)
- Modify: `cmd/server/main.go` (route)

- [ ] **Step 1: Add `Search` handler to files.go**

```go
// GET /files/search?q= — name search across all active files and folders.
func (h *FilesHandler) Search(w http.ResponseWriter, r *http.Request) {
    user, ok := auth.GetCurrentUser(r.Context())
    if !ok {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    q := strings.TrimSpace(r.URL.Query().Get("q"))
    if q == "" {
        h.List(w, r) // empty query falls back to normal root listing
        return
    }
    qp := &q
    dbFiles, err := h.queries.SearchFiles(r.Context(), qp)
    if err != nil { http.Error(w, "search failed", http.StatusInternalServerError); return }
    dbFolders, err := h.queries.SearchFolders(r.Context(), qp)
    if err != nil { http.Error(w, "search failed", http.StatusInternalServerError); return }
    // map to viewmodels (same mapping as List), render SearchResults
}
```

(Exact sqlc param type — `*string` vs `string` — confirmed after generate; adjust accordingly.)

- [ ] **Step 2: Add search box to `FileBrowserContent` toolbar and a `SearchResults` templ**

Search input (inside toolbar, next to breadcrumb):

```html
<input class="input search-box" type="search" name="q" placeholder="Search files…"
  hx-get="/files/search" hx-trigger="input changed delay:300ms, search"
  hx-target="#browser-content" hx-swap="outerHTML" hx-push-url="true"/>
```

`SearchResults` renders `#browser-content` with the same search box (value retained via `value={ q }`), a "Results for ‘q’" heading, Folders/Files grids reusing `FolderCard`/`FileCard`, and an empty state. Include a "Clear" link back to `/files`.

- [ ] **Step 3: Route** `r.Get("/files/search", filesHandler.Search)` — must register BEFORE `/files/{folderID}` wildcard.

- [ ] **Step 4: `templ generate`, `go build ./...`, manual test in browser**

- [ ] **Step 5: Commit** `feat: file and folder name search`

---

### Task 4: Admin trash page with restore

**Files:**
- Modify: `internal/handler/admin.go` (Trash, RestoreFile)
- Create: `web/templates/admin_trash.templ`
- Modify: `web/templates/sidebar.templ` (Trash link in Admin section)
- Modify: `cmd/server/main.go` (routes)

- [ ] **Step 1: Handlers in admin.go**

`Trash` (GET /admin/trash): admin gate (same `AuthErrorPage(403…)` pattern as ListUsers), `ListDeletedFiles`, map to a small view struct (name, size human, uploader, date, ID), render `AdminTrashPage`.

`RestoreFile` (POST /admin/trash/{fileID}/restore): admin gate, `RestoreFile` query, audit `"file.restore"`, return 200 with `HX-Trigger` toast; HTMX removes the row (`hx-target="closest tr" hx-swap="outerHTML"` returning empty body).

- [ ] **Step 2: `admin_trash.templ`** — `data-table` with Name / Size / Uploaded by / Date / Actions. Actions: Restore button (`hx-post`), Delete permanently button (`hx-delete="/files/{id}?permanent=true"` + `hx-confirm`), both targeting `closest tr`.

- [ ] **Step 3: Sidebar** — add under Admin section:

```html
<a class="nav-item" href="/admin/trash">…Trash</a>
```

- [ ] **Step 4: Routes** in admin group: `r.Get("/trash", adminHandler.Trash)`, `r.Post("/trash/{fileID}/restore", adminHandler.RestoreFile)`

- [ ] **Step 5: `templ generate` + build + test; commit** `feat: admin trash page with restore and permanent delete`

---

### Task 5: Share management page with revoke

**Files:**
- Modify: `internal/handler/share.go` (SharesPage, RevokeShare)
- Create: `web/templates/shares_page.templ`
- Modify: `web/templates/sidebar.templ` (Shared links for admin/editor)
- Modify: `cmd/server/main.go` (routes)

- [ ] **Step 1: Handlers**

`SharesPage` (GET /shares): require login; viewers redirected to /files. Admin → `ListAllShares`; editor → `ListSharesByCreator(user)`. Map to view struct: ID, FileName, FileStatus, Slug, CreatorName, CreatedAt, ExpiresAt (or "Never"), ViewCount, Expired bool. Render `SharesPage` templ.

`RevokeShare` (DELETE /shares/{shareID}): `GetShare`; allow if admin or `share.CreatedBy == user.ID`; `RevokeShare` query; audit `"share.revoke"`; 200 + toast trigger, empty body for row swap.

- [ ] **Step 2: `shares_page.templ`** — `data-table`: File / Link (Copy button using `navigator.clipboard` + slug in `data-slug`) / Created by / Created / Expires / Views / Revoke (`hx-delete` + `hx-confirm`, `hx-target="closest tr"`). Expired or non-active-file rows shown dimmed with a badge.

- [ ] **Step 3: Sidebar** — for admin/editor:

```html
<a class="nav-item" href="/shares">…Shared links</a>
```

- [ ] **Step 4: Routes** in auth group: `r.Get("/shares", shareHandler.SharesPage)`, `r.Delete("/shares/{shareID}", shareHandler.RevokeShare)`

- [ ] **Step 5: `templ generate` + build + test; commit** `feat: share management page with revoke`

---

### Task 6: Tests + final verification

- [ ] **Step 1: Handler tests** (nil-queries pattern used by existing tests):
  - `TestTrash_NonAdminForbidden` — editor gets 403 from GET /admin/trash
  - `TestRestoreFile_NonAdminForbidden` — editor gets 403
  - `TestRevokeShare_ViewerForbidden` — viewer gets 403
  - `TestSearch_Unauthenticated` — no user in context → 401

- [ ] **Step 2: `go build ./... && go test ./...`**

- [ ] **Step 3: `docker compose up -d --build`, verify startup logs, smoke-test all three features in browser**

- [ ] **Step 4: Final commit + push**
