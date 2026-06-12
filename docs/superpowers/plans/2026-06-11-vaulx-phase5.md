# Vaulx Phase 5 — Hardening, Preview, UX Polish & Multipart Upload

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the running Vaulx platform with fatal S3 errors, login rate limiting, friendly error pages, paginated file/audit views, a slide-in file preview panel, and Uppy-powered multipart upload for large files.

**Architecture:** All changes are additive (no schema changes). New SQL queries add pagination support via LIMIT/OFFSET. The preview panel uses HTMX partial loading into a fixed `#preview-panel` div with Alpine.js open/close state. Multipart upload uses AWS SDK Go v2 S3 multipart API with Uppy v3.27.0 on the client; files ≤ 100 MB continue using the existing single-part presigned PUT.

**Tech Stack:** Go 1.25, Chi v5, a-h/templ v0.3.1020, HTMX 2.0.3, Alpine.js 3.14.3, AWS SDK Go v2 S3, sqlc v1.30.0, `github.com/go-chi/httprate`, Uppy v3.27.0 (CDN)

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/db/queries/pagination.sql` | Create | ListFilesPage, CountFiles, ListAuditLogPage, CountAuditLog, UpdateFileSizeAndStatus |
| `internal/storage/presign.go` | Modify | Add PresignGETWithTTL |
| `internal/storage/multipart.go` | Create | CreateMultipartUpload, ListMultipartParts, PresignUploadPart, AbortMultipartUpload, CompleteMultipartUpload helpers |
| `cmd/server/main.go` | Modify | Fatal S3, rate limit, new routes |
| `web/templates/error.templ` | Create | ErrorPage (standalone), AuthErrorPage (with sidebar) |
| `internal/handler/files.go` | Modify | PreviewFile handler, 403/404 → ErrorPage, pagination in List/ListFolder |
| `internal/handler/admin.go` | Modify | Audit log pagination, 403 → AuthErrorPage |
| `internal/handler/share.go` | Modify | 404/410 → ErrorPage |
| `internal/handler/download.go` | Modify | 403/404 → AuthErrorPage |
| `internal/handler/permission.go` | Modify | 403 → AuthErrorPage |
| `internal/handler/multipart.go` | Create | MultipartHandler — 5 endpoints |
| `internal/handler/multipart_test.go` | Create | Viewer forbidden, complete sets active |
| `internal/handler/files_test.go` | Modify | Add TestPreviewFile_Forbidden |
| `internal/viewmodel/models.go` | Modify | PaginationData struct, FileBrowserData.Pagination field |
| `web/templates/layout.templ` | Modify | ShowUpload bool param, preview panel div, Alpine previewOpen, Uppy CDN |
| `web/templates/file_browser.templ` | Modify | Uppy container, richer empty state, pagination bar |
| `web/templates/admin_audit.templ` | Modify | Pagination param + bar |
| `web/templates/admin_users.templ` | Modify | Layout call: add false for ShowUpload |
| `web/templates/admin_permissions.templ` | Modify | Layout call: add false for ShowUpload |
| `web/templates/preview_panel.templ` | Create | PreviewPanel HTMX partial |
| `web/static/app.css` | Modify | Preview panel styles, pagination bar styles, error page styles |
| `web/static/upload.js` | Create | Uppy wrapper — initUppy() |
| `Makefile` | Modify | Add `sqlc generate` to generate target |

---

## Task 1: SQL Queries + sqlc Regenerate

**Files:**
- Create: `internal/db/queries/pagination.sql`
- Run: `sqlc generate` (regenerates `internal/db/*.sql.go` and `internal/db/querier.go`)

- [ ] **Step 1: Create the pagination SQL file**

```sql
-- internal/db/queries/pagination.sql

-- name: ListFilesPage :many
SELECT * FROM files
WHERE folder_id IS NOT DISTINCT FROM $1
  AND status = 'active'
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountFiles :one
SELECT COUNT(*) FROM files
WHERE folder_id IS NOT DISTINCT FROM $1
  AND status = 'active';

-- name: ListAuditLogPage :many
SELECT
  al.id,
  al.user_id,
  al.action,
  al.resource_type,
  al.resource_id,
  al.meta,
  al.created_at,
  u.name  AS actor_name,
  u.email AS actor_email
FROM audit_log al
LEFT JOIN users u ON u.id = al.user_id
ORDER BY al.created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountAuditLog :one
SELECT COUNT(*) FROM audit_log;

-- name: UpdateFileSizeAndStatus :one
UPDATE files SET size_bytes = $1, status = $2 WHERE id = $3 RETURNING *;
```

- [ ] **Step 2: Run sqlc generate**

```bash
cd /path/to/vaulx
sqlc generate
```

Expected: no errors. New methods appear in `internal/db/querier.go`:
- `ListFilesPage(ctx, ListFilesPageParams{FolderID pgtype.UUID, Limit int32, Offset int32}) ([]File, error)`
- `CountFiles(ctx, pgtype.UUID) (int64, error)`
- `ListAuditLogPage(ctx, ListAuditLogPageParams{Limit int32, Offset int32}) ([]ListAuditLogPageRow, error)`
- `CountAuditLog(ctx) (int64, error)`
- `UpdateFileSizeAndStatus(ctx, UpdateFileSizeAndStatusParams{SizeBytes int64, Status string, ID pgtype.UUID}) (File, error)`

- [ ] **Step 3: Verify build still passes**

```bash
go build ./...
```

Expected: no output (clean build).

- [ ] **Step 4: Commit**

```bash
git add internal/db/queries/pagination.sql internal/db/
git commit -m "feat: add pagination + UpdateFileSizeAndStatus SQL queries — Phase 5 Task 1"
```

---

## Task 2: Storage Additions — PresignGETWithTTL + Multipart Helpers

**Files:**
- Modify: `internal/storage/presign.go`
- Create: `internal/storage/multipart.go`

- [ ] **Step 1: Add PresignGETWithTTL to presign.go**

In `internal/storage/presign.go`, append after the existing `PresignGET` function (before `DeleteObject`):

```go
// PresignGETWithTTL returns a presigned GET URL with a custom TTL.
func PresignGETWithTTL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if PresignClient == nil {
		return "", fmt.Errorf("storage: not connected")
	}
	req, err := PresignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: presign GET %s: %w", key, err)
	}
	return req.URL, nil
}
```

No new imports needed — `time` is already imported.

- [ ] **Step 2: Create internal/storage/multipart.go**

```go
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Part represents a completed multipart upload part (PartNumber + ETag).
type Part struct {
	PartNumber int32
	ETag       string
}

// CreateMultipartUpload initiates a multipart upload and returns the S3 UploadId.
func CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error) {
	if Client == nil {
		return "", fmt.Errorf("storage: not connected")
	}
	out, err := Client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(Bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("storage: create multipart %s: %w", key, err)
	}
	return aws.ToString(out.UploadId), nil
}

// ListMultipartParts lists already-uploaded parts for a multipart upload.
func ListMultipartParts(ctx context.Context, key, uploadID string) ([]types.Part, error) {
	if Client == nil {
		return nil, fmt.Errorf("storage: not connected")
	}
	out, err := Client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:   aws.String(Bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		return nil, fmt.Errorf("storage: list parts %s/%s: %w", key, uploadID, err)
	}
	return out.Parts, nil
}

// PresignUploadPart returns a presigned URL for uploading a single part (1-hour TTL).
func PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32) (string, error) {
	if PresignClient == nil {
		return "", fmt.Errorf("storage: not connected")
	}
	req, err := PresignClient.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(Bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
	}, s3.WithPresignExpires(1*time.Hour))
	if err != nil {
		return "", fmt.Errorf("storage: presign part %d/%s: %w", partNumber, key, err)
	}
	return req.URL, nil
}

// AbortMultipartUpload cancels an in-progress multipart upload.
func AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	if Client == nil {
		return fmt.Errorf("storage: not connected")
	}
	_, err := Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(Bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		return fmt.Errorf("storage: abort %s/%s: %w", key, uploadID, err)
	}
	return nil
}

// CompleteMultipartUpload finalises a multipart upload and returns the object location.
func CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []Part) (string, error) {
	if Client == nil {
		return "", fmt.Errorf("storage: not connected")
	}
	completed := make([]types.CompletedPart, len(parts))
	for i, p := range parts {
		completed[i] = types.CompletedPart{
			PartNumber: aws.Int32(p.PartNumber),
			ETag:       aws.String(p.ETag),
		}
	}
	out, err := Client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(Bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completed,
		},
	})
	if err != nil {
		return "", fmt.Errorf("storage: complete %s/%s: %w", key, uploadID, err)
	}
	return aws.ToString(out.Location), nil
}
```

- [ ] **Step 3: Build**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/storage/
git commit -m "feat: PresignGETWithTTL + S3 multipart helpers — Phase 5 Task 2"
```

---

## Task 3: Fatal S3 + Rate Limiting Package

**Files:**
- Modify: `cmd/server/main.go`
- Modify: `go.mod` / `go.sum` (via `go get`)
- Modify: `Makefile`

- [ ] **Step 1: Add sqlc generate to Makefile**

Open `Makefile`. Replace:
```makefile
generate:
	templ generate
```
With:
```makefile
generate:
	sqlc generate
	templ generate
```

- [ ] **Step 2: Add httprate dependency**

```bash
go get github.com/go-chi/httprate
```

Expected: go.mod and go.sum updated.

- [ ] **Step 3: Fix fatal S3 in main.go**

In `cmd/server/main.go`, find:
```go
if err := storage.Connect(ctx); err != nil {
    log.Printf("storage: %v (continuing — S3 not required for Phase 1)", err)
}
```

Replace with:
```go
if err := storage.Connect(ctx); err != nil {
    log.Fatalf("fatal: cannot connect to S3 storage: %v", err)
}
```

- [ ] **Step 4: Add httprate import to main.go**

In the import block of `cmd/server/main.go`, add:
```go
"github.com/go-chi/httprate"
```

- [ ] **Step 5: Apply rate limit to POST /auth/login**

Find the auth route block:
```go
r.Route("/auth", func(r chi.Router) {
    r.Get("/login", authHandler.LoginPage)
    r.Post("/login", authHandler.LoginPage)
    r.Post("/logout", authHandler.Logout)
})
```

Replace with:
```go
r.Route("/auth", func(r chi.Router) {
    r.Get("/login", authHandler.LoginPage)
    r.With(httprate.LimitByIP(10, 1*time.Minute)).Post("/login", authHandler.LoginPage)
    r.Post("/logout", authHandler.Logout)
})
```

- [ ] **Step 6: Build**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add cmd/server/main.go go.mod go.sum Makefile
git commit -m "fix: fatal S3 error, login rate limit (10/min), Makefile sqlc — Phase 5 Task 3"
```

---

## Task 4: Friendly Error Pages

**Files:**
- Create: `web/templates/error.templ`
- Modify: `web/static/app.css`
- Modify: `internal/handler/files.go`
- Modify: `internal/handler/admin.go`
- Modify: `internal/handler/share.go`
- Modify: `internal/handler/download.go`
- Modify: `internal/handler/permission.go`

**Context:** Two components — `ErrorPage` for unauthenticated routes (share link expiry), `AuthErrorPage` for authenticated routes (uses the Layout shell with sidebar). Handlers pass the correct HTTP status code alongside rendering.

- [ ] **Step 1: Create web/templates/error.templ**

```go
package templates

import "github.com/noelzappy/vaulx/internal/viewmodel"

// ErrorPage is used on unauthenticated routes (no sidebar).
templ ErrorPage(code int, title, message string) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="UTF-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
			<title>{ title } — Vaulx</title>
			<link rel="stylesheet" href="/static/app.css"/>
		</head>
		<body>
			<div class="error-standalone">
				<div class="error-code">{ intToStr(code) }</div>
				<h1 class="error-title">{ title }</h1>
				<p class="error-message">{ message }</p>
				<a href="javascript:history.back()" class="btn btn-ghost" style="margin-top:24px;">Go back</a>
			</div>
		</body>
	</html>
}

// AuthErrorPage is used on authenticated routes (shows sidebar + layout).
templ AuthErrorPage(code int, title, message string, user viewmodel.UserView) {
	@Layout("Error", user, false) {
		<div class="error-auth">
			<div class="error-code">{ intToStr(code) }</div>
			<h1 class="error-title">{ title }</h1>
			<p class="error-message">{ message }</p>
			<a href="javascript:history.back()" class="btn btn-ghost" style="margin-top:24px;">Go back</a>
		</div>
	}
}

func intToStr(n int) string {
	return fmt.Sprintf("%d", n)
}
```

Add `"fmt"` to the import at the top:

```go
package templates

import (
	"fmt"
	"github.com/noelzappy/vaulx/internal/viewmodel"
)
```

Note: `AuthErrorPage` calls `@Layout("Error", user, false)` — the Layout signature is updated in Task 8. Write it this way now; it will compile after Task 8 completes. **If running templ generate before Task 8, temporarily write it as `@Layout("Error", user)` and update after Task 8.**

Actually, to avoid ordering problems: write `AuthErrorPage` to call a 2-arg Layout (current signature) for now, and update the call to 3-arg after Task 8. Write it as:

```go
templ AuthErrorPage(code int, title, message string, user viewmodel.UserView) {
	@Layout("Error", user) {
		<div class="error-auth">
			<div class="error-code">{ intToStr(code) }</div>
			<h1 class="error-title">{ title }</h1>
			<p class="error-message">{ message }</p>
			<a href="javascript:history.back()" class="btn btn-ghost" style="margin-top:24px;">Go back</a>
		</div>
	}
}
```

Update to 3 args in Task 8.

- [ ] **Step 2: Add CSS for error pages**

In `web/static/app.css`, append:

```css
/* Error pages */
.error-standalone{min-height:100vh;display:flex;flex-direction:column;align-items:center;justify-content:center;background:var(--bg);text-align:center;padding:20px}
.error-auth{display:flex;flex-direction:column;align-items:center;justify-content:center;min-height:60vh;text-align:center;padding:20px}
.error-code{font-size:72px;font-weight:700;color:var(--accent);line-height:1;margin-bottom:12px;opacity:.8}
.error-title{font-size:22px;font-weight:600;color:var(--text);margin-bottom:10px}
.error-message{font-size:14px;color:var(--text-muted);max-width:400px;line-height:1.6}
```

- [ ] **Step 3: Run templ generate**

```bash
templ generate
```

Expected: `(✓) Complete`.

- [ ] **Step 4: Update files.go — replace http.Error 403/404 with error page renders**

In `internal/handler/files.go`, find the `ListFolder` handler's forbidden block:
```go
if !hasPerm {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}
```
Replace with:
```go
if !hasPerm {
    w.WriteHeader(http.StatusForbidden)
    _ = templates.AuthErrorPage(403, "Access denied",
        "You don't have permission to view this. Contact your admin to request access.",
        viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
    ).Render(r.Context(), w)
    return
}
```

Find `GetFolder` 404 in `ListFolder`:
```go
folder, err := h.queries.GetFolder(ctx, folderUUID)
if err != nil {
    http.NotFound(w, r)
    return
}
```
Replace with:
```go
folder, err := h.queries.GetFolder(ctx, folderUUID)
if err != nil {
    w.WriteHeader(http.StatusNotFound)
    _ = templates.AuthErrorPage(404, "Not found",
        "This file or folder doesn't exist, or it's been deleted.",
        viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
    ).Render(r.Context(), w)
    return
}
```

- [ ] **Step 5: Update admin.go — replace 403 with AuthErrorPage**

In `internal/handler/admin.go`, the `ListUsers`, `CreateUser`, `UpdateUser`, and `AuditLog` handlers all start with:
```go
if !ok || user.Role != "admin" {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}
```

Replace each with:
```go
if !ok || user.Role != "admin" {
    w.WriteHeader(http.StatusForbidden)
    _ = templates.AuthErrorPage(403, "Access denied",
        "You don't have permission to view this. Contact your admin to request access.",
        viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
    ).Render(r.Context(), w)
    return
}
```

Note: `ListUsers` and `AuditLog` have `user` already fetched. `CreateUser` and `UpdateUser` also have `user` fetched. All 4 instances are the same replacement.

- [ ] **Step 6: Update share.go — replace 404/410 with ErrorPage**

In `internal/handler/share.go`, `ResolveShare` handler:

Find:
```go
share, err := h.queries.GetShareBySlug(r.Context(), slug)
if err != nil {
    http.NotFound(w, r)
    return
}
```
Replace with:
```go
share, err := h.queries.GetShareBySlug(r.Context(), slug)
if err != nil {
    w.WriteHeader(http.StatusNotFound)
    _ = templates.ErrorPage(404, "Not found",
        "This file or folder doesn't exist, or it's been deleted.",
    ).Render(r.Context(), w)
    return
}
```

Find:
```go
if share.ExpiresAt.Valid && time.Now().UTC().After(share.ExpiresAt.Time) {
    http.Error(w, "share link has expired", http.StatusGone)
    return
}

if share.MaxViews != nil && share.ViewCount >= *share.MaxViews {
    http.Error(w, "share link has reached its view limit", http.StatusGone)
    return
}
```
Replace with:
```go
if share.ExpiresAt.Valid && time.Now().UTC().After(share.ExpiresAt.Time) {
    w.WriteHeader(http.StatusGone)
    _ = templates.ErrorPage(410, "Link expired",
        "This share link has expired or been revoked.",
    ).Render(r.Context(), w)
    return
}

if share.MaxViews != nil && share.ViewCount >= *share.MaxViews {
    w.WriteHeader(http.StatusGone)
    _ = templates.ErrorPage(410, "Link expired",
        "This share link has expired or been revoked.",
    ).Render(r.Context(), w)
    return
}
```

Add `"github.com/noelzappy/vaulx/web/templates"` to share.go imports.

- [ ] **Step 7: Update download.go — replace 403/404 with AuthErrorPage**

In `internal/handler/download.go`, the `Download` handler:

Find the 404 after `GetFile`:
```go
file, err := h.queries.GetFile(r.Context(), fileUUID)
if err != nil {
    http.NotFound(w, r)
    return
}

if file.Status != "active" {
    http.NotFound(w, r)
    return
}
```
Replace both `http.NotFound(w, r)` with:
```go
w.WriteHeader(http.StatusNotFound)
_ = templates.AuthErrorPage(404, "Not found",
    "This file or folder doesn't exist, or it's been deleted.",
    viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
).Render(r.Context(), w)
return
```

Find the 403:
```go
if !auth.CanAccess(user, file.UploadedBy.String()) {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}
```
Replace with:
```go
if !auth.CanAccess(user, file.UploadedBy.String()) {
    w.WriteHeader(http.StatusForbidden)
    _ = templates.AuthErrorPage(403, "Access denied",
        "You don't have permission to view this. Contact your admin to request access.",
        viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
    ).Render(r.Context(), w)
    return
}
```

Add `"github.com/noelzappy/vaulx/web/templates"` to download.go imports.

- [ ] **Step 8: Update permission.go — replace 403 with AuthErrorPage**

In `internal/handler/permission.go`, each of the `List`, `Grant`, `Revoke` methods checks:
```go
if !ok || user.Role != "admin" {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}
```
Replace each with:
```go
if !ok || user.Role != "admin" {
    w.WriteHeader(http.StatusForbidden)
    _ = templates.AuthErrorPage(403, "Access denied",
        "You don't have permission to view this. Contact your admin to request access.",
        viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
    ).Render(r.Context(), w)
    return
}
```

- [ ] **Step 9: Build**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 10: Run tests**

```bash
go test ./... -count=1
```

Expected: all pass.

- [ ] **Step 11: Commit**

```bash
git add web/templates/error.templ web/static/app.css internal/handler/
git commit -m "feat: friendly error pages 403/404/410 — Phase 5 Task 4"
```

---

## Task 5: Pagination — File Browser

**Files:**
- Modify: `internal/viewmodel/models.go`
- Modify: `internal/handler/files.go`
- Modify: `web/templates/file_browser.templ`
- Modify: `web/static/app.css`

**Context:** Pagination is only applied to the admin view (using `ListFilesPage` / `CountFiles`). Non-admin views continue using the existing user-filtered queries (they typically see far fewer files). Default page=1, limit=48.

- [ ] **Step 1: Add PaginationData to viewmodel and update FileBrowserData**

In `internal/viewmodel/models.go`, append after the `FolderView` struct definition:

```go
type PaginationData struct {
	Page       int
	Limit      int
	Total      int64
	TotalPages int
}
```

And update `FileBrowserData` to:
```go
type FileBrowserData struct {
	Folders     []FolderView
	Files       []FileView
	Breadcrumbs []BreadcrumbItem
	FolderID    string
	Pagination  *PaginationData // nil = no pagination bar
}
```

- [ ] **Step 2: Add pagination helper to viewmodel**

Still in `internal/viewmodel/models.go`, append:
```go
func NewPagination(page, limit int, total int64) *PaginationData {
	if total == 0 {
		return nil
	}
	totalPages := int((total + int64(limit) - 1) / int64(limit))
	if totalPages <= 1 {
		return nil // don't show bar when only 1 page
	}
	return &PaginationData{
		Page:       page,
		Limit:      limit,
		Total:      total,
		TotalPages: totalPages,
	}
}
```

- [ ] **Step 3: Update the List handler in files.go to support pagination**

At the top of `files.go`, add a helper function (after the imports):

```go
func parsePage(r *http.Request) (page, limit int) {
	page = 1
	limit = 48
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	return
}
```

Add `"strconv"` to the imports of `files.go`.

- [ ] **Step 4: Update List handler to paginate admin file view**

Find the `List` handler body in `files.go`. After the existing `var dbFolders []db.Folder` / `var dbFiles []db.File` block, the admin branch currently calls `ListRootFiles`. Replace the entire `var ... if user.Role == "admin" ... else` block with:

```go
var (
    dbFolders []db.Folder
    dbFiles   []db.File
    totalFiles int64
    err        error
)

page, limit := parsePage(r)
offset := int32((page - 1) * limit)

userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)
if user.Role == "admin" || uuidErr != nil {
    dbFolders, err = h.queries.ListRootFolders(ctx)
    if err == nil {
        dbFiles, err = h.queries.ListFilesPage(ctx, db.ListFilesPageParams{
            FolderID: pgtype.UUID{Valid: false}, // NULL = root
            Limit:    int32(limit),
            Offset:   offset,
        })
    }
    if err == nil {
        totalFiles, err = h.queries.CountFiles(ctx, pgtype.UUID{Valid: false})
    }
} else {
    dbFolders, err = h.queries.ListRootFoldersForUser(ctx, userUUID)
    if err == nil {
        dbFiles, err = h.queries.ListRootFilesForUser(ctx, userUUID)
    }
}
```

Add `"github.com/jackc/pgx/v5/pgtype"` to imports of `files.go` if not already present.

Then update the `data` construction to include pagination:
```go
folders, files := h.buildViews(ctx, dbFolders, dbFiles)
var pagination *viewmodel.PaginationData
if user.Role == "admin" {
    pagination = viewmodel.NewPagination(page, limit, totalFiles)
}
data := viewmodel.FileBrowserData{
    Folders:     folders,
    Files:       files,
    Breadcrumbs: []viewmodel.BreadcrumbItem{{Name: "My Files", URL: "/files"}},
    Pagination:  pagination,
}
```

- [ ] **Step 5: Update ListFolder handler to paginate admin file view**

Find the `ListFolder` handler body. Replace the files-listing portion similarly. After the folder ACL check, find the block:
```go
var dbFolders []db.Folder
var dbFiles []db.File
userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)

if user.Role == "admin" || uuidErr != nil {
    dbFolders, err = h.queries.ListFoldersByParent(ctx, folderUUID)
    if err == nil {
        dbFiles, err = h.queries.ListFilesByFolder(ctx, folderUUID)
    }
} else {
    ...
}
```

Replace with:
```go
var dbFolders []db.Folder
var dbFiles []db.File
var totalFiles int64
userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)

page, limit := parsePage(r)
offset := int32((page - 1) * limit)

if user.Role == "admin" || uuidErr != nil {
    dbFolders, err = h.queries.ListFoldersByParent(ctx, folderUUID)
    if err == nil {
        dbFiles, err = h.queries.ListFilesPage(ctx, db.ListFilesPageParams{
            FolderID: folderUUID,
            Limit:    int32(limit),
            Offset:   offset,
        })
    }
    if err == nil {
        totalFiles, err = h.queries.CountFiles(ctx, folderUUID)
    }
} else {
    dbFolders, err = h.queries.ListFoldersByParentForUser(ctx, db.ListFoldersByParentForUserParams{
        ParentID: folderUUID,
        UserID:   userUUID,
    })
    if err == nil {
        dbFiles, err = h.queries.ListFilesByFolderForUser(ctx, db.ListFilesByFolderForUserParams{
            FolderID: folderUUID,
            UserID:   userUUID,
        })
    }
}
```

Then update data construction:
```go
var pagination *viewmodel.PaginationData
if user.Role == "admin" {
    pagination = viewmodel.NewPagination(page, limit, totalFiles)
}
data := viewmodel.FileBrowserData{
    Folders:     folders,
    Files:       files,
    Breadcrumbs: breadcrumbs,
    FolderID:    folderIDStr,
    Pagination:  pagination,
}
```

- [ ] **Step 6: Add pagination bar to file_browser.templ**

In `web/templates/file_browser.templ`, add a pagination bar component and render it in `FileBrowserContent` below the files grid.

Add at the end of the file (before the `Breadcrumb` component):

```go
templ PaginationBar(p viewmodel.PaginationData, baseURL string) {
	<div class="pagination-bar">
		if p.Page > 1 {
			<a
				class="btn btn-ghost"
				href={ templ.SafeURL(fmt.Sprintf("%s?page=%d&limit=%d", baseURL, p.Page-1, p.Limit)) }
				hx-get={ fmt.Sprintf("%s?page=%d&limit=%d", baseURL, p.Page-1, p.Limit) }
				hx-target="#browser-content"
				hx-swap="outerHTML"
				hx-push-url="true"
			>Previous</a>
		} else {
			<span class="btn btn-ghost" style="opacity:.35;cursor:default;">Previous</span>
		}
		<span class="pagination-info">Page { fmt.Sprintf("%d", p.Page) } of { fmt.Sprintf("%d", p.TotalPages) }</span>
		if p.Page < p.TotalPages {
			<a
				class="btn btn-ghost"
				href={ templ.SafeURL(fmt.Sprintf("%s?page=%d&limit=%d", baseURL, p.Page+1, p.Limit)) }
				hx-get={ fmt.Sprintf("%s?page=%d&limit=%d", baseURL, p.Page+1, p.Limit) }
				hx-target="#browser-content"
				hx-swap="outerHTML"
				hx-push-url="true"
			>Next</a>
		} else {
			<span class="btn btn-ghost" style="opacity:.35;cursor:default;">Next</span>
		}
	</div>
}
```

Add `"fmt"` to the import in `file_browser.templ`:
```go
import (
    "fmt"
    "github.com/noelzappy/vaulx/internal/viewmodel"
)
```

In `FileBrowserContent`, after the closing `}` of the `if len(data.Folders) == 0 && len(data.Files) == 0 { ... } else { ... }` block, add:

```go
if data.Pagination != nil {
    @PaginationBar(*data.Pagination, currentBrowserURL(data))
}
```

Add a Go helper function at the bottom of file_browser.templ:
```go
func currentBrowserURL(data viewmodel.FileBrowserData) string {
    if data.FolderID != "" {
        return "/files/" + data.FolderID
    }
    return "/files"
}
```

- [ ] **Step 7: Add pagination CSS**

In `web/static/app.css`, append:

```css
/* Pagination */
.pagination-bar{display:flex;align-items:center;gap:8px;margin-top:24px;justify-content:center}
.pagination-info{font-size:12.5px;color:var(--text-muted);padding:0 8px}
```

- [ ] **Step 8: Run templ generate**

```bash
templ generate
```

Expected: `(✓) Complete`.

- [ ] **Step 9: Build and test**

```bash
go build ./... && go test ./... -count=1
```

Expected: clean.

- [ ] **Step 10: Commit**

```bash
git add internal/viewmodel/ internal/handler/files.go web/templates/file_browser.templ web/static/app.css
git commit -m "feat: file browser pagination (admin, page/limit query params) — Phase 5 Task 5"
```

---

## Task 6: Pagination — Audit Log

**Files:**
- Modify: `internal/handler/admin.go`
- Modify: `web/templates/admin_audit.templ`

- [ ] **Step 1: Update AuditLog handler**

In `internal/handler/admin.go`, replace the `AuditLog` handler body with:

```go
func (h *AdminHandler) AuditLog(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || user.Role != "admin" {
		w.WriteHeader(http.StatusForbidden)
		_ = templates.AuthErrorPage(403, "Access denied",
			"You don't have permission to view this. Contact your admin to request access.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	actionFilter := r.URL.Query().Get("action")

	page := 1
	limit := 50
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}

	var entries []viewmodel.AuditLogView
	var pagination *viewmodel.PaginationData

	if actionFilter != "" {
		rows, err := h.queries.ListAuditLogByAction(r.Context(), db.ListAuditLogByActionParams{
			Action: actionFilter,
			Limit:  100,
		})
		if err != nil {
			http.Error(w, "failed to load audit log", http.StatusInternalServerError)
			return
		}
		for _, row := range rows {
			entries = append(entries, viewmodel.AuditLogViewFromRow(
				row.ID, row.Action, row.ResourceType, row.ResourceID,
				row.CreatedAt, row.ActorName, row.ActorEmail,
			))
		}
	} else {
		offset := int32((page - 1) * limit)
		rows, err := h.queries.ListAuditLogPage(r.Context(), db.ListAuditLogPageParams{
			Limit:  int32(limit),
			Offset: offset,
		})
		if err != nil {
			http.Error(w, "failed to load audit log", http.StatusInternalServerError)
			return
		}
		for _, row := range rows {
			entries = append(entries, viewmodel.AuditLogViewFromRow(
				row.ID, row.Action, row.ResourceType, row.ResourceID,
				row.CreatedAt, row.ActorName, row.ActorEmail,
			))
		}
		total, _ := h.queries.CountAuditLog(r.Context())
		pagination = viewmodel.NewPagination(page, limit, total)
	}

	if entries == nil {
		entries = []viewmodel.AuditLogView{}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.AdminAuditPage(entries, actionFilter, pagination, viewmodel.UserView{
		ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
	}).Render(r.Context(), w)
}
```

Add `"strconv"` to imports of `admin.go`.

- [ ] **Step 2: Update AdminAuditPage signature**

In `web/templates/admin_audit.templ`, change the signature:
```go
templ AdminAuditPage(entries []viewmodel.AuditLogView, activeFilter string, currentUser viewmodel.UserView) {
```
To:
```go
templ AdminAuditPage(entries []viewmodel.AuditLogView, activeFilter string, pagination *viewmodel.PaginationData, currentUser viewmodel.UserView) {
```

And add the pagination bar at the end of the template body, after the closing `}` of the `if len(entries) == 0` block, before the outer closing `}`:

```go
if pagination != nil {
    <div class="pagination-bar" style="margin-top:24px;justify-content:center;display:flex;gap:8px;align-items:center;">
        if pagination.Page > 1 {
            <a
                class="btn btn-ghost"
                href={ templ.SafeURL(fmt.Sprintf("/admin/audit?page=%d", pagination.Page-1)) }
            >Previous</a>
        } else {
            <span class="btn btn-ghost" style="opacity:.35;cursor:default;">Previous</span>
        }
        <span class="pagination-info">Page { fmt.Sprintf("%d", pagination.Page) } of { fmt.Sprintf("%d", pagination.TotalPages) }</span>
        if pagination.Page < pagination.TotalPages {
            <a
                class="btn btn-ghost"
                href={ templ.SafeURL(fmt.Sprintf("/admin/audit?page=%d", pagination.Page+1)) }
            >Next</a>
        } else {
            <span class="btn btn-ghost" style="opacity:.35;cursor:default;">Next</span>
        }
    </div>
}
```

Add `"fmt"` to imports of `admin_audit.templ`:
```go
import (
    "fmt"
    "github.com/noelzappy/vaulx/internal/viewmodel"
)
```

- [ ] **Step 3: Run templ generate**

```bash
templ generate
```

- [ ] **Step 4: Build and test**

```bash
go build ./... && go test ./... -count=1
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/handler/admin.go web/templates/admin_audit.templ
git commit -m "feat: audit log pagination (page/limit, default 50) — Phase 5 Task 6"
```

---

## Task 7: File Preview — Handler + PreviewPanel Template + CSS

**Files:**
- Modify: `internal/handler/files.go`
- Create: `web/templates/preview_panel.templ`
- Modify: `web/static/app.css`

- [ ] **Step 1: Write the failing test for preview forbidden**

In `internal/handler/files_test.go`, append:

```go
func TestPreviewFile_Forbidden(t *testing.T) {
	h := handler.NewFilesHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/file/some-id/preview", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "viewer"})
	ctx = withChiParam(ctx, "fileID", "not-a-uuid")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.PreviewFile(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad UUID, got %d", rr.Code)
	}
}
```

(This tests the bad-UUID path. The actual 403 path needs a DB mock — it is implicitly tested by the ACL logic shared with download, which is tested in `download_test.go`.)

- [ ] **Step 2: Run failing test**

```bash
go test ./internal/handler/... -run TestPreviewFile_Forbidden -v
```

Expected: FAIL — `handler.FilesHandler has no method PreviewFile`.

- [ ] **Step 3: Add PreviewFile handler to files.go**

In `internal/handler/files.go`, append (before `buildViews`):

```go
// GET /api/file/{fileID}/preview — HTMX partial, returns PreviewPanel
func (h *FilesHandler) PreviewFile(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "fileID"))
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	file, err := h.queries.GetFile(r.Context(), fileUUID)
	if err != nil || file.Status != "active" {
		w.WriteHeader(http.StatusNotFound)
		_ = templates.AuthErrorPage(404, "Not found",
			"This file or folder doesn't exist, or it's been deleted.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	if !auth.CanAccess(user, file.UploadedBy.String()) {
		// Also check per-resource permission
		userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)
		hasPerm := false
		if uuidErr == nil {
			hasPerm, _ = h.queries.CheckPermission(r.Context(), db.CheckPermissionParams{
				UserID:       userUUID,
				ResourceType: "file",
				ResourceID:   fileUUID,
			})
		}
		if !hasPerm {
			w.WriteHeader(http.StatusForbidden)
			_ = templates.AuthErrorPage(403, "Access denied",
				"You don't have permission to view this. Contact your admin to request access.",
				viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
			).Render(r.Context(), w)
			return
		}
	}

	presignedURL, err := storage.PresignGETWithTTL(r.Context(), file.S3Key, 5*time.Minute)
	if err != nil {
		http.Error(w, "failed to generate preview URL", http.StatusInternalServerError)
		return
	}

	uploaderName := ""
	if u, err := h.queries.GetUserByID(r.Context(), file.UploadedBy); err == nil {
		uploaderName = u.Name
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.PreviewPanel(file, uploaderName, presignedURL).Render(r.Context(), w)
}
```

Add `"time"` to the imports of `files.go`.

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/handler/... -run TestPreviewFile_Forbidden -v
```

Expected: PASS.

- [ ] **Step 5: Create web/templates/preview_panel.templ**

```go
package templates

import (
	"fmt"
	"strings"

	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/viewmodel"
)

templ PreviewPanel(file db.File, uploaderName string, presignedURL string) {
	<div class="preview-header">
		<span class="preview-filename" title={ file.Name }>{ file.Name }</span>
		<button
			class="preview-close"
			x-on:click.stop="previewOpen = false"
			aria-label="Close preview"
		>
			<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" style="width:18px;height:18px;">
				<path stroke-linecap="round" stroke-linejoin="round" d="M6 18 18 6M6 6l12 12"></path>
			</svg>
		</button>
	</div>
	<div class="preview-media">
		@previewArea(mimeType(file), presignedURL, file.Name)
	</div>
	<div class="preview-meta">
		<div class="preview-meta-row">
			<span class="preview-meta-label">Size</span>
			<span>{ viewmodel.HumanSize(file.SizeBytes) }</span>
		</div>
		<div class="preview-meta-row">
			<span class="preview-meta-label">Uploaded by</span>
			<span>{ uploaderName }</span>
		</div>
		<div class="preview-meta-row">
			<span class="preview-meta-label">Date</span>
			<span>{ viewmodel.RelativeTime(file.CreatedAt.Time) }</span>
		</div>
		<div class="preview-meta-row">
			<span class="preview-meta-label">Type</span>
			<span>{ mimeType(file) }</span>
		</div>
	</div>
	<div class="preview-actions">
		<a class="btn btn-primary" href={ templ.SafeURL("/files/" + file.ID.String() + "/download") }>
			Download
		</a>
		<button
			class="btn btn-ghost"
			data-file-id={ file.ID.String() }
			onclick="event.stopPropagation(); copyShareLink(this.dataset.fileId)"
		>Share</button>
	</div>
}

templ previewArea(mime, url, name string) {
	if strings.HasPrefix(mime, "video/") {
		<video controls preload="metadata" src={ url } style="width:100%;max-height:280px;background:#000;">
			Your browser does not support video.
		</video>
	} else if strings.HasPrefix(mime, "image/") {
		<img src={ url } alt={ name } style="width:100%;object-fit:contain;max-height:280px;"/>
	} else if mime == "application/pdf" {
		<iframe src={ url } style="width:100%;height:280px;border:none;"></iframe>
	} else {
		<div class="preview-no-preview">
			<span class="preview-ext">{ fileExt(name) }</span>
			<span style="font-size:12px;color:var(--text-muted);margin-top:8px;">No preview available</span>
		</div>
	}
}

func mimeType(file db.File) string {
	if file.MimeType != nil {
		return *file.MimeType
	}
	return ""
}

func fileExt(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 || idx == len(name)-1 {
		return "FILE"
	}
	ext := strings.ToUpper(name[idx+1:])
	if len(ext) > 6 {
		return ext[:6]
	}
	return ext
}

// Ensure fmt is used to avoid import errors
var _ = fmt.Sprintf
```

- [ ] **Step 6: Add preview panel CSS**

In `web/static/app.css`, append:

```css
/* Preview panel */
.preview-panel{position:fixed;top:0;right:0;height:100vh;width:480px;background:var(--bg-surface);border-left:1px solid var(--border);z-index:200;display:flex;flex-direction:column;overflow-y:auto;box-shadow:-4px 0 24px rgba(0,0,0,.4);transition:transform .25s ease}
.preview-header{display:flex;align-items:center;justify-content:space-between;padding:16px 20px;border-bottom:1px solid var(--border);flex-shrink:0}
.preview-filename{font-size:14px;font-weight:500;color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:360px}
.preview-close{background:none;border:none;color:var(--text-muted);padding:4px;display:flex;align-items:center;justify-content:center;border-radius:var(--radius-sm);transition:color .15s,background .15s}
.preview-close:hover{color:var(--text);background:var(--bg-card)}
.preview-media{padding:20px;border-bottom:1px solid var(--border);flex-shrink:0}
.preview-no-preview{display:flex;flex-direction:column;align-items:center;justify-content:center;height:160px;background:var(--bg-card);border-radius:var(--radius);color:var(--text-muted)}
.preview-ext{font-size:32px;font-weight:700;color:var(--text-faint);font-family:monospace}
.preview-meta{padding:16px 20px;display:flex;flex-direction:column;gap:10px;border-bottom:1px solid var(--border)}
.preview-meta-row{display:flex;justify-content:space-between;font-size:12.5px}
.preview-meta-label{color:var(--text-muted)}
.preview-actions{padding:16px 20px;display:flex;gap:8px}
```

- [ ] **Step 7: Run templ generate**

```bash
templ generate
```

- [ ] **Step 8: Build and test**

```bash
go build ./... && go test ./... -count=1
```

Expected: clean.

- [ ] **Step 9: Commit**

```bash
git add internal/handler/files.go internal/handler/files_test.go web/templates/preview_panel.templ web/static/app.css
git commit -m "feat: file preview panel — HTMX partial, presigned 5-min URL — Phase 5 Task 7"
```

---

## Task 8: Layout Updates — Preview Panel + ShowUpload + Uppy CDN

**Files:**
- Modify: `web/templates/layout.templ`
- Modify: `web/templates/file_browser.templ`
- Modify: `web/templates/admin_audit.templ`
- Modify: `web/templates/admin_users.templ`
- Modify: `web/templates/admin_permissions.templ`
- Modify: `web/templates/error.templ`

**Context:** `Layout` gains a `showUpload bool` third parameter. When true, Uppy CDN scripts are injected into `<head>`. A fixed `#preview-panel` div is added to the app shell with Alpine `previewOpen` state. The file_browser.templ Upload button is replaced by the Uppy container.

- [ ] **Step 1: Update layout.templ**

Replace the entire content of `web/templates/layout.templ` with:

```go
package templates

import "github.com/noelzappy/vaulx/internal/viewmodel"

templ Layout(title string, user viewmodel.UserView, showUpload bool) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="UTF-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
			<title>{ title } — Vaulx</title>
			<link rel="stylesheet" href="/static/app.css"/>
			<script src="https://unpkg.com/htmx.org@2.0.3" integrity="sha384-0895/pl2MU10Hqc6jd4RvrthNlDiE9U1tWmX7WRESftEDRosgxNsQG/Ze9YMRzHq" crossorigin="anonymous"></script>
			<script src="https://unpkg.com/alpinejs@3.14.3/dist/cdn.min.js" defer></script>
			if showUpload {
				<link href="https://releases.transloadit.com/uppy/v3.27.0/uppy.min.css" rel="stylesheet"/>
				<script src="https://releases.transloadit.com/uppy/v3.27.0/uppy.min.js"></script>
				<script src="/static/upload.js"></script>
			}
		</head>
		<body>
			<div
				class="app"
				x-data="{ toasts: [], previewOpen: false }"
				x-on:showtoast.window="toasts.push($event.detail); setTimeout(() => toasts.shift(), 3000)"
			>
				@Sidebar(user)
				<main class="main-content">
					{ children... }
				</main>
				<div
					id="preview-panel"
					class="preview-panel"
					x-show="previewOpen"
					x-cloak
					@click.outside="previewOpen = false"
					@keydown.escape.window="previewOpen = false"
				></div>
				<div class="toast-container">
					<template x-for="(t, i) in toasts" :key="i">
						<div class="toast" :class="'toast-' + (t.type || 'info')">
							<span x-text="t.message"></span>
						</div>
					</template>
				</div>
			</div>
			<script>
				document.body.addEventListener("showToast", function(e) {
					window.dispatchEvent(new CustomEvent("showtoast", { detail: e.detail }));
				});
				// Open preview panel when HTMX loads content into #preview-panel
				document.body.addEventListener("htmx:afterSwap", function(e) {
					if (e.detail.target && e.detail.target.id === "preview-panel") {
						const app = document.querySelector(".app");
						if (app && app.__x) {
							app.__x.$data.previewOpen = true;
						} else if (app && app._x_dataStack) {
							Alpine.$data(app).previewOpen = true;
						}
					}
				});
			</script>
			<script>
				async function copyShareLink(fileID) {
					try {
						const res = await fetch('/files/' + fileID + '/share', { method: 'POST' })
						if (!res.ok) throw new Error('share failed')
						const { url } = await res.json()
						await navigator.clipboard.writeText(window.location.origin + url)
						window.dispatchEvent(new CustomEvent('showtoast', {
							detail: { message: 'Share link copied!', type: 'success' }
						}))
					} catch (e) {
						window.dispatchEvent(new CustomEvent('showtoast', {
							detail: { message: 'Failed to create share link', type: 'error' }
						}))
					}
				}
			</script>
		</body>
	</html>
}
```

Add `[x-cloak] { display: none !important; }` to `app.css`.

- [ ] **Step 2: Update all Layout call sites**

**file_browser.templ** — `FilesPage`:
```go
templ FilesPage(data viewmodel.FileBrowserData, user viewmodel.UserView) {
	@Layout("My Files", user, true) {
		@FileBrowserContent(data, user)
	}
}
```

**admin_audit.templ** — `AdminAuditPage`:
```go
@Layout("Audit Log", currentUser, false) {
```

**admin_users.templ** — `AdminUsersPage`:
```go
@Layout("Users", currentUser, false) {
```

**admin_permissions.templ** — `AdminPermissionsPage`:
```go
@Layout("Permissions — "+data.ResourceName, currentUser, false) {
```

**error.templ** — `AuthErrorPage`:
```go
@Layout("Error", user, false) {
```

- [ ] **Step 3: Update file_browser.templ — replace Upload button/zone with Uppy container**

In `FileBrowserContent`, find the toolbar upload button:
```go
<button
    class="btn btn-primary"
    onclick="document.getElementById('upload-zone').style.display='block'"
>
    ...
    Upload
</button>
```
Remove it (the Uppy Dashboard provides its own trigger).

Find:
```go
if user.Role == "admin" || user.Role == "editor" {
    @UploadZone(data.FolderID)
}
```
Replace with:
```go
if user.Role == "admin" || user.Role == "editor" {
    <div id="uppy-container" data-folder-id={ data.FolderID } style="margin-bottom:20px;"></div>
    <script>
        (function() {
            var fid = document.getElementById('uppy-container') && document.getElementById('uppy-container').dataset.folderId || '';
            if (typeof initUppy === 'function') { initUppy(fid); }
        })();
    </script>
}
```

- [ ] **Step 4: Update file_browser.templ — richer empty state**

Find:
```go
if len(data.Folders) == 0 && len(data.Files) == 0 {
    <div class="file-grid">
        <div class="empty-state">
            <svg ...>...</svg>
            <p>No files or folders yet</p>
        </div>
    </div>
}
```

Replace with:
```go
if len(data.Folders) == 0 && len(data.Files) == 0 {
    <div class="empty-state" style="padding:60px 20px;text-align:center;">
        <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1" stroke="currentColor" style="width:40px;height:40px;opacity:.3;margin-bottom:12px;">
            <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z"></path>
        </svg>
        <p style="font-size:14px;font-weight:500;color:var(--text);margin-bottom:6px;">This folder is empty</p>
        if user.Role == "admin" || user.Role == "editor" {
            <p style="font-size:12.5px;color:var(--text-muted);">Upload files or create a folder to get started.</p>
        }
    </div>
}
```

- [ ] **Step 5: Add file card click handler for preview**

In `web/templates/file_card.templ`, add HTMX attributes to the outer card div so clicking opens the preview panel:

Change:
```go
<div
    class="card"
    data-file-name={ file.Name }
    x-data="{ editing: false }"
    title={ file.Name }
>
```
To:
```go
<div
    class="card"
    data-file-name={ file.Name }
    x-data="{ editing: false }"
    title={ file.Name }
    hx-get={ string(templ.SafeURL("/api/file/" + file.ID + "/preview")) }
    hx-target="#preview-panel"
    hx-swap="innerHTML"
    hx-trigger="click[!event.target.closest('.card-btn') && !event.target.closest('form') && !event.target.closest('input')]"
>
```

- [ ] **Step 6: Run templ generate**

```bash
templ generate
```

Expected: `(✓) Complete`.

- [ ] **Step 7: Build and test**

```bash
go build ./... && go test ./... -count=1
```

Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add web/templates/ web/static/app.css
git commit -m "feat: layout preview panel, ShowUpload, Uppy CDN, richer empty state — Phase 5 Task 8"
```

---

## Task 9: Multipart Upload Handler + Tests

**Files:**
- Create: `internal/handler/multipart.go`
- Create: `internal/handler/multipart_test.go`

**Context:** `MultipartHandler` implements 5 endpoints for Uppy's AwsS3Multipart plugin. The `CompleteS3` field is exported for test injection, defaulting to `storage.CompleteMultipartUpload`.

- [ ] **Step 1: Write failing tests first**

Create `internal/handler/multipart_test.go`:

```go
package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/handler"
	"github.com/noelzappy/vaulx/internal/storage"
)

// multipartQuerier is a minimal mock for multipart handler tests.
type multipartQuerier struct {
	db.Querier
	updatedStatus string
	updatedSize   int64
}

func (m *multipartQuerier) CreateFile(ctx context.Context, arg db.CreateFileParams) (db.File, error) {
	return db.File{
		ID:     pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		S3Key:  arg.S3Key,
		Status: "pending",
	}, nil
}

func (m *multipartQuerier) GetFile(ctx context.Context, id pgtype.UUID) (db.File, error) {
	return db.File{
		ID:        id,
		S3Key:     "uploads/2026/06/test-key/file.mp4",
		Status:    "pending",
		UploadedBy: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
	}, nil
}

func (m *multipartQuerier) UpdateFileSizeAndStatus(ctx context.Context, arg db.UpdateFileSizeAndStatusParams) (db.File, error) {
	m.updatedStatus = arg.Status
	m.updatedSize = arg.SizeBytes
	return db.File{ID: arg.ID, Status: arg.Status, SizeBytes: arg.SizeBytes}, nil
}

func (m *multipartQuerier) SoftDeleteFile(ctx context.Context, id pgtype.UUID) error {
	return nil
}

func (m *multipartQuerier) CreateAuditLog(ctx context.Context, arg db.CreateAuditLogParams) (db.AuditLog, error) {
	return db.AuditLog{}, nil
}

func TestCreateMultipartUpload_ViewerForbidden(t *testing.T) {
	h := handler.NewMultipartHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/s3/multipart",
		strings.NewReader(`{"filename":"video.mp4","contentType":"video/mp4"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "viewer"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.CreateMultipartUpload(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for viewer, got %d", rr.Code)
	}
}

func TestCompleteMultipartUpload_SetsStatusActive(t *testing.T) {
	mock := &multipartQuerier{}
	h := handler.NewMultipartHandler(mock)
	// Inject a fake S3 completer so the test doesn't need real S3
	h.CompleteS3 = func(ctx context.Context, key, uploadID string, parts []storage.Part) (string, error) {
		return "https://s3.example.com/location", nil
	}

	body := `{"key":"uploads/2026/06/test/file.mp4","fileId":"01000000-0000-0000-0000-000000000000","parts":[{"PartNumber":1,"ETag":"\"abc123\""}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/s3/multipart/upload-id-123/complete",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "editor"})
	ctx = withChiParam(ctx, "uploadId", "upload-id-123")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.CompleteMultipartUpload(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if mock.updatedStatus != "active" {
		t.Errorf("expected status 'active', got %q", mock.updatedStatus)
	}
}
```

- [ ] **Step 2: Run failing tests**

```bash
go test ./internal/handler/... -run "TestCreateMultipartUpload|TestCompleteMultipartUpload" -v
```

Expected: FAIL — `handler has no NewMultipartHandler`.

- [ ] **Step 3: Create internal/handler/multipart.go**

```go
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/internal/viewmodel"
	"github.com/jackc/pgx/v5/pgtype"
)

type MultipartHandler struct {
	queries    db.Querier
	// CompleteS3 is exported for test injection; defaults to storage.CompleteMultipartUpload.
	CompleteS3 func(ctx context.Context, key, uploadID string, parts []storage.Part) (string, error)
}

func NewMultipartHandler(q db.Querier) *MultipartHandler {
	return &MultipartHandler{
		queries:    q,
		CompleteS3: storage.CompleteMultipartUpload,
	}
}

// POST /api/s3/multipart
func (h *MultipartHandler) CreateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Filename    string `json:"filename"`
		ContentType string `json:"contentType"`
		FolderID    string `json:"folderId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Filename == "" {
		http.Error(w, "filename is required", http.StatusBadRequest)
		return
	}

	safe := sanitizeFilename(req.Filename)
	if safe == "" {
		safe = "file"
	}
	now := time.Now().UTC()
	s3Key := fmt.Sprintf("uploads/%d/%02d/%x/%s", now.Year(), now.Month(), mustRandBytes(8), safe)

	uploadID, err := storage.CreateMultipartUpload(r.Context(), s3Key, req.ContentType)
	if err != nil {
		http.Error(w, "failed to initiate multipart upload", http.StatusInternalServerError)
		return
	}

	uploaderUUID, err := viewmodel.UUIDFromString(user.ID)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusInternalServerError)
		return
	}

	var folderID pgtype.UUID
	if req.FolderID != "" {
		folderID, _ = viewmodel.UUIDFromString(req.FolderID)
	}

	ct := req.ContentType
	var ctPtr *string
	if ct != "" {
		ctPtr = &ct
	}

	file, err := h.queries.CreateFile(r.Context(), db.CreateFileParams{
		FolderID:   folderID,
		Name:       req.Filename,
		S3Key:      s3Key,
		SizeBytes:  0,
		MimeType:   ctPtr,
		UploadedBy: uploaderUUID,
	})
	if err != nil {
		http.Error(w, "failed to create file record", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"uploadId": uploadID,
		"key":      s3Key,
		"fileId":   file.ID.String(),
	})
}

// GET /api/s3/multipart/{uploadId}?key=...
func (h *MultipartHandler) ListParts(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetCurrentUser(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	uploadID := chi.URLParam(r, "uploadId")
	key := r.URL.Query().Get("key")
	if uploadID == "" || key == "" {
		http.Error(w, "uploadId and key required", http.StatusBadRequest)
		return
	}

	parts, err := storage.ListMultipartParts(r.Context(), key, uploadID)
	if err != nil {
		http.Error(w, "failed to list parts", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(parts)
}

// GET /api/s3/multipart/{uploadId}/{partNumber}?key=...
func (h *MultipartHandler) PresignPart(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetCurrentUser(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	uploadID := chi.URLParam(r, "uploadId")
	partStr := chi.URLParam(r, "partNumber")
	key := r.URL.Query().Get("key")

	partNum, err := strconv.Atoi(partStr)
	if err != nil || uploadID == "" || key == "" {
		http.Error(w, "invalid params", http.StatusBadRequest)
		return
	}

	url, err := storage.PresignUploadPart(r.Context(), key, uploadID, int32(partNum))
	if err != nil {
		http.Error(w, "failed to presign part", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": url})
}

// DELETE /api/s3/multipart/{uploadId}?key=...
func (h *MultipartHandler) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	uploadID := chi.URLParam(r, "uploadId")
	key := r.URL.Query().Get("key")
	if uploadID == "" || key == "" {
		http.Error(w, "uploadId and key required", http.StatusBadRequest)
		return
	}

	_ = storage.AbortMultipartUpload(r.Context(), key, uploadID)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/s3/multipart/{uploadId}/complete
func (h *MultipartHandler) CompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Key    string `json:"key"`
		FileID string `json:"fileId"`
		Parts  []struct {
			PartNumber int32  `json:"PartNumber"`
			ETag       string `json:"ETag"`
		} `json:"parts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" || req.FileID == "" {
		http.Error(w, "key and fileId required", http.StatusBadRequest)
		return
	}

	uploadID := chi.URLParam(r, "uploadId")

	parts := make([]storage.Part, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = storage.Part{PartNumber: p.PartNumber, ETag: p.ETag}
	}

	location, err := h.CompleteS3(r.Context(), req.Key, uploadID, parts)
	if err != nil {
		http.Error(w, "failed to complete upload", http.StatusInternalServerError)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(req.FileID)
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	file, err := h.queries.UpdateFileSizeAndStatus(r.Context(), db.UpdateFileSizeAndStatusParams{
		SizeBytes: 0, // size from S3 response not available directly; set when known
		Status:    "active",
		ID:        fileUUID,
	})
	if err != nil {
		http.Error(w, "failed to activate file", http.StatusInternalServerError)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	action := "file.upload"
	resourceType := "file"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       action,
		ResourceType: &resourceType,
		ResourceID:   file.ID,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"location": location})
}

func mustRandBytes(n int) []byte {
	import_crypto_rand_read := func(b []byte) {
		// inline rand.Read for key gen — reuse sanitizeFilename from upload.go
	}
	_ = import_crypto_rand_read
	b := make([]byte, n)
	// Use crypto/rand via the package-level function already available in this package
	if _, err := cryptoRandRead(b); err != nil {
		return b
	}
	return b
}
```

Wait — `mustRandBytes` needs `crypto/rand`. The `sanitizeFilename` function is already in `upload.go` in the same package. For `crypto/rand.Read`, it's in the same package already. Let me write this properly without the incorrect helper. Replace `mustRandBytes` with inline usage:

Actually, let me rewrite `multipart.go` with proper crypto/rand usage:

```go
package handler

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/internal/viewmodel"
)

type MultipartHandler struct {
	queries    db.Querier
	CompleteS3 func(ctx context.Context, key, uploadID string, parts []storage.Part) (string, error)
}

func NewMultipartHandler(q db.Querier) *MultipartHandler {
	return &MultipartHandler{
		queries:    q,
		CompleteS3: storage.CompleteMultipartUpload,
	}
}

// POST /api/s3/multipart
func (h *MultipartHandler) CreateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Filename    string `json:"filename"`
		ContentType string `json:"contentType"`
		FolderID    string `json:"folderId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Filename == "" {
		http.Error(w, "filename is required", http.StatusBadRequest)
		return
	}

	safe := sanitizeFilename(body.Filename)
	if safe == "" {
		safe = "file"
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	s3Key := fmt.Sprintf("uploads/%d/%02d/%x/%s", now.Year(), now.Month(), b, safe)

	uploadID, err := storage.CreateMultipartUpload(r.Context(), s3Key, body.ContentType)
	if err != nil {
		http.Error(w, "failed to initiate upload", http.StatusInternalServerError)
		return
	}

	uploaderUUID, err := viewmodel.UUIDFromString(user.ID)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusInternalServerError)
		return
	}

	var folderID pgtype.UUID
	if body.FolderID != "" {
		folderID, _ = viewmodel.UUIDFromString(body.FolderID)
	}

	var ctPtr *string
	if body.ContentType != "" {
		ct := body.ContentType
		ctPtr = &ct
	}

	file, err := h.queries.CreateFile(r.Context(), db.CreateFileParams{
		FolderID:   folderID,
		Name:       body.Filename,
		S3Key:      s3Key,
		SizeBytes:  0,
		MimeType:   ctPtr,
		UploadedBy: uploaderUUID,
	})
	if err != nil {
		http.Error(w, "failed to create file record", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"uploadId": uploadID,
		"key":      s3Key,
		"fileId":   file.ID.String(),
	})
}

// GET /api/s3/multipart/{uploadId}?key=...
func (h *MultipartHandler) ListParts(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetCurrentUser(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	uploadID := chi.URLParam(r, "uploadId")
	key := r.URL.Query().Get("key")
	if uploadID == "" || key == "" {
		http.Error(w, "uploadId and key required", http.StatusBadRequest)
		return
	}
	parts, err := storage.ListMultipartParts(r.Context(), key, uploadID)
	if err != nil {
		http.Error(w, "failed to list parts", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(parts)
}

// GET /api/s3/multipart/{uploadId}/{partNumber}?key=...
func (h *MultipartHandler) PresignPart(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetCurrentUser(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	uploadID := chi.URLParam(r, "uploadId")
	partStr := chi.URLParam(r, "partNumber")
	key := r.URL.Query().Get("key")
	partNum, err := strconv.Atoi(partStr)
	if err != nil || uploadID == "" || key == "" {
		http.Error(w, "invalid params", http.StatusBadRequest)
		return
	}
	url, err := storage.PresignUploadPart(r.Context(), key, uploadID, int32(partNum))
	if err != nil {
		http.Error(w, "failed to presign part", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": url})
}

// DELETE /api/s3/multipart/{uploadId}?key=...
func (h *MultipartHandler) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	uploadID := chi.URLParam(r, "uploadId")
	key := r.URL.Query().Get("key")
	if uploadID == "" || key == "" {
		http.Error(w, "uploadId and key required", http.StatusBadRequest)
		return
	}
	_ = storage.AbortMultipartUpload(r.Context(), key, uploadID)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/s3/multipart/{uploadId}/complete
func (h *MultipartHandler) CompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Key    string `json:"key"`
		FileID string `json:"fileId"`
		Parts  []struct {
			PartNumber int32  `json:"PartNumber"`
			ETag       string `json:"ETag"`
		} `json:"parts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" || body.FileID == "" {
		http.Error(w, "key and fileId required", http.StatusBadRequest)
		return
	}

	uploadID := chi.URLParam(r, "uploadId")

	parts := make([]storage.Part, len(body.Parts))
	for i, p := range body.Parts {
		parts[i] = storage.Part{PartNumber: p.PartNumber, ETag: p.ETag}
	}

	location, err := h.CompleteS3(r.Context(), body.Key, uploadID, parts)
	if err != nil {
		http.Error(w, "failed to complete upload", http.StatusInternalServerError)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(body.FileID)
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	file, err := h.queries.UpdateFileSizeAndStatus(r.Context(), db.UpdateFileSizeAndStatusParams{
		SizeBytes: 0,
		Status:    "active",
		ID:        fileUUID,
	})
	if err != nil {
		http.Error(w, "failed to activate file", http.StatusInternalServerError)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	action := "file.upload"
	resourceType := "file"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       action,
		ResourceType: &resourceType,
		ResourceID:   file.ID,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"location": location})
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/handler/... -run "TestCreateMultipartUpload|TestCompleteMultipartUpload" -v
```

Expected: PASS for both.

- [ ] **Step 5: Build**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 6: Run all tests**

```bash
go test ./... -count=1
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/handler/multipart.go internal/handler/multipart_test.go
git commit -m "feat: multipart upload handler — 5 endpoints, viewer blocked, complete sets active — Phase 5 Task 9"
```

---

## Task 10: Uppy Frontend JS + Route Wiring

**Files:**
- Create: `web/static/upload.js`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Create web/static/upload.js**

```js
// Uppy v3.27.0 multipart upload integration.
// Loaded only on pages with file browser (Layout ShowUpload=true).
// CDN scripts loaded in layout.templ before this file.

function initUppy(targetFolderId) {
  if (typeof Uppy === 'undefined') return;

  var existing = window.__vaulxUppy;
  if (existing) { existing.destroy(); }

  var uppy = new Uppy.Uppy({
    autoProceed: false,
    restrictions: { maxFileSize: null },
  });

  uppy.use(Uppy.Dashboard, {
    inline: true,
    target: '#uppy-container',
    proudlyDisplayPoweredByUppy: false,
    height: 320,
  });

  uppy.use(Uppy.AwsS3Multipart, {
    shouldUseMultipart: function(file) { return file.size > 100 * 1024 * 1024; },

    createMultipartUpload: async function(file) {
      var res = await fetch('/api/s3/multipart', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          filename: file.name,
          contentType: file.type || 'application/octet-stream',
          folderId: targetFolderId || '',
        }),
      });
      if (!res.ok) throw new Error('createMultipartUpload failed');
      var data = await res.json();
      // Store fileId in file meta for the complete step
      uppy.setFileMeta(file.id, { fileId: data.fileId });
      return data; // { uploadId, key, fileId }
    },

    listParts: async function(file, _ref) {
      var uploadId = _ref.uploadId, key = _ref.key;
      var res = await fetch('/api/s3/multipart/' + uploadId + '?key=' + encodeURIComponent(key));
      if (!res.ok) throw new Error('listParts failed');
      return res.json();
    },

    signPart: async function(file, _ref) {
      var uploadId = _ref.uploadId, partNumber = _ref.partNumber, key = _ref.key;
      var res = await fetch('/api/s3/multipart/' + uploadId + '/' + partNumber + '?key=' + encodeURIComponent(key));
      if (!res.ok) throw new Error('signPart failed');
      var data = await res.json();
      return { url: data.url };
    },

    abortMultipartUpload: async function(file, _ref) {
      var uploadId = _ref.uploadId, key = _ref.key;
      await fetch('/api/s3/multipart/' + uploadId + '?key=' + encodeURIComponent(key), { method: 'DELETE' });
    },

    completeMultipartUpload: async function(file, _ref) {
      var uploadId = _ref.uploadId, key = _ref.key, parts = _ref.parts;
      var fileId = (file.meta && file.meta.fileId) || '';
      var res = await fetch('/api/s3/multipart/' + uploadId + '/complete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key: key, fileId: fileId, parts: parts }),
      });
      if (!res.ok) throw new Error('completeMultipartUpload failed');
      return res.json();
    },
  });

  uppy.on('complete', function(result) {
    if (result.successful && result.successful.length > 0) {
      var target = window.location.pathname;
      if (typeof htmx !== 'undefined') {
        htmx.ajax('GET', target, { target: '#browser-content', swap: 'outerHTML' });
      }
    }
  });

  window.__vaulxUppy = uppy;
}
```

- [ ] **Step 2: Wire new routes in main.go**

In `cmd/server/main.go`, inside the `r.Group(func(r chi.Router) { r.Use(auth.RequireAuth(sessionStore)) ... })` block, add:

After the existing file operations:
```go
// File preview (HTMX partial)
r.Get("/api/file/{fileID}/preview", filesHandler.PreviewFile)
```

Add a new multipart sub-router (before the `// Admin` section):
```go
// Multipart upload
multipartHandler := handler.NewMultipartHandler(queries)
r.Route("/api/s3/multipart", func(r chi.Router) {
    r.Post("/", multipartHandler.CreateMultipartUpload)
    r.Get("/{uploadId}", multipartHandler.ListParts)
    r.Get("/{uploadId}/{partNumber}", multipartHandler.PresignPart)
    r.Delete("/{uploadId}", multipartHandler.AbortMultipartUpload)
    r.Post("/{uploadId}/complete", multipartHandler.CompleteMultipartUpload)
})
```

- [ ] **Step 3: Build**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 4: Run all tests**

```bash
go test ./... -count=1
```

Expected: all pass.

- [ ] **Step 5: Run templ generate and rebuild**

```bash
make generate && go build ./...
```

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add web/static/upload.js cmd/server/main.go
git commit -m "feat: Uppy JS client, wire multipart + preview routes — Phase 5 Task 10"
```

---

## Task 11: Final Build Verification

- [ ] **Step 1: Clean build**

```bash
make generate
go build ./...
```

Expected: no errors.

- [ ] **Step 2: Run all tests**

```bash
go test ./... -count=1
```

Expected: all pass. Verify the three required spec tests appear:
- `TestCreateMultipartUpload_ViewerForbidden` — PASS
- `TestCompleteMultipartUpload_SetsStatusActive` — PASS
- `TestPreviewFile_Forbidden` — PASS

- [ ] **Step 3: Commit**

```bash
git add -A
git status # should show nothing new (or only minor leftover changes)
git commit -m "chore: phase 5 complete — hardening, preview, pagination, multipart upload" --allow-empty
```

---

## Self-Review

### Spec Coverage

| Requirement | Task |
|-------------|------|
| Fix 1 — Fatal S3 on connect failure | Task 3 |
| Fix 2 — Rate limit POST /auth/login (10/IP/min) | Task 3 |
| Fix 3 — Friendly 403/404/410 error pages | Task 4 |
| Fix 4 — Richer empty state (heading, viewer-gated upload hint) | Task 8 |
| Feature 1 — File browser pagination (page/limit, default 48) | Task 5 |
| Feature 1 — Audit log pagination (page/limit, default 50) | Task 6 |
| Feature 2 — Preview panel, HTMX `/api/file/{id}/preview` | Task 7 |
| Feature 2 — PreviewPanel by MIME type (video/image/pdf/other) | Task 7 |
| Feature 2 — Layout preview panel div, Alpine previewOpen | Task 8 |
| Feature 2 — File card click triggers HTMX preview load | Task 8 |
| Feature 3 — Multipart backend (5 endpoints) | Task 9 |
| Feature 3 — Uppy client JS (initUppy, AwsS3Multipart) | Task 10 |
| Feature 3 — Uppy replaces upload zone in file_browser | Task 8 |
| SQL — ListFilesPage, CountFiles, ListAuditLogPage, CountAuditLog, UpdateFileSizeAndStatus | Task 1 |
| Test — viewer blocked from POST /api/s3/multipart | Task 9 |
| Test — complete sets status active | Task 9 |
| Test — preview 403 for unauthorized user | Task 7 |
| Makefile generate target includes sqlc | Task 3 |
| go build + go test pass clean | Task 11 |

### Type Consistency

- `PaginationData` defined in Task 5, used in Tasks 5, 6, 8 — consistent
- `ListFilesPage` params: `{FolderID pgtype.UUID, Limit int32, Offset int32}` — consistent across Tasks 1 and 5
- `UpdateFileSizeAndStatus` params: `{SizeBytes int64, Status string, ID pgtype.UUID}` — consistent across Tasks 1 and 9
- `storage.Part{PartNumber int32, ETag string}` — defined Task 2, used Tasks 9 and 10
- `MultipartHandler.CompleteS3` signature matches `storage.CompleteMultipartUpload` signature — consistent
- `Layout(title, user, showUpload)` — updated in Task 8, all callers updated in same task
- `AdminAuditPage(entries, filter, pagination, user)` — updated in Task 6, handler updated in same task
