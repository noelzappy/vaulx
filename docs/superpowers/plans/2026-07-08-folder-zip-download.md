# Folder Zip Download Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Download an entire folder (recursive, all active files, pagination-independent) as a zip — instantly via streaming, or via a background "prepared zip" job with a resumable presigned download. Dashboard and shared-folder pages.

**Architecture:** A `zipbuild` package assembles store-only zips from a list of entries plus a fetch callback. The streaming handler writes the zip straight to the HTTP response; the background worker writes the same zip into the bucket (`zips/<folderID>/<jobID>.zip`) and hands out presigned GETs. Jobs live in a `zip_jobs` table; an in-process worker pool (cap 2) claims `pending` jobs with `FOR UPDATE SKIP LOCKED`.

**Tech Stack:** Go, chi, sqlc (pgx/v5), templ + htmx, aws-sdk-go-v2 (+ `feature/s3/manager` for streamed upload), archive/zip.

**Spec:** `docs/superpowers/specs/2026-07-08-folder-zip-download-design.md`

## Global Constraints

- Store method only (`zip.Store`) — never compress.
- Only `status = 'active'` files are included.
- Dashboard zips include only files where `auth.CanAccess(user, uploadedBy)` — same rule as single-file `Download`.
- Share zip routes revalidate: slug exists, `folder_id` set, not expired, `?folder=` inside tree via `folderInTree` (mirrors `SharedFileDownload`).
- Prepared zips expire 24h after ready; global running-job cap = 2; share prepare rate limit = 5/min per IP.
- Commit messages follow repo style: `feat:`, `fix:`, `chore:` prefixes, lowercase summary.
- After changing `.sql` or `.templ` files run `make generate` and commit the generated `*.sql.go` / `*_templ.go` alongside.

---

### Task 1: Schema, queries, generated code

**Files:**
- Create: `migrations/003_zip_jobs.up.sql`, `migrations/003_zip_jobs.down.sql`
- Create: `internal/db/queries/zipjobs.sql`
- Modify: `internal/db/queries/files.sql` (append tree queries)
- Generated: `internal/db/*` via `make generate`

**Interfaces (produces):**
- `db.ZipJob` model; `db.Querier` gains: `CreateZipJob`, `GetZipJob`, `GetReusableZipJob`, `ClaimNextPendingZipJob`, `MarkZipJobReady`, `MarkZipJobFailed`, `FailStaleRunningZipJobs`, `ListExpiredReadyZipJobs`, `MarkZipJobExpired`, `ListFolderTreeFolders`, `ListFolderTreeFiles`.
- `db.ListFolderTreeFilesRow{ID, Name, S3Key, SizeBytes, UploadedBy, CreatedAt, Relpath}` and `db.ListFolderTreeFoldersRow{ID, Relpath}`.

- [ ] **Step 1: Write migration**

`migrations/003_zip_jobs.up.sql`:
```sql
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
```

`migrations/003_zip_jobs.down.sql`:
```sql
DROP TABLE zip_jobs;
```

- [ ] **Step 2: Write zip job queries**

`internal/db/queries/zipjobs.sql`:
```sql
-- name: CreateZipJob :one
INSERT INTO zip_jobs (folder_id, share_id, file_count, content_bytes)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetZipJob :one
SELECT * FROM zip_jobs WHERE id = $1;

-- name: GetReusableZipJob :one
SELECT * FROM zip_jobs
WHERE folder_id = $1
  AND (
    status IN ('pending', 'running')
    OR (
      status = 'ready'
      AND expires_at > NOW()
      AND file_count = $2
      AND content_bytes = $3
      AND created_at > $4
    )
  )
ORDER BY created_at DESC
LIMIT 1;

-- name: ClaimNextPendingZipJob :one
UPDATE zip_jobs SET status = 'running'
WHERE id = (
  SELECT id FROM zip_jobs
  WHERE status = 'pending'
  ORDER BY created_at
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: MarkZipJobReady :exec
UPDATE zip_jobs
SET status = 'ready', s3_key = $2, size_bytes = $3, expires_at = NOW() + INTERVAL '24 hours'
WHERE id = $1;

-- name: MarkZipJobFailed :exec
UPDATE zip_jobs SET status = 'failed', error = $2 WHERE id = $1;

-- name: FailStaleRunningZipJobs :exec
UPDATE zip_jobs SET status = 'failed', error = 'interrupted by server restart'
WHERE status = 'running';

-- name: ListExpiredReadyZipJobs :many
SELECT * FROM zip_jobs WHERE status = 'ready' AND expires_at < NOW();

-- name: MarkZipJobExpired :exec
UPDATE zip_jobs SET status = 'failed', error = 'expired' WHERE id = $1;
```

- [ ] **Step 3: Append tree queries to `internal/db/queries/files.sql`**

```sql
-- name: ListFolderTreeFolders :many
WITH RECURSIVE tree AS (
  SELECT id, parent_id, ''::text AS relpath
  FROM folders WHERE id = $1
  UNION ALL
  SELECT f.id, f.parent_id,
         CASE WHEN t.relpath = '' THEN f.name ELSE t.relpath || '/' || f.name END
  FROM folders f JOIN tree t ON f.parent_id = t.id
)
SELECT id, relpath FROM tree ORDER BY relpath;

-- name: ListFolderTreeFiles :many
WITH RECURSIVE tree AS (
  SELECT id, ''::text AS relpath
  FROM folders WHERE id = $1
  UNION ALL
  SELECT f.id,
         CASE WHEN t.relpath = '' THEN f.name ELSE t.relpath || '/' || f.name END
  FROM folders f JOIN tree t ON f.parent_id = t.id
)
SELECT fi.id, fi.name, fi.s3_key, fi.size_bytes, fi.uploaded_by, fi.created_at, t.relpath
FROM files fi
JOIN tree t ON fi.folder_id = t.id
WHERE fi.status = 'active'
ORDER BY t.relpath, fi.name;
```

- [ ] **Step 4: Generate and build**

Run: `make generate && go build ./...`
Expected: compiles; `internal/db/zipjobs.sql.go` exists; `db.Querier` interface includes the new methods.

- [ ] **Step 5: Verify migration applies**

Run: `docker run -d --name vaulx-zip-pg -e POSTGRES_USER=vaulx -e POSTGRES_PASSWORD=vaulx -e POSTGRES_DB=vaulx -p 5433:5432 postgres:16-alpine && sleep 3 && DATABASE_URL="postgres://vaulx:vaulx@localhost:5433/vaulx?sslmode=disable" PORT=8090 timeout 10 go run ./cmd/server; docker rm -f vaulx-zip-pg`
Expected: log line `migrations: applied` before the timeout kills it.

- [ ] **Step 6: Commit**

```bash
git add migrations/003_zip_jobs.* internal/db/
git commit -m "feat: add zip_jobs table and folder tree queries"
```

---

### Task 2: zipbuild package

**Files:**
- Create: `internal/zipbuild/zipbuild.go`
- Test: `internal/zipbuild/zipbuild_test.go`

**Interfaces (produces):**
```go
package zipbuild

type Entry struct {
    Path  string // relative path inside the zip, "/"-separated, no leading slash
    S3Key string // object to fetch; empty for directory entries
    IsDir bool
}

type FetchFunc func(ctx context.Context, s3Key string) (io.ReadCloser, error)

// Build writes a store-only (uncompressed) zip of entries to w.
// Duplicate file paths get " (2)", " (3)"… suffixes before the extension.
// Directory entries create empty folders. Stops at the first fetch/copy error.
func Build(ctx context.Context, w io.Writer, entries []Entry, fetch FetchFunc) error
```

- [ ] **Step 1: Write failing tests**

`internal/zipbuild/zipbuild_test.go`:
```go
package zipbuild_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/noelzappy/vaulx/internal/zipbuild"
)

func fakeFetch(objects map[string]string) zipbuild.FetchFunc {
	return func(_ context.Context, key string) (io.ReadCloser, error) {
		body, ok := objects[key]
		if !ok {
			return nil, errors.New("no such key: " + key)
		}
		return io.NopCloser(strings.NewReader(body)), nil
	}
}

func readZip(t *testing.T, buf *bytes.Buffer) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("invalid zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestBuild_TreePathsAndContent(t *testing.T) {
	var buf bytes.Buffer
	entries := []zipbuild.Entry{
		{Path: "a.txt", S3Key: "k1"},
		{Path: "sub/b.txt", S3Key: "k2"},
	}
	err := zipbuild.Build(context.Background(), &buf, entries, fakeFetch(map[string]string{"k1": "one", "k2": "two"}))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := readZip(t, &buf)
	if got["a.txt"] != "one" || got["sub/b.txt"] != "two" {
		t.Errorf("unexpected zip contents: %#v", got)
	}
}

func TestBuild_StoreMethodOnly(t *testing.T) {
	var buf bytes.Buffer
	_ = zipbuild.Build(context.Background(), &buf,
		[]zipbuild.Entry{{Path: "a.txt", S3Key: "k1"}},
		fakeFetch(map[string]string{"k1": "payload"}))
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("invalid zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Errorf("%s: method = %d, want zip.Store", f.Name, f.Method)
		}
	}
}

func TestBuild_DedupesDuplicateNames(t *testing.T) {
	var buf bytes.Buffer
	entries := []zipbuild.Entry{
		{Path: "report.pdf", S3Key: "k1"},
		{Path: "report.pdf", S3Key: "k2"},
		{Path: "report.pdf", S3Key: "k3"},
	}
	err := zipbuild.Build(context.Background(), &buf, entries,
		fakeFetch(map[string]string{"k1": "1", "k2": "2", "k3": "3"}))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := readZip(t, &buf)
	for _, name := range []string{"report.pdf", "report (2).pdf", "report (3).pdf"} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing entry %q; have %#v", name, got)
		}
	}
}

func TestBuild_EmptyDirectoryEntries(t *testing.T) {
	var buf bytes.Buffer
	entries := []zipbuild.Entry{{Path: "empty-folder", IsDir: true}}
	if err := zipbuild.Build(context.Background(), &buf, entries, fakeFetch(nil)); err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := readZip(t, &buf)
	if _, ok := got["empty-folder/"]; !ok {
		t.Errorf("missing directory entry, have %#v", got)
	}
}

func TestBuild_FetchErrorStops(t *testing.T) {
	var buf bytes.Buffer
	entries := []zipbuild.Entry{{Path: "a.txt", S3Key: "missing"}}
	if err := zipbuild.Build(context.Background(), &buf, entries, fakeFetch(nil)); err == nil {
		t.Fatal("expected error for missing object")
	}
}
```

- [ ] **Step 2: Run tests, verify failure**

Run: `go test ./internal/zipbuild/`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement**

`internal/zipbuild/zipbuild.go`:
```go
// Package zipbuild assembles store-only zip archives from object storage
// entries. It is shared by the streaming download handler and the
// prepared-zip background worker.
package zipbuild

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"
)

type Entry struct {
	Path  string // relative path inside the zip, "/"-separated, no leading slash
	S3Key string // object to fetch; empty for directory entries
	IsDir bool
}

type FetchFunc func(ctx context.Context, s3Key string) (io.ReadCloser, error)

// Build writes a store-only zip of entries to w. Duplicate file paths get
// " (2)"-style suffixes before the extension. Stops at the first error so
// callers never emit an archive that silently misses entries.
func Build(ctx context.Context, w io.Writer, entries []Entry, fetch FetchFunc) error {
	zw := zip.NewWriter(w)
	seen := map[string]bool{}

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.IsDir {
			name := strings.TrimSuffix(e.Path, "/") + "/"
			if seen[name] {
				continue
			}
			seen[name] = true
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store}); err != nil {
				return fmt.Errorf("zipbuild: dir %s: %w", name, err)
			}
			continue
		}

		name := dedupe(e.Path, seen)
		seen[name] = true
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return fmt.Errorf("zipbuild: entry %s: %w", name, err)
		}
		rc, err := fetch(ctx, e.S3Key)
		if err != nil {
			return fmt.Errorf("zipbuild: fetch %s: %w", e.S3Key, err)
		}
		_, err = io.Copy(fw, rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("zipbuild: copy %s: %w", e.S3Key, err)
		}
	}
	return zw.Close()
}

// dedupe returns p, or "base (n).ext" for the first n ≥ 2 not yet taken.
func dedupe(p string, seen map[string]bool) string {
	if !seen[p] {
		return p
	}
	dir, file := path.Split(p)
	ext := path.Ext(file)
	base := strings.TrimSuffix(file, ext)
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s%s (%d)%s", dir, base, n, ext)
		if !seen[cand] {
			return cand
		}
	}
}
```

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/zipbuild/`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/zipbuild/
git commit -m "feat: add zipbuild package for store-only zip assembly"
```

---

### Task 3: Storage streaming helpers

**Files:**
- Create: `internal/storage/stream.go`
- Modify: `go.mod` (add `github.com/aws/aws-sdk-go-v2/feature/s3/manager`)

**Interfaces (produces):**
```go
// GetObjectStream returns the object body for streaming; caller closes it.
func GetObjectStream(ctx context.Context, key string) (io.ReadCloser, error)

// UploadStream uploads r to key via managed multipart upload and returns
// the number of bytes written.
func UploadStream(ctx context.Context, key, contentType string, r io.Reader) (int64, error)
```

- [ ] **Step 1: Add dependency**

Run: `go get github.com/aws/aws-sdk-go-v2/feature/s3/manager && go mod tidy`
Expected: go.mod gains the manager module.

- [ ] **Step 2: Implement**

`internal/storage/stream.go`:
```go
package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// GetObjectStream returns the object body for streaming; caller closes it.
func GetObjectStream(ctx context.Context, key string) (io.ReadCloser, error) {
	if Client == nil {
		return nil, fmt.Errorf("storage: not connected")
	}
	out, err := Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("storage: get %s: %w", key, err)
	}
	return out.Body, nil
}

// countingReader counts bytes as they pass through.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// UploadStream uploads r to key via managed multipart upload (constant
// memory) and returns the number of bytes written.
func UploadStream(ctx context.Context, key, contentType string, r io.Reader) (int64, error) {
	if Client == nil {
		return 0, fmt.Errorf("storage: not connected")
	}
	cr := &countingReader{r: r}
	uploader := manager.NewUploader(Client)
	_, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(Bucket),
		Key:         aws.String(key),
		Body:        cr,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return 0, fmt.Errorf("storage: upload stream %s: %w", key, err)
	}
	return cr.n, nil
}
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 4: Commit**

```bash
git add internal/storage/stream.go go.mod go.sum
git commit -m "feat: add storage streaming get/upload helpers"
```

---

### Task 4: Streaming zip handlers + routes

**Files:**
- Create: `internal/handler/zip.go`
- Test: `internal/handler/zip_test.go`
- Modify: `cmd/server/main.go` (routes)

**Interfaces:**
- Consumes: `db.Querier` tree queries (Task 1), `zipbuild.Build` (Task 2), `storage.GetObjectStream` (Task 3), existing `auth.CanAccess`, `sanitizeFilename` (upload.go), `ShareHandler.folderInTree` (same package).
- Produces:
```go
type ZipHandler struct {
    queries db.Querier
    shares  *ShareHandler     // for folderInTree
    Fetch   zipbuild.FetchFunc // test seam; defaults to storage.GetObjectStream
}
func NewZipHandler(q db.Querier, sh *ShareHandler) *ZipHandler
func (h *ZipHandler) StreamZip(w http.ResponseWriter, r *http.Request)       // GET /files/{folderID}/zip
func (h *ZipHandler) SharedStreamZip(w http.ResponseWriter, r *http.Request) // GET /s/{slug}/zip?folder=
// internal, reused by Task 6:
// collectEntries(ctx, folderID, filter func(uploadedBy pgtype.UUID) bool) ([]zipbuild.Entry, treeStats, error)
// type treeStats struct { FileCount int32; ContentBytes int64; NewestFile pgtype.Timestamptz }
// resolveShareForZip(r *http.Request) (db.Share, pgtype.UUID, int) — share, target folder, HTTP error code (0 = ok)
```

- [ ] **Step 1: Write failing tests**

`internal/handler/zip_test.go`:
```go
package handler_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/handler"
)

func uuid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

// zipQuerier serves a two-level tree: root(0x0A) with sub(0x0B);
// f1.txt owned by u1 in root, f2.txt owned by u2 in sub.
type zipQuerier struct {
	db.Querier
	audited []string
}

func (m *zipQuerier) GetFolder(ctx context.Context, id pgtype.UUID) (db.Folder, error) {
	if id == uuid(0x0B) {
		return db.Folder{ID: id, Name: "sub", ParentID: uuid(0x0A)}, nil
	}
	return db.Folder{ID: id, Name: "root"}, nil
}

func (m *zipQuerier) ListFolderTreeFolders(ctx context.Context, id pgtype.UUID) ([]db.ListFolderTreeFoldersRow, error) {
	return []db.ListFolderTreeFoldersRow{
		{ID: uuid(0x0A), Relpath: ""},
		{ID: uuid(0x0B), Relpath: "sub"},
	}, nil
}

func (m *zipQuerier) ListFolderTreeFiles(ctx context.Context, id pgtype.UUID) ([]db.ListFolderTreeFilesRow, error) {
	return []db.ListFolderTreeFilesRow{
		{ID: uuid(1), Name: "f1.txt", S3Key: "k1", SizeBytes: 3, UploadedBy: uuid(0xA1), Relpath: ""},
		{ID: uuid(2), Name: "f2.txt", S3Key: "k2", SizeBytes: 3, UploadedBy: uuid(0xA2), Relpath: "sub"},
	}, nil
}

func (m *zipQuerier) CreateAuditLog(ctx context.Context, arg db.CreateAuditLogParams) (db.AuditLog, error) {
	m.audited = append(m.audited, arg.Action)
	return db.AuditLog{}, nil
}

func fetchFrom(objects map[string]string) func(context.Context, string) (io.ReadCloser, error) {
	return func(_ context.Context, key string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(objects[key])), nil
	}
}

func streamZipRequest(t *testing.T, q db.Querier, user auth.UserContext) *httptest.ResponseRecorder {
	t.Helper()
	h := handler.NewZipHandler(q, handler.NewShareHandler(q))
	h.Fetch = fetchFrom(map[string]string{"k1": "one", "k2": "two"})

	req := httptest.NewRequest(http.MethodGet, "/files/0a000000-0000-0000-0000-000000000000/zip", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("folderID", "0a000000-0000-0000-0000-000000000000")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.SetCurrentUser(req.Context(), user))
	rr := httptest.NewRecorder()
	h.StreamZip(rr, req)
	return rr
}

func entryNames(t *testing.T, body *bytes.Buffer) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body.Bytes()), int64(body.Len()))
	if err != nil {
		t.Fatalf("response is not a zip: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

func TestStreamZip_AdminGetsFullTree(t *testing.T) {
	q := &zipQuerier{}
	rr := streamZipRequest(t, q, auth.UserContext{ID: "admin", Role: "admin"})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="root.zip"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	names := entryNames(t, rr.Body)
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "f1.txt") || !strings.Contains(joined, "sub/f2.txt") {
		t.Errorf("entries = %v", names)
	}
	if len(q.audited) == 0 || q.audited[0] != "folder.zip_download" {
		t.Errorf("audit log = %v", q.audited)
	}
}

func TestStreamZip_EditorOnlyOwnFiles(t *testing.T) {
	// editor a1... owns only f1 (uploadedBy 0xA1)
	ownerID := pgtype.UUID{Bytes: [16]byte{0xA1}, Valid: true}.String()
	rr := streamZipRequest(t, &zipQuerier{}, auth.UserContext{ID: ownerID, Role: "editor"})
	names := strings.Join(entryNames(t, rr.Body), ",")
	if !strings.Contains(names, "f1.txt") {
		t.Errorf("expected own file present, entries = %v", names)
	}
	if strings.Contains(names, "f2.txt") {
		t.Errorf("expected other user's file excluded, entries = %v", names)
	}
}

func TestStreamZip_Unauthenticated(t *testing.T) {
	h := handler.NewZipHandler(&zipQuerier{}, handler.NewShareHandler(&zipQuerier{}))
	req := httptest.NewRequest(http.MethodGet, "/files/x/zip", nil)
	rr := httptest.NewRecorder()
	h.StreamZip(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// shareZipQuerier wraps zipQuerier with a folder share for root 0x0A.
type shareZipQuerier struct {
	zipQuerier
	expired bool
}

func (m *shareZipQuerier) GetShareBySlug(ctx context.Context, slug string) (db.Share, error) {
	sh := db.Share{ID: uuid(0x51), Slug: slug, FolderID: uuid(0x0A)}
	if m.expired {
		sh.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	}
	return sh, nil
}

func sharedZipRequest(t *testing.T, q db.Querier, folderParam string) *httptest.ResponseRecorder {
	t.Helper()
	h := handler.NewZipHandler(q, handler.NewShareHandler(q))
	h.Fetch = fetchFrom(map[string]string{"k1": "one", "k2": "two"})
	url := "/s/abc/zip"
	if folderParam != "" {
		url += "?folder=" + folderParam
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", "abc")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.SharedStreamZip(rr, req)
	return rr
}

func TestSharedStreamZip_FullTreeNoOwnerFilter(t *testing.T) {
	rr := sharedZipRequest(t, &shareZipQuerier{}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	names := strings.Join(entryNames(t, rr.Body), ",")
	if !strings.Contains(names, "f1.txt") || !strings.Contains(names, "sub/f2.txt") {
		t.Errorf("entries = %v", names)
	}
}

func TestSharedStreamZip_SubfolderInsideTree(t *testing.T) {
	rr := sharedZipRequest(t, &shareZipQuerier{}, "0b000000-0000-0000-0000-000000000000")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestSharedStreamZip_ExpiredShareGone(t *testing.T) {
	rr := sharedZipRequest(t, &shareZipQuerier{expired: true}, "")
	if rr.Code != http.StatusGone {
		t.Errorf("status = %d, want 410", rr.Code)
	}
}
```

- [ ] **Step 2: Run tests, verify failure**

Run: `go test ./internal/handler/ -run 'StreamZip|SharedStreamZip'`
Expected: FAIL — `handler.NewZipHandler` undefined.

- [ ] **Step 3: Implement**

`internal/handler/zip.go`:
```go
package handler

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/internal/viewmodel"
	"github.com/noelzappy/vaulx/internal/zipbuild"
)

type ZipHandler struct {
	queries db.Querier
	shares  *ShareHandler
	// Fetch is the object reader used for zip entries; a field so tests can
	// substitute in-memory objects.
	Fetch zipbuild.FetchFunc
}

func NewZipHandler(q db.Querier, sh *ShareHandler) *ZipHandler {
	return &ZipHandler{queries: q, shares: sh, Fetch: storage.GetObjectStream}
}

type treeStats struct {
	FileCount    int32
	ContentBytes int64
	NewestFile   pgtype.Timestamptz
}

// collectEntries lists the folder tree as zip entries. filter decides which
// files are included (nil = all); empty folders become directory entries.
func (h *ZipHandler) collectEntries(ctx context.Context, folderID pgtype.UUID, filter func(uploadedBy pgtype.UUID) bool) ([]zipbuild.Entry, treeStats, error) {
	folders, err := h.queries.ListFolderTreeFolders(ctx, folderID)
	if err != nil {
		return nil, treeStats{}, fmt.Errorf("list tree folders: %w", err)
	}
	files, err := h.queries.ListFolderTreeFiles(ctx, folderID)
	if err != nil {
		return nil, treeStats{}, fmt.Errorf("list tree files: %w", err)
	}

	nonEmpty := map[string]bool{}
	var entries []zipbuild.Entry
	var stats treeStats
	for _, f := range files {
		if filter != nil && !filter(f.UploadedBy) {
			continue
		}
		p := sanitizeFilename(f.Name)
		if f.Relpath != "" {
			p = f.Relpath + "/" + p
		}
		entries = append(entries, zipbuild.Entry{Path: p, S3Key: f.S3Key})
		nonEmpty[f.Relpath] = true
		stats.FileCount++
		stats.ContentBytes += f.SizeBytes
		if !stats.NewestFile.Valid || f.CreatedAt.Time.After(stats.NewestFile.Time) {
			stats.NewestFile = f.CreatedAt
		}
	}
	for _, fo := range folders {
		if fo.Relpath != "" && !nonEmpty[fo.Relpath] {
			entries = append(entries, zipbuild.Entry{Path: fo.Relpath, IsDir: true})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, stats, nil
}

// ownerFilter returns the per-file access rule for dashboard zips: admins
// see everything, everyone else only their own uploads (mirrors Download).
func ownerFilter(user auth.UserContext) func(pgtype.UUID) bool {
	if user.Role == "admin" {
		return nil
	}
	return func(uploadedBy pgtype.UUID) bool {
		return auth.CanAccess(user, uploadedBy.String())
	}
}

func (h *ZipHandler) streamZip(w http.ResponseWriter, r *http.Request, folderID pgtype.UUID, filter func(pgtype.UUID) bool) {
	folder, err := h.queries.GetFolder(r.Context(), folderID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	entries, _, err := h.collectEntries(r.Context(), folderID, filter)
	if err != nil {
		http.Error(w, "failed to list folder", http.StatusInternalServerError)
		return
	}

	name := sanitizeFilename(folder.Name)
	if name == "" {
		name = "folder"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, name))
	// Errors past this point cannot change the response code — headers are
	// already on the wire. Build stops writing, leaving a truncated archive.
	if err := zipbuild.Build(r.Context(), w, entries, h.Fetch); err != nil {
		fmt.Printf("zip stream %s: %v\n", folder.Name, err)
	}
}

// GET /files/{folderID}/zip
func (h *ZipHandler) StreamZip(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	folderID, err := viewmodel.UUIDFromString(chi.URLParam(r, "folderID"))
	if err != nil {
		http.Error(w, "invalid folder id", http.StatusBadRequest)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	action := "folder.zip_download"
	resourceType := "folder"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       action,
		ResourceType: &resourceType,
		ResourceID:   folderID,
	})

	h.streamZip(w, r, folderID, ownerFilter(user))
}

// resolveShareForZip validates the slug and optional ?folder= subfolder.
// Returns the share, the target folder, and a non-zero HTTP status on error.
func (h *ZipHandler) resolveShareForZip(r *http.Request) (db.Share, pgtype.UUID, int) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		return db.Share{}, pgtype.UUID{}, http.StatusNotFound
	}
	share, err := h.queries.GetShareBySlug(r.Context(), slug)
	if err != nil || !share.FolderID.Valid {
		return db.Share{}, pgtype.UUID{}, http.StatusNotFound
	}
	if share.ExpiresAt.Valid && time.Now().UTC().After(share.ExpiresAt.Time) {
		return db.Share{}, pgtype.UUID{}, http.StatusGone
	}
	if share.MaxViews != nil && share.ViewCount >= *share.MaxViews {
		return db.Share{}, pgtype.UUID{}, http.StatusGone
	}

	target := share.FolderID
	if sub := r.URL.Query().Get("folder"); sub != "" {
		subUUID, err := viewmodel.UUIDFromString(sub)
		if err != nil || !h.shares.folderInTree(r.Context(), subUUID, share.FolderID) {
			return db.Share{}, pgtype.UUID{}, http.StatusNotFound
		}
		target = subUUID
	}
	return share, target, 0
}

// GET /s/{slug}/zip?folder=
func (h *ZipHandler) SharedStreamZip(w http.ResponseWriter, r *http.Request) {
	_, target, code := h.resolveShareForZip(r)
	if code != 0 {
		http.Error(w, http.StatusText(code), code)
		return
	}
	h.streamZip(w, r, target, nil)
}
```

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/handler/ -run 'StreamZip|SharedStreamZip'`
Expected: PASS (6 tests). Also run `go test ./...` — no regressions.

- [ ] **Step 5: Wire routes in `cmd/server/main.go`**

Add `zipHandler := handler.NewZipHandler(queries, shareHandler)` next to the other handler constructors. In the authenticated group (near `r.Get("/files/{fileID}/download", …)`):
```go
r.Get("/files/{folderID}/zip", zipHandler.StreamZip)
```
Next to the public share routes:
```go
r.Get("/s/{slug}/zip", zipHandler.SharedStreamZip)
```
NOTE: chi treats `/files/{folderID}/zip` and `/files/{fileID}/download` as distinct literal-suffix patterns; no conflict with `/files/{folderID}` (GET ListFolder) because the `/zip` suffix is more specific.

- [ ] **Step 6: Build and commit**

Run: `go build ./... && go test ./internal/handler/`
Expected: compiles, tests pass.

```bash
git add internal/handler/zip.go internal/handler/zip_test.go cmd/server/main.go
git commit -m "feat: stream folder tree as zip on dashboard and shares"
```

---

### Task 5: Prepared-zip worker

**Files:**
- Create: `internal/zipjobs/worker.go`
- Test: `internal/zipjobs/worker_test.go`

**Interfaces:**
- Consumes: `db.Querier` zip-job queries (Task 1), `zipbuild.Build` (Task 2), `storage.GetObjectStream` / `storage.UploadStream` (Task 3).
- Produces:
```go
package zipjobs

// ListEntries returns the zip entries for a folder tree. Set by main to the
// handler-level collector so worker and handlers agree on contents.
type ListEntriesFunc func(ctx context.Context, folderID pgtype.UUID) ([]zipbuild.Entry, error)

type Worker struct {
    Queries     db.Querier
    ListEntries ListEntriesFunc
    Fetch       zipbuild.FetchFunc
    Upload      func(ctx context.Context, key, contentType string, r io.Reader) (int64, error)
    Delete      func(ctx context.Context, key string) error
    Concurrency int           // running-job cap; default 2
    PollEvery   time.Duration // claim-loop interval; default 2s
}
func (w *Worker) Start(ctx context.Context)      // spawns claim loop + hourly cleanup; returns immediately
func (w *Worker) RecoverStale(ctx context.Context) error // FailStaleRunningZipJobs, call once at boot
func (w *Worker) RunOnce(ctx context.Context) bool       // claim + run one job; exported for tests
func (w *Worker) CleanupExpired(ctx context.Context)     // delete expired zips; exported for tests
func ZipKey(job db.ZipJob) string                        // "zips/<folderID>/<jobID>.zip"
```

- [ ] **Step 1: Write failing tests**

`internal/zipjobs/worker_test.go`:
```go
package zipjobs_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/zipbuild"
	"github.com/noelzappy/vaulx/internal/zipjobs"
)

func uuid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

type jobQuerier struct {
	db.Querier
	pending    []db.ZipJob
	readyKey   string
	readySize  int64
	failedErr  string
	staleReset bool
	expired    []db.ZipJob
	expiredIDs []pgtype.UUID
}

func (m *jobQuerier) ClaimNextPendingZipJob(ctx context.Context) (db.ZipJob, error) {
	if len(m.pending) == 0 {
		return db.ZipJob{}, errors.New("no rows")
	}
	j := m.pending[0]
	m.pending = m.pending[1:]
	return j, nil
}

func (m *jobQuerier) MarkZipJobReady(ctx context.Context, arg db.MarkZipJobReadyParams) error {
	if arg.S3Key != nil {
		m.readyKey = *arg.S3Key
	}
	m.readySize = arg.SizeBytes
	return nil
}

func (m *jobQuerier) MarkZipJobFailed(ctx context.Context, arg db.MarkZipJobFailedParams) error {
	if arg.Error != nil {
		m.failedErr = *arg.Error
	}
	return nil
}

func (m *jobQuerier) FailStaleRunningZipJobs(ctx context.Context) error {
	m.staleReset = true
	return nil
}

func (m *jobQuerier) ListExpiredReadyZipJobs(ctx context.Context) ([]db.ZipJob, error) {
	return m.expired, nil
}

func (m *jobQuerier) MarkZipJobExpired(ctx context.Context, id pgtype.UUID) error {
	m.expiredIDs = append(m.expiredIDs, id)
	return nil
}

func testWorker(q db.Querier, uploadErr error) (*zipjobs.Worker, *[]string) {
	var uploaded []string
	w := &zipjobs.Worker{
		Queries: q,
		ListEntries: func(ctx context.Context, folderID pgtype.UUID) ([]zipbuild.Entry, error) {
			return []zipbuild.Entry{{Path: "a.txt", S3Key: "k1"}}, nil
		},
		Fetch: func(ctx context.Context, key string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("payload")), nil
		},
		Upload: func(ctx context.Context, key, contentType string, r io.Reader) (int64, error) {
			if uploadErr != nil {
				return 0, uploadErr
			}
			n, _ := io.Copy(io.Discard, r)
			uploaded = append(uploaded, key)
			return n, nil
		},
		Delete: func(ctx context.Context, key string) error { return nil },
	}
	return w, &uploaded
}

func TestRunOnce_Success(t *testing.T) {
	q := &jobQuerier{pending: []db.ZipJob{{ID: uuid(1), FolderID: uuid(2)}}}
	w, uploaded := testWorker(q, nil)

	if ran := w.RunOnce(context.Background()); !ran {
		t.Fatal("expected a job to run")
	}
	if len(*uploaded) != 1 || (*uploaded)[0] != zipjobs.ZipKey(db.ZipJob{ID: uuid(1), FolderID: uuid(2)}) {
		t.Errorf("uploaded = %v", *uploaded)
	}
	if q.readyKey == "" || q.readySize == 0 {
		t.Errorf("job not marked ready: key=%q size=%d", q.readyKey, q.readySize)
	}
}

func TestRunOnce_UploadFailureMarksFailed(t *testing.T) {
	q := &jobQuerier{pending: []db.ZipJob{{ID: uuid(1), FolderID: uuid(2)}}}
	w, _ := testWorker(q, errors.New("bucket unreachable"))

	w.RunOnce(context.Background())
	if q.failedErr == "" {
		t.Error("expected job marked failed")
	}
}

func TestRunOnce_NoPendingJobs(t *testing.T) {
	w, _ := testWorker(&jobQuerier{}, nil)
	if ran := w.RunOnce(context.Background()); ran {
		t.Error("expected no job to run")
	}
}

func TestRecoverStale(t *testing.T) {
	q := &jobQuerier{}
	w, _ := testWorker(q, nil)
	if err := w.RecoverStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !q.staleReset {
		t.Error("expected FailStaleRunningZipJobs called")
	}
}

func TestCleanupExpired(t *testing.T) {
	key := "zips/x/y.zip"
	q := &jobQuerier{expired: []db.ZipJob{{ID: uuid(9), S3Key: &key}}}
	w, _ := testWorker(q, nil)
	w.CleanupExpired(context.Background())
	if len(q.expiredIDs) != 1 {
		t.Errorf("expected 1 job marked expired, got %d", len(q.expiredIDs))
	}
}
```

- [ ] **Step 2: Run tests, verify failure**

Run: `go test ./internal/zipjobs/`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement**

`internal/zipjobs/worker.go`:
```go
// Package zipjobs runs prepared-zip background jobs: it claims pending
// zip_jobs rows, builds the archive into the bucket, and cleans up expired
// archives. One Worker runs inside the server process.
package zipjobs

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/zipbuild"
)

type ListEntriesFunc func(ctx context.Context, folderID pgtype.UUID) ([]zipbuild.Entry, error)

type Worker struct {
	Queries     db.Querier
	ListEntries ListEntriesFunc
	Fetch       zipbuild.FetchFunc
	Upload      func(ctx context.Context, key, contentType string, r io.Reader) (int64, error)
	Delete      func(ctx context.Context, key string) error
	Concurrency int           // running-job cap; default 2
	PollEvery   time.Duration // claim-loop interval; default 2s
}

func ZipKey(job db.ZipJob) string {
	return fmt.Sprintf("zips/%s/%s.zip", job.FolderID.String(), job.ID.String())
}

// RecoverStale marks jobs left 'running' by a previous process as failed.
// Call once at startup, before Start.
func (w *Worker) RecoverStale(ctx context.Context) error {
	return w.Queries.FailStaleRunningZipJobs(ctx)
}

// Start launches Concurrency claim loops and an hourly cleanup loop, all
// exiting when ctx is done. Returns immediately.
func (w *Worker) Start(ctx context.Context) {
	n := w.Concurrency
	if n <= 0 {
		n = 2
	}
	poll := w.PollEvery
	if poll <= 0 {
		poll = 2 * time.Second
	}
	for i := 0; i < n; i++ {
		go func() {
			t := time.NewTicker(poll)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					for w.RunOnce(ctx) { // drain the queue while jobs exist
					}
				}
			}
		}()
	}
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.CleanupExpired(ctx)
			}
		}
	}()
}

// RunOnce claims and runs a single pending job. Returns false when the
// queue is empty.
func (w *Worker) RunOnce(ctx context.Context) bool {
	job, err := w.Queries.ClaimNextPendingZipJob(ctx)
	if err != nil {
		return false // no pending rows (or db error — next tick retries)
	}
	w.run(ctx, job)
	return true
}

func (w *Worker) run(ctx context.Context, job db.ZipJob) {
	entries, err := w.ListEntries(ctx, job.FolderID)
	if err != nil {
		w.fail(ctx, job, fmt.Sprintf("list folder: %v", err))
		return
	}

	key := ZipKey(job)
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(zipbuild.Build(ctx, pw, entries, w.Fetch))
	}()
	size, err := w.Upload(ctx, key, "application/zip", pr)
	if err != nil {
		pr.CloseWithError(err)
		w.fail(ctx, job, fmt.Sprintf("upload: %v", err))
		return
	}

	if err := w.Queries.MarkZipJobReady(ctx, db.MarkZipJobReadyParams{
		ID:        job.ID,
		S3Key:     &key,
		SizeBytes: size,
	}); err != nil {
		log.Printf("zipjobs: mark ready %s: %v", job.ID.String(), err)
	}
}

func (w *Worker) fail(ctx context.Context, job db.ZipJob, msg string) {
	log.Printf("zipjobs: job %s failed: %s", job.ID.String(), msg)
	if err := w.Queries.MarkZipJobFailed(ctx, db.MarkZipJobFailedParams{ID: job.ID, Error: &msg}); err != nil {
		log.Printf("zipjobs: mark failed %s: %v", job.ID.String(), err)
	}
}

// CleanupExpired deletes bucket objects for expired ready zips and marks
// their rows.
func (w *Worker) CleanupExpired(ctx context.Context) {
	jobs, err := w.Queries.ListExpiredReadyZipJobs(ctx)
	if err != nil {
		log.Printf("zipjobs: list expired: %v", err)
		return
	}
	for _, j := range jobs {
		if j.S3Key != nil {
			if err := w.Delete(ctx, *j.S3Key); err != nil {
				log.Printf("zipjobs: delete %s: %v", *j.S3Key, err)
				continue // retry next sweep; keep the row until the object is gone
			}
		}
		if err := w.Queries.MarkZipJobExpired(ctx, j.ID); err != nil {
			log.Printf("zipjobs: mark expired %s: %v", j.ID.String(), err)
		}
	}
}
```

NOTE for implementer: `MarkZipJobReadyParams`/`MarkZipJobFailedParams` field names come from sqlc generation in Task 1 — check `internal/db/zipjobs.sql.go` and adjust test/impl field names if sqlc chose different ones (e.g. `Error` may be generated as `Error_`; if so, rename here accordingly).

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/zipjobs/`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/zipjobs/
git commit -m "feat: add prepared-zip background worker"
```

---

### Task 6: Prepare/status handlers + routes

**Files:**
- Modify: `internal/handler/zip.go` (add handlers)
- Test: `internal/handler/zip_test.go` (append)
- Modify: `cmd/server/main.go` (routes, worker startup)

**Interfaces:**
- Consumes: `collectEntries`, `resolveShareForZip`, `ownerFilter` (Task 4); zip-job queries (Task 1); `zipjobs.Worker` (Task 5); `storage.PresignGETDownload`, `storage.DeleteObject`, `storage.UploadStream`, `storage.GetObjectStream` (Task 3 + existing); `httprate` (existing dep).
- Produces:
```go
func (h *ZipHandler) PrepareZip(w http.ResponseWriter, r *http.Request)       // POST /files/{folderID}/zip/prepare
func (h *ZipHandler) ZipStatus(w http.ResponseWriter, r *http.Request)        // GET  /files/{folderID}/zip/status
func (h *ZipHandler) SharedPrepareZip(w http.ResponseWriter, r *http.Request) // POST /s/{slug}/zip/prepare?folder=
func (h *ZipHandler) SharedZipStatus(w http.ResponseWriter, r *http.Request)  // GET  /s/{slug}/zip/status?folder=
// h.Presign func(ctx, key, filename string) (string, error) — test seam, defaults storage.PresignGETDownload
// h.EntriesForWorker() zipjobs.ListEntriesFunc — adapter passed to the worker in main
```
Status responses are small HTML fragments (htmx swap targets), `Content-Type: text/html`:
- pending/running: `<div class="zip-status" hx-get="<status-url>" hx-trigger="every 3s" hx-swap="outerHTML">Preparing zip…</div>`
- ready: `<div class="zip-status"><a class="btn btn-primary" href="<presigned>">Download zip (resumable)</a></div>`
- failed: `<div class="zip-status">Zip failed: <error></div>`
- none: `<div class="zip-status"></div>`

- [ ] **Step 1: Write failing tests (append to `internal/handler/zip_test.go`)**

```go
// prepQuerier extends zipQuerier with zip-job bookkeeping.
type prepQuerier struct {
	zipQuerier
	created  []db.CreateZipJobParams
	reusable *db.ZipJob
	byID     map[[16]byte]db.ZipJob
}

func (m *prepQuerier) CreateZipJob(ctx context.Context, arg db.CreateZipJobParams) (db.ZipJob, error) {
	m.created = append(m.created, arg)
	return db.ZipJob{ID: uuid(0x77), FolderID: arg.FolderID, Status: "pending"}, nil
}

func (m *prepQuerier) GetReusableZipJob(ctx context.Context, arg db.GetReusableZipJobParams) (db.ZipJob, error) {
	if m.reusable != nil {
		return *m.reusable, nil
	}
	return db.ZipJob{}, errors.New("no rows")
}

func (m *prepQuerier) GetZipJob(ctx context.Context, id pgtype.UUID) (db.ZipJob, error) {
	if j, ok := m.byID[id.Bytes]; ok {
		return j, nil
	}
	return db.ZipJob{}, errors.New("no rows")
}

func prepareRequest(t *testing.T, q db.Querier) *httptest.ResponseRecorder {
	t.Helper()
	h := handler.NewZipHandler(q, handler.NewShareHandler(q))
	req := httptest.NewRequest(http.MethodPost, "/files/0a000000-0000-0000-0000-000000000000/zip/prepare", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("folderID", "0a000000-0000-0000-0000-000000000000")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "admin", Role: "admin"}))
	rr := httptest.NewRecorder()
	h.PrepareZip(rr, req)
	return rr
}

func TestPrepareZip_CreatesJobWithSnapshot(t *testing.T) {
	q := &prepQuerier{}
	rr := prepareRequest(t, q)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if len(q.created) != 1 {
		t.Fatalf("created %d jobs, want 1", len(q.created))
	}
	// zipQuerier tree: 2 files x 3 bytes
	if q.created[0].FileCount != 2 || q.created[0].ContentBytes != 6 {
		t.Errorf("snapshot = %+v", q.created[0])
	}
	if !strings.Contains(rr.Body.String(), "Preparing zip") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestPrepareZip_ReusesExistingJob(t *testing.T) {
	q := &prepQuerier{reusable: &db.ZipJob{ID: uuid(0x66), Status: "running"}}
	rr := prepareRequest(t, q)
	if len(q.created) != 0 {
		t.Errorf("expected no new job, created %d", len(q.created))
	}
	if !strings.Contains(rr.Body.String(), "Preparing zip") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestZipStatus_Ready(t *testing.T) {
	key := "zips/a/b.zip"
	job := db.ZipJob{ID: uuid(0x66), Status: "ready", S3Key: &key}
	q := &prepQuerier{reusable: &job, byID: map[[16]byte]db.ZipJob{{0x66}: job}}
	h := handler.NewZipHandler(q, handler.NewShareHandler(q))
	h.Presign = func(ctx context.Context, k, filename string) (string, error) {
		return "https://signed.example/" + k, nil
	}
	req := httptest.NewRequest(http.MethodGet, "/files/0a000000-0000-0000-0000-000000000000/zip/status", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("folderID", "0a000000-0000-0000-0000-000000000000")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "admin", Role: "admin"}))
	rr := httptest.NewRecorder()
	h.ZipStatus(rr, req)
	if !strings.Contains(rr.Body.String(), "https://signed.example/zips/a/b.zip") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestSharedPrepareZip_ExpiredShareGone(t *testing.T) {
	sq := &shareZipQuerier{expired: true}
	h := handler.NewZipHandler(sq, handler.NewShareHandler(sq))
	req := httptest.NewRequest(http.MethodPost, "/s/abc/zip/prepare", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", "abc")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.SharedPrepareZip(rr, req)
	if rr.Code != http.StatusGone {
		t.Errorf("status = %d, want 410", rr.Code)
	}
}
```

- [ ] **Step 2: Run tests, verify failure**

Run: `go test ./internal/handler/ -run 'PrepareZip|ZipStatus'`
Expected: FAIL — `PrepareZip` undefined.

- [ ] **Step 3: Implement (append to `internal/handler/zip.go`)**

```go
// Presign is a test seam; defaults to storage.PresignGETDownload.
// Add field to ZipHandler struct and initialize in NewZipHandler:
//   Presign func(ctx context.Context, key, filename string) (string, error)
//   h.Presign = storage.PresignGETDownload

// EntriesForWorker adapts collectEntries for the background worker (no
// owner filter: prepared zips carry the folder's full active tree — the
// dashboard prepare button is admin/owner-gated the same as streaming, and
// share prepares are tree-scoped by resolveShareForZip).
func (h *ZipHandler) EntriesForWorker() func(ctx context.Context, folderID pgtype.UUID) ([]zipbuild.Entry, error) {
	return func(ctx context.Context, folderID pgtype.UUID) ([]zipbuild.Entry, error) {
		entries, _, err := h.collectEntries(ctx, folderID, nil)
		return entries, err
	}
}

// prepare finds or creates a job for the folder and renders its status.
func (h *ZipHandler) prepare(w http.ResponseWriter, r *http.Request, folderID pgtype.UUID, shareID pgtype.UUID, statusURL string) {
	job, stats, found, err := h.findJob(r.Context(), folderID)
	if err != nil {
		http.Error(w, "failed to inspect folder", http.StatusInternalServerError)
		return
	}
	if !found {
		job, err = h.queries.CreateZipJob(r.Context(), db.CreateZipJobParams{
			FolderID:     folderID,
			ShareID:      shareID,
			FileCount:    stats.FileCount,
			ContentBytes: stats.ContentBytes,
		})
		if err != nil {
			http.Error(w, "failed to create zip job", http.StatusInternalServerError)
			return
		}
	}
	h.renderJobStatus(w, r, job, statusURL)
}

// renderJobStatus writes the htmx status fragment for a job.
func (h *ZipHandler) renderJobStatus(w http.ResponseWriter, r *http.Request, job db.ZipJob, statusURL string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	switch job.Status {
	case "ready":
		if job.S3Key == nil {
			fmt.Fprint(w, `<div class="zip-status">Zip failed: missing archive</div>`)
			return
		}
		url, err := h.Presign(r.Context(), *job.S3Key, "folder.zip")
		if err != nil {
			fmt.Fprint(w, `<div class="zip-status">Zip failed: could not sign download</div>`)
			return
		}
		fmt.Fprintf(w, `<div class="zip-status"><a class="btn btn-primary" href="%s">Download zip (resumable, expires in 24h)</a></div>`, url)
	case "failed":
		msg := "unknown error"
		if job.Error != nil {
			msg = *job.Error
		}
		fmt.Fprintf(w, `<div class="zip-status">Zip failed: %s</div>`, msg)
	default: // pending / running
		fmt.Fprintf(w, `<div class="zip-status" hx-get="%s" hx-trigger="every 3s" hx-swap="outerHTML">Preparing zip…</div>`, statusURL)
	}
}

// findJob returns the folder's current job — an in-flight one, or a ready
// one whose snapshot still matches the folder contents. found=false means
// no such job; err is reserved for listing failures.
func (h *ZipHandler) findJob(ctx context.Context, folderID pgtype.UUID) (job db.ZipJob, stats treeStats, found bool, err error) {
	_, stats, err = h.collectEntries(ctx, folderID, nil)
	if err != nil {
		return db.ZipJob{}, treeStats{}, false, err
	}
	job, jerr := h.queries.GetReusableZipJob(ctx, db.GetReusableZipJobParams{
		FolderID:     folderID,
		FileCount:    stats.FileCount,
		ContentBytes: stats.ContentBytes,
		CreatedAt:    stats.NewestFile,
	})
	return job, stats, jerr == nil, nil
}

// status renders the current job for the folder, or an empty slot.
func (h *ZipHandler) status(w http.ResponseWriter, r *http.Request, folderID pgtype.UUID, statusURL string) {
	job, _, found, err := h.findJob(r.Context(), folderID)
	if err != nil {
		http.Error(w, "failed to inspect folder", http.StatusInternalServerError)
		return
	}
	if !found {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="zip-status"></div>`)
		return
	}
	h.renderJobStatus(w, r, job, statusURL)
}

// POST /files/{folderID}/zip/prepare
func (h *ZipHandler) PrepareZip(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_ = user
	folderID, err := viewmodel.UUIDFromString(chi.URLParam(r, "folderID"))
	if err != nil {
		http.Error(w, "invalid folder id", http.StatusBadRequest)
		return
	}
	h.prepare(w, r, folderID, pgtype.UUID{}, "/files/"+folderID.String()+"/zip/status")
}

// GET /files/{folderID}/zip/status
func (h *ZipHandler) ZipStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetCurrentUser(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	folderID, err := viewmodel.UUIDFromString(chi.URLParam(r, "folderID"))
	if err != nil {
		http.Error(w, "invalid folder id", http.StatusBadRequest)
		return
	}
	h.status(w, r, folderID, "/files/"+folderID.String()+"/zip/status")
}

// POST /s/{slug}/zip/prepare?folder=
func (h *ZipHandler) SharedPrepareZip(w http.ResponseWriter, r *http.Request) {
	share, target, code := h.resolveShareForZip(r)
	if code != 0 {
		http.Error(w, http.StatusText(code), code)
		return
	}
	statusURL := "/s/" + share.Slug + "/zip/status?folder=" + target.String()
	h.prepare(w, r, target, share.ID, statusURL)
}

// GET /s/{slug}/zip/status?folder=
func (h *ZipHandler) SharedZipStatus(w http.ResponseWriter, r *http.Request) {
	share, target, code := h.resolveShareForZip(r)
	if code != 0 {
		http.Error(w, http.StatusText(code), code)
		return
	}
	h.status(w, r, target, "/s/"+share.Slug+"/zip/status?folder="+target.String())
}
```

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/handler/`
Expected: PASS, including all Task 4 tests.

- [ ] **Step 5: Wire routes and worker in `cmd/server/main.go`**

Authenticated group:
```go
r.Post("/files/{folderID}/zip/prepare", zipHandler.PrepareZip)
r.Get("/files/{folderID}/zip/status", zipHandler.ZipStatus)
```
Public share routes (rate-limit prepare):
```go
r.With(httprate.LimitByIP(5, 1*time.Minute)).Post("/s/{slug}/zip/prepare", zipHandler.SharedPrepareZip)
r.Get("/s/{slug}/zip/status", zipHandler.SharedZipStatus)
```
After handler construction, before `ListenAndServe`:
```go
zipWorker := &zipjobs.Worker{
	Queries:     queries,
	ListEntries: zipHandler.EntriesForWorker(),
	Fetch:       storage.GetObjectStream,
	Upload:      storage.UploadStream,
	Delete:      storage.DeleteObject,
}
if err := zipWorker.RecoverStale(ctx); err != nil {
	log.Printf("zipjobs: recover stale: %v", err)
}
zipWorker.Start(ctx)
```
(`ctx` = the server's base context already present in main; import `internal/zipjobs`.)

- [ ] **Step 6: Build, test, commit**

Run: `go build ./... && go test ./...`
Expected: all pass.

```bash
git add internal/handler/zip.go internal/handler/zip_test.go cmd/server/main.go
git commit -m "feat: prepared zip jobs with resumable presigned downloads"
```

---

### Task 7: UI wiring

**Files:**
- Modify: `web/templates/file_browser.templ` (toolbar, folder view)
- Modify: `web/templates/shared_folder.templ` (header actions)
- Generated: `web/templates/*_templ.go` via `make generate`

**Interfaces:**
- Consumes: routes from Tasks 4 and 6. The file browser toolbar is at `web/templates/file_browser.templ:16`; the shared folder header at `web/templates/shared_folder.templ:16`. Buttons only render when viewing a folder (dashboard: the folder-view variant that has a folder ID in scope; shared: always — the page is always a folder).

- [ ] **Step 1: Dashboard toolbar buttons**

In `file_browser.templ`, in the toolbar of the folder view (the template variant rendering a specific folder — it has the folder's ID in scope; match existing `btn btn-ghost` styling), add:
```html
<a class="btn btn-ghost" href={ templ.SafeURL("/files/" + folderID + "/zip") }>Download zip</a>
<button
	class="btn btn-ghost"
	hx-post={ "/files/" + folderID + "/zip/prepare" }
	hx-target="#zip-status-slot"
	hx-swap="innerHTML"
>Prepare zip</button>
<div id="zip-status-slot"></div>
```
If the root ("My Files") toolbar has no folder ID, the buttons appear only inside folders — matching the spec (zip a folder).

- [ ] **Step 2: Shared page buttons**

In `shared_folder.templ` header (line ~16), with `slug`, the current folder id (`folderID` — the template renders `folder`; pass its ID through, adding a parameter to the templ component if needed) and `isRoot` in scope:
```html
<a class="btn btn-ghost" href={ templ.SafeURL("/s/" + slug + "/zip?folder=" + folderID) }>Download zip</a>
<button
	class="btn btn-ghost"
	hx-post={ "/s/" + slug + "/zip/prepare?folder=" + folderID }
	hx-target="#zip-status-slot"
	hx-swap="innerHTML"
>Prepare zip</button>
<div id="zip-status-slot"></div>
```
Check `shared_folder.templ`'s component signature first; `SharedFolderPage(slug, folderName, isRoot, folders, files)` currently receives no folder ID — add it as a parameter and update the call in `share.go:269` (`resolveFolderShare`) to pass `target.String()`.

- [ ] **Step 3: Regenerate and build**

Run: `make generate && go build ./... && go test ./...`
Expected: compiles, all tests pass.

- [ ] **Step 4: Verify htmx availability on shared page**

Check `shared_folder.templ`'s layout includes the htmx `<script>` (the dashboard layout does). If the shared layout omits htmx, add the same script tag the main layout uses — otherwise `hx-post` buttons are dead.

- [ ] **Step 5: Commit**

```bash
git add web/templates/ internal/handler/share.go
git commit -m "feat: zip download and prepare buttons on dashboard and shared folders"
```

---

### Task 8: End-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Run the app** per `.claude/skills/verify/SKILL.md` (throwaway postgres :5433, server :8090, real bucket).

- [ ] **Step 2: Drive it** (headless Chrome per the verify skill if the extension is down):
  1. Login, create folder `ziptest` with subfolder `inner`; upload a small file to each (≤20MB → single PUT path).
  2. Open `ziptest` → click **Download zip** → response is a valid zip containing both files with `inner/` prefix on the nested one (`unzip -l` the download).
  3. Click **Prepare zip** → status fragment shows "Preparing zip…", then within ~10s polls to "Download zip (resumable…)"; the presigned link downloads the same archive; `curl -r 0-99 <url>` returns HTTP 206 (range/resume works).
  4. Create a folder share for `ziptest`; open `/s/<slug>` logged out → both buttons present; **Download zip** streams the full tree; **Prepare zip** reuses the *same* job (no second `zips/` object appears in the bucket).
  5. 🔍 Probe: request `/s/<slug>/zip?folder=<unrelated-folder-id>` → 404. Hit share prepare 6× rapidly → 6th returns 429.

- [ ] **Step 3: Clean up** — delete test objects (`files/…`, `uploads/…`, `zips/…` keys) with the signed-DELETE curl from the verify skill; remove container; kill server.

- [ ] **Step 4: Update docs** — add zip endpoints to README if it documents routes.

```bash
git add -A && git commit -m "docs: note zip download endpoints" # only if README changed
```
