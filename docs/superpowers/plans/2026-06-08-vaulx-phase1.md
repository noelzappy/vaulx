# Vaulx Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a runnable Go+templ file-asset platform (Phase 1): session auth, folder/file browser, seed admin, full dark-theme UI.

**Architecture:** Chi router, templ server-side components, pgxpool+sqlc (pgx/v5) for DB queries, gorilla/sessions+antonlindstrom/pgstore for Postgres-backed sessions, golang-migrate with embed.FS for migrations. No file bytes transit Go — presigned URLs only (S3 client stub in Phase 1).

**Tech Stack:** Go 1.23, Chi v5, a-h/templ, HTMX+Alpine.js (CDN), PostgreSQL, sqlc pgx/v5, golang-migrate, gorilla/sessions, antonlindstrom/pgstore, joho/godotenv, bcrypt, AWS SDK Go v2

---

## File Map

| File | Purpose |
|------|---------|
| `go.mod` | Module definition + all direct deps |
| `Makefile` | `generate`, `build`, `dev` targets |
| `.env.example` | Env var template |
| `migrations/embed.go` | Embeds `*.sql` files into the binary |
| `migrations/001_initial.up.sql` | Full DB schema |
| `migrations/001_initial.down.sql` | Schema teardown (reverse order) |
| `internal/db/db.go` | pgxpool singleton, `Connect()` |
| `internal/db/queries/users.sql` | sqlc user queries |
| `internal/db/queries/folders.sql` | sqlc folder queries |
| `internal/db/queries/files.sql` | sqlc file + joined queries |
| `internal/db/queries/audit.sql` | sqlc audit log insert |
| `internal/viewmodel/models.go` | View structs + `HumanSize`, `RelativeTime` |
| `internal/viewmodel/models_test.go` | Unit tests for helper functions |
| `internal/auth/context.go` | Context key types, `SetCurrentUser`, `GetCurrentUser` |
| `internal/auth/acl.go` | `CanAccess()` Phase 1 stub |
| `internal/auth/acl_test.go` | Unit tests for ACL logic |
| `internal/auth/middleware.go` | `RequireAuth` middleware |
| `internal/handler/auth.go` | Login / logout handlers |
| `internal/handler/files.go` | Folder + file listing, create, delete, rename |
| `internal/storage/client.go` | S3/Hetzner client + presign client (Phase 1 wired but unused) |
| `internal/seed/seed.go` | Seeds admin user on empty `users` table |
| `web/templates/login.templ` | Standalone login page |
| `web/templates/layout.templ` | Outer HTML shell with `{ children... }` slot |
| `web/templates/sidebar.templ` | Fixed left nav |
| `web/templates/file_browser.templ` | Grid + breadcrumb page composites |
| `web/templates/file_card.templ` | Single file card |
| `web/templates/folder_card.templ` | Single folder card |
| `web/static/app.css` | Global dark-theme CSS |
| `cmd/server/main.go` | Entry point — wires everything, starts HTTP |

---

## Task 1: Project Scaffold

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `.env.example`
- Create directory tree

- [ ] **Step 1: Create directories**

```bash
mkdir -p cmd/server
mkdir -p internal/{auth,db/queries,handler,storage,seed,viewmodel}
mkdir -p migrations
mkdir -p web/{templates,static}
```

- [ ] **Step 2: Write `go.mod`**

```
module github.com/brifafrica/vaulx

go 1.23
```

- [ ] **Step 3: Add all direct dependencies**

```bash
go get github.com/go-chi/chi/v5@v5.2.1
go get github.com/a-h/templ@latest
go get github.com/jackc/pgx/v5@v5.7.2
go get github.com/golang-migrate/migrate/v4@v4.18.2
go get github.com/golang-migrate/migrate/v4/database/postgres@v4.18.2
go get github.com/golang-migrate/migrate/v4/source/iofs@v4.18.2
go get github.com/gorilla/sessions@v1.4.0
go get github.com/gorilla/securecookie@v1.1.2
go get github.com/antonlindstrom/pgstore@latest
go get github.com/lib/pq@v1.10.9
go get github.com/joho/godotenv@v1.5.1
go get golang.org/x/crypto@latest
go get github.com/aws/aws-sdk-go-v2@latest
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/credentials@latest
go get github.com/aws/aws-sdk-go-v2/service/s3@latest
go mod tidy
```

- [ ] **Step 4: Write `Makefile`**

```makefile
.PHONY: generate build dev

generate:
	templ generate

build: generate
	go build -o bin/server ./cmd/server

dev: generate
	air -c .air.toml || go run ./cmd/server
```

- [ ] **Step 5: Write `.env.example`**

```env
DATABASE_URL=postgres://user:pass@localhost:5432/vaulx?sslmode=disable
SESSION_SECRET=replace-with-32-plus-byte-random-string
HETZNER_ACCESS_KEY=
HETZNER_SECRET_KEY=
HETZNER_S3_ENDPOINT=https://fsn1.your-objectstorage.com
HETZNER_BUCKET=vaulx
PORT=8080
SEED_ADMIN_EMAIL=admin@brif.africa
SEED_ADMIN_PASSWORD=changeme
```

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum Makefile .env.example
git commit -m "chore: project scaffold with all dependencies"
```

---

## Task 2: Migration Files

**Files:**
- Create: `migrations/embed.go`
- Create: `migrations/001_initial.up.sql`
- Create: `migrations/001_initial.down.sql`

- [ ] **Step 1: Write `migrations/embed.go`**

```go
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

- [ ] **Step 2: Write `migrations/001_initial.up.sql`**

```sql
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
```

- [ ] **Step 3: Write `migrations/001_initial.down.sql`**

```sql
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS shares;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS folders;
DROP TABLE IF EXISTS users;
DROP EXTENSION IF EXISTS "pgcrypto";
```

- [ ] **Step 4: Commit**

```bash
git add migrations/
git commit -m "feat: add initial database schema migration"
```

---

## Task 3: Database Package

**Files:**
- Create: `internal/db/db.go`

- [ ] **Step 1: Write `internal/db/db.go`**

```go
package db

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

var DB *pgxpool.Pool

func Connect(ctx context.Context) {
	var err error
	DB, err = pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db: create pool: %v", err)
	}
	if err := DB.Ping(ctx); err != nil {
		log.Fatalf("db: ping: %v", err)
	}
	log.Println("db: connected")
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/db/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/db/db.go
git commit -m "feat: add pgxpool database connection singleton"
```

---

## Task 4: sqlc Configuration + SQL Queries + Code Generation

**Files:**
- Create: `sqlc.yaml`
- Create: `internal/db/queries/users.sql`
- Create: `internal/db/queries/folders.sql`
- Create: `internal/db/queries/files.sql`
- Create: `internal/db/queries/audit.sql`
- Generated: `internal/db/*.go` (by sqlc)

- [ ] **Step 1: Install sqlc (if not present)**

```bash
which sqlc || go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
```

- [ ] **Step 2: Write `sqlc.yaml`**

```yaml
version: "2"
sql:
  - engine: "postgresql"
    queries: "internal/db/queries/"
    schema: "migrations/"
    gen:
      go:
        package: "db"
        out: "internal/db"
        sql_package: "pgx/v5"
        emit_json_tags: true
        emit_pointers_for_null_types: true
        emit_rows_affected: false
```

- [ ] **Step 3: Write `internal/db/queries/users.sql`**

```sql
-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1 AND active = true
LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (email, name, role, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;
```

- [ ] **Step 4: Write `internal/db/queries/folders.sql`**

```sql
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

-- name: CountFolderItems :one
SELECT COUNT(*) FROM (
  SELECT id FROM folders WHERE parent_id = $1
  UNION ALL
  SELECT id FROM files WHERE folder_id = $1 AND status = 'active'
) sub;
```

- [ ] **Step 5: Write `internal/db/queries/files.sql`**

```sql
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
```

- [ ] **Step 6: Write `internal/db/queries/audit.sql`**

```sql
-- name: CreateAuditLog :one
INSERT INTO audit_log (user_id, action, resource_type, resource_id, meta)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;
```

- [ ] **Step 7: Run sqlc generate**

```bash
sqlc generate
```

Expected: creates `internal/db/models.go`, `internal/db/querier.go`, `internal/db/db.go` (sqlc version), and `internal/db/query.sql.go`.

> **Note:** sqlc will create its own `internal/db/db.go`. Rename the hand-written one to `internal/db/pool.go` first:
> ```bash
> mv internal/db/db.go internal/db/pool.go
> sqlc generate
> ```

- [ ] **Step 8: Verify compilation**

```bash
go build ./internal/db/...
```

Expected: no output, exit 0.

- [ ] **Step 9: Commit**

```bash
git add sqlc.yaml internal/db/
git commit -m "feat: add sqlc config, SQL queries, and generated DB code"
```

---

## Task 5: View Models + Auth Types + ACL

**Files:**
- Create: `internal/viewmodel/models.go`
- Create: `internal/viewmodel/models_test.go`
- Create: `internal/auth/context.go`
- Create: `internal/auth/acl.go`
- Create: `internal/auth/acl_test.go`

- [ ] **Step 1: Write failing test for `HumanSize`**

`internal/viewmodel/models_test.go`:
```go
package viewmodel_test

import (
	"testing"
	"time"

	"github.com/brifafrica/vaulx/internal/viewmodel"
)

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tc := range tests {
		got := viewmodel.HumanSize(tc.bytes)
		if got != tc.expected {
			t.Errorf("HumanSize(%d) = %q, want %q", tc.bytes, got, tc.expected)
		}
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Now()
	if viewmodel.RelativeTime(now.Add(-30*time.Second)) != "just now" {
		t.Error("expected 'just now' for 30s ago")
	}
	if viewmodel.RelativeTime(now.Add(-2*time.Hour)) != "2 hours ago" {
		t.Error("expected '2 hours ago' for 2h ago")
	}
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/viewmodel/... 2>&1 | head -5
```

Expected: `cannot find package` or `undefined: viewmodel.HumanSize`

- [ ] **Step 3: Write `internal/viewmodel/models.go`**

```go
package viewmodel

import (
	"fmt"
	"time"

	"github.com/brifafrica/vaulx/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

type UserView struct {
	ID    string
	Email string
	Name  string
	Role  string
}

type FileView struct {
	ID           string
	Name         string
	SizeBytes    int64
	SizeHuman    string
	MimeType     string
	UploaderID   string
	UploaderName string
	FolderID     string
	Status       string
	CreatedAt    time.Time
	RelativeDate string
}

type FolderView struct {
	ID        string
	Name      string
	ParentID  string
	OwnerID   string
	CreatedAt time.Time
	ItemCount int64
}

type BreadcrumbItem struct {
	Name string
	ID   string
	URL  string
}

type FileBrowserData struct {
	Folders     []FolderView
	Files       []FileView
	Breadcrumbs []BreadcrumbItem
	FolderID    string
}

func HumanSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case bytes < KB:
		return fmt.Sprintf("%d B", bytes)
	case bytes < MB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	case bytes < GB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	default:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	}
}

func RelativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}

func UserFromDB(u db.User) UserView {
	return UserView{
		ID:    u.ID.String(),
		Email: u.Email,
		Name:  u.Name,
		Role:  u.Role,
	}
}

func FileFromDB(f db.File, uploaderName string) FileView {
	var mimeType, folderID string
	if f.MimeType != nil {
		mimeType = *f.MimeType
	}
	if f.FolderID != nil {
		folderID = f.FolderID.String()
	}
	var createdAt time.Time
	if f.CreatedAt != nil {
		createdAt = f.CreatedAt.Time
	}
	return FileView{
		ID:           f.ID.String(),
		Name:         f.Name,
		SizeBytes:    f.SizeBytes,
		SizeHuman:    HumanSize(f.SizeBytes),
		MimeType:     mimeType,
		UploaderID:   f.UploadedBy.String(),
		UploaderName: uploaderName,
		FolderID:     folderID,
		Status:       f.Status,
		CreatedAt:    createdAt,
		RelativeDate: RelativeTime(createdAt),
	}
}

func FolderFromDB(f db.Folder, itemCount int64) FolderView {
	var parentID string
	if f.ParentID != nil {
		parentID = f.ParentID.String()
	}
	var createdAt time.Time
	if f.CreatedAt != nil {
		createdAt = f.CreatedAt.Time
	}
	return FolderView{
		ID:        f.ID.String(),
		Name:      f.Name,
		ParentID:  parentID,
		OwnerID:   f.OwnerID.String(),
		CreatedAt: createdAt,
		ItemCount: itemCount,
	}
}

func UUIDFromString(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	err := u.Scan(s)
	return u, err
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/viewmodel/... -v
```

Expected:
```
--- PASS: TestHumanSize (0.00s)
--- PASS: TestRelativeTime (0.00s)
PASS
```

- [ ] **Step 5: Write failing ACL test**

`internal/auth/acl_test.go`:
```go
package auth_test

import (
	"testing"

	"github.com/brifafrica/vaulx/internal/auth"
)

func TestCanAccess(t *testing.T) {
	adminUser := auth.UserContext{ID: "a1", Role: "admin"}
	editorUser := auth.UserContext{ID: "u1", Role: "editor"}
	viewerUser := auth.UserContext{ID: "u2", Role: "viewer"}
	ownerID := "u1"
	otherOwner := "u3"

	tests := []struct {
		name     string
		user     auth.UserContext
		ownerID  string
		expected bool
	}{
		{"admin sees anything", adminUser, otherOwner, true},
		{"editor sees own resource", editorUser, ownerID, true},
		{"viewer sees own resource", viewerUser, "u2", true},
		{"editor blocked on other owner", editorUser, otherOwner, false},
		{"viewer blocked on other owner", viewerUser, otherOwner, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := auth.CanAccess(tc.user, tc.ownerID)
			if got != tc.expected {
				t.Errorf("CanAccess(%+v, %q) = %v, want %v", tc.user, tc.ownerID, got, tc.expected)
			}
		})
	}
}
```

- [ ] **Step 6: Run to verify failure**

```bash
go test ./internal/auth/... 2>&1 | head -5
```

Expected: `cannot find package` or `undefined`

- [ ] **Step 7: Write `internal/auth/context.go`**

```go
package auth

import "context"

type contextKey string

const (
	userContextKey contextKey = "vaulx_user"
)

type UserContext struct {
	ID    string
	Email string
	Name  string
	Role  string
}

func SetCurrentUser(ctx context.Context, u UserContext) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

func GetCurrentUser(ctx context.Context) (UserContext, bool) {
	u, ok := ctx.Value(userContextKey).(UserContext)
	return u, ok
}
```

- [ ] **Step 8: Write `internal/auth/acl.go`**

```go
package auth

// CanAccess returns true if user may access a resource owned by ownerID.
// Phase 1 stub: admin bypasses all checks; others need ownership.
// Phase 3 will expand this with explicit permission-table lookups.
func CanAccess(user UserContext, ownerID string) bool {
	if user.Role == "admin" {
		return true
	}
	return user.ID == ownerID
}

// CanEdit returns true if the user may create or modify resources.
func CanEdit(user UserContext) bool {
	return user.Role == "admin" || user.Role == "editor"
}
```

- [ ] **Step 9: Run ACL tests to verify they pass**

```bash
go test ./internal/auth/... -v -run TestCanAccess
```

Expected:
```
--- PASS: TestCanAccess/admin_sees_anything (0.00s)
--- PASS: TestCanAccess/editor_sees_own_resource (0.00s)
...
PASS
```

- [ ] **Step 10: Commit**

```bash
git add internal/viewmodel/ internal/auth/
git commit -m "feat: view models, auth context types, and ACL stub"
```

---

## Task 6: Auth Middleware + Session Store

**Files:**
- Create: `internal/auth/middleware.go`
- Create: `internal/auth/middleware_test.go`

- [ ] **Step 1: Write failing middleware test**

`internal/auth/middleware_test.go`:
```go
package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/gorilla/sessions"
)

func TestRequireAuth_RedirectsWhenNoSession(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret"))
	middleware := auth.RequireAuth(store)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/files", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected redirect 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/auth/login" {
		t.Errorf("expected redirect to /auth/login, got %q", loc)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/auth/... -run TestRequireAuth 2>&1 | head -5
```

Expected: `undefined: auth.RequireAuth`

- [ ] **Step 3: Write `internal/auth/middleware.go`**

```go
package auth

import (
	"net/http"

	"github.com/gorilla/sessions"
)

func RequireAuth(store sessions.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := store.Get(r, "vaulx-session")
			if err != nil || session.IsNew {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}

			userID, ok1 := session.Values["user_id"].(string)
			role, ok2 := session.Values["role"].(string)
			email, _ := session.Values["email"].(string)
			name, _ := session.Values["name"].(string)

			if !ok1 || !ok2 || userID == "" || role == "" {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}

			ctx := SetCurrentUser(r.Context(), UserContext{
				ID:    userID,
				Email: email,
				Name:  name,
				Role:  role,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

- [ ] **Step 4: Run middleware tests to verify they pass**

```bash
go test ./internal/auth/... -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/middleware.go internal/auth/middleware_test.go
git commit -m "feat: session-based auth middleware with redirect on missing session"
```

---

## Task 7: Auth Handlers (Login / Logout)

**Files:**
- Create: `internal/handler/auth.go`

- [ ] **Step 1: Write `internal/handler/auth.go`**

```go
package handler

import (
	"net/http"

	"github.com/brifafrica/vaulx/internal/db"
	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
)

type AuthHandler struct {
	queries *db.Queries
	store   sessions.Store
}

func NewAuthHandler(q *db.Queries, s sessions.Store) *AuthHandler {
	return &AuthHandler{queries: q, store: s}
}

func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderLogin(w, r, "")
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	user, err := h.queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		renderLogin(w, r, "Invalid email or password")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		renderLogin(w, r, "Invalid email or password")
		return
	}

	session, err := h.store.Get(r, "vaulx-session")
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	session.Values["user_id"] = user.ID.String()
	session.Values["role"] = user.Role
	session.Values["email"] = user.Email
	session.Values["name"] = user.Name

	if err := session.Save(r, w); err != nil {
		http.Error(w, "failed to save session", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/files", http.StatusFound)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	session, err := h.store.Get(r, "vaulx-session")
	if err == nil {
		session.Options.MaxAge = -1
		_ = session.Save(r, w)
	}
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}
```

- [ ] **Step 2: Add `renderLogin` helper**

Append to `internal/handler/auth.go`:
```go
func renderLogin(w http.ResponseWriter, r *http.Request, errMsg string) {
	// Imported in Task 9 once templates exist.
	// For now: placeholder that writes plain HTML.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	// templates.LoginPage(errMsg).Render(r.Context(), w) — uncommented in Task 12
	http.Error(w, "login page – templates not yet generated", http.StatusServiceUnavailable)
}
```

> **Note:** The renderLogin body gets replaced in Task 12 once templ components exist. Keep the comment.

- [ ] **Step 3: Verify compilation**

```bash
go build ./internal/handler/...
```

Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add internal/handler/auth.go
git commit -m "feat: login and logout HTTP handlers"
```

---

## Task 8: Storage Client

**Files:**
- Create: `internal/storage/client.go`

- [ ] **Step 1: Write `internal/storage/client.go`**

```go
package storage

import (
	"context"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var (
	Client        *s3.Client
	PresignClient *s3.PresignClient
	Bucket        string
)

func Connect(ctx context.Context) error {
	Bucket = os.Getenv("HETZNER_BUCKET")

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("eu-central"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			os.Getenv("HETZNER_ACCESS_KEY"),
			os.Getenv("HETZNER_SECRET_KEY"),
			"",
		)),
		config.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, opts ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					URL:               os.Getenv("HETZNER_S3_ENDPOINT"),
					HostnameImmutable: true,
				}, nil
			}),
		),
	)
	if err != nil {
		return err
	}

	Client = s3.NewFromConfig(cfg)
	PresignClient = s3.NewPresignClient(Client)
	return nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/storage/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/client.go
git commit -m "feat: S3/Hetzner storage client with presign support"
```

---

## Task 9: Seed Admin User

**Files:**
- Create: `internal/seed/seed.go`

- [ ] **Step 1: Write `internal/seed/seed.go`**

```go
package seed

import (
	"context"
	"log"
	"os"

	"github.com/brifafrica/vaulx/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func AdminUser(ctx context.Context, queries *db.Queries) {
	count, err := queries.CountUsers(ctx)
	if err != nil {
		log.Printf("seed: count users: %v", err)
		return
	}
	if count > 0 {
		return
	}

	email := os.Getenv("SEED_ADMIN_EMAIL")
	password := os.Getenv("SEED_ADMIN_PASSWORD")
	if email == "" || password == "" {
		log.Println("seed: SEED_ADMIN_EMAIL or SEED_ADMIN_PASSWORD not set — skipping")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("seed: bcrypt: %v", err)
	}

	_, err = queries.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		Name:         "Admin",
		Role:         "admin",
		PasswordHash: string(hash),
	})
	if err != nil {
		log.Fatalf("seed: create admin: %v", err)
	}

	log.Printf("seed: admin user created — email: %s  password: %s", email, password)
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/seed/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/seed/seed.go
git commit -m "feat: seed admin user on first run when users table is empty"
```

---

## Task 10: File & Folder Handlers

**Files:**
- Create: `internal/handler/files.go`

- [ ] **Step 1: Write `internal/handler/files.go`**

```go
package handler

import (
	"net/http"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/db"
	"github.com/brifafrica/vaulx/internal/viewmodel"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type FilesHandler struct {
	queries *db.Queries
}

func NewFilesHandler(q *db.Queries) *FilesHandler {
	return &FilesHandler{queries: q}
}

// GET /files — root-level folders and files
func (h *FilesHandler) List(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	ctx := r.Context()

	var (
		dbFolders []db.Folder
		dbFiles   []db.File
		err       error
	)

	userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)

	if user.Role == "admin" || uuidErr != nil {
		dbFolders, err = h.queries.ListRootFolders(ctx)
		if err != nil {
			http.Error(w, "failed to list folders", http.StatusInternalServerError)
			return
		}
		dbFiles, err = h.queries.ListRootFiles(ctx)
	} else {
		dbFolders, err = h.queries.ListRootFoldersForUser(ctx, userUUID)
		if err != nil {
			http.Error(w, "failed to list folders", http.StatusInternalServerError)
			return
		}
		dbFiles, err = h.queries.ListRootFilesForUser(ctx, userUUID)
	}
	if err != nil {
		http.Error(w, "failed to list files", http.StatusInternalServerError)
		return
	}

	folders, files := h.buildViews(ctx, dbFolders, dbFiles)
	data := viewmodel.FileBrowserData{
		Folders:     folders,
		Files:       files,
		Breadcrumbs: []viewmodel.BreadcrumbItem{{Name: "My Files", URL: "/files"}},
	}

	renderFileBrowser(w, r, data, viewmodel.UserFromDB(db.User{
		ID:    pgtype.UUID{},
		Email: user.Email,
		Name:  user.Name,
		Role:  user.Role,
	}))
}

// GET /files/{folderID} — folder contents
func (h *FilesHandler) ListFolder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	ctx := r.Context()
	folderIDStr := chi.URLParam(r, "folderID")

	folderUUID, err := viewmodel.UUIDFromString(folderIDStr)
	if err != nil {
		http.Error(w, "invalid folder id", http.StatusBadRequest)
		return
	}

	folder, err := h.queries.GetFolder(ctx, folderUUID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if !auth.CanAccess(user, folder.OwnerID.String()) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var dbFolders []db.Folder
	var dbFiles []db.File
	userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)

	if user.Role == "admin" || uuidErr != nil {
		dbFolders, err = h.queries.ListFoldersByParent(ctx, &folderUUID)
		if err != nil {
			http.Error(w, "failed to list folders", http.StatusInternalServerError)
			return
		}
		dbFiles, err = h.queries.ListFilesByFolder(ctx, &folderUUID)
	} else {
		dbFolders, err = h.queries.ListFoldersByParentForUser(ctx, db.ListFoldersByParentForUserParams{
			ParentID: &folderUUID,
			UserID:   userUUID,
		})
		if err != nil {
			http.Error(w, "failed to list folders", http.StatusInternalServerError)
			return
		}
		dbFiles, err = h.queries.ListFilesByFolderForUser(ctx, db.ListFilesByFolderForUserParams{
			FolderID: &folderUUID,
			UserID:   userUUID,
		})
	}
	if err != nil {
		http.Error(w, "failed to list files", http.StatusInternalServerError)
		return
	}

	breadcrumbs := h.buildBreadcrumbs(ctx, folder)
	folders, files := h.buildViews(ctx, dbFolders, dbFiles)

	data := viewmodel.FileBrowserData{
		Folders:     folders,
		Files:       files,
		Breadcrumbs: breadcrumbs,
		FolderID:    folderIDStr,
	}

	renderFileBrowser(w, r, data, viewmodel.UserFromDB(db.User{
		Email: user.Email,
		Name:  user.Name,
		Role:  user.Role,
	}))
}

// POST /files/folders — create folder
func (h *FilesHandler) CreateFolder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	if !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	parentIDStr := r.FormValue("parent_id")
	var parentID *pgtype.UUID
	if parentIDStr != "" {
		u, err := viewmodel.UUIDFromString(parentIDStr)
		if err == nil {
			parentID = &u
		}
	}

	ownerUUID, err := viewmodel.UUIDFromString(user.ID)
	if err != nil {
		http.Error(w, "invalid user", http.StatusInternalServerError)
		return
	}

	_, err = h.queries.CreateFolder(ctx, db.CreateFolderParams{
		Name:     name,
		ParentID: parentID,
		OwnerID:  ownerUUID,
	})
	if err != nil {
		http.Error(w, "failed to create folder", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Folder created","type":"success"}}`)
	if parentIDStr != "" {
		http.Redirect(w, r, "/files/"+parentIDStr, http.StatusFound)
	} else {
		http.Redirect(w, r, "/files", http.StatusFound)
	}
}

// DELETE /files/folders/{folderID}
func (h *FilesHandler) DeleteFolder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	ctx := r.Context()
	folderIDStr := chi.URLParam(r, "folderID")

	folderUUID, err := viewmodel.UUIDFromString(folderIDStr)
	if err != nil {
		http.Error(w, "invalid folder id", http.StatusBadRequest)
		return
	}

	folder, err := h.queries.GetFolder(ctx, folderUUID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if !auth.CanAccess(user, folder.OwnerID.String()) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.queries.DeleteFolder(ctx, folderUUID); err != nil {
		http.Error(w, "failed to delete folder", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Folder deleted","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}

// PATCH /files/folders/{folderID}
func (h *FilesHandler) RenameFolder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	ctx := r.Context()
	folderIDStr := chi.URLParam(r, "folderID")

	folderUUID, err := viewmodel.UUIDFromString(folderIDStr)
	if err != nil {
		http.Error(w, "invalid folder id", http.StatusBadRequest)
		return
	}

	folder, err := h.queries.GetFolder(ctx, folderUUID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if !auth.CanAccess(user, folder.OwnerID.String()) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	newName := r.FormValue("name")
	if newName == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	_, err = h.queries.UpdateFolderName(ctx, db.UpdateFolderNameParams{
		Name: newName,
		ID:   folderUUID,
	})
	if err != nil {
		http.Error(w, "failed to rename folder", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Folder renamed","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}

// buildViews converts DB rows to view models, fetching item counts for folders.
func (h *FilesHandler) buildViews(ctx context.Context, dbFolders []db.Folder, dbFiles []db.File) ([]viewmodel.FolderView, []viewmodel.FileView) {
	folders := make([]viewmodel.FolderView, 0, len(dbFolders))
	for _, f := range dbFolders {
		fUUID := f.ID
		count, _ := h.queries.CountFolderItems(ctx, &fUUID)
		folders = append(folders, viewmodel.FolderFromDB(f, count))
	}
	files := make([]viewmodel.FileView, 0, len(dbFiles))
	for _, f := range dbFiles {
		uploaderName := ""
		u, err := h.queries.GetUserByID(ctx, f.UploadedBy)
		if err == nil {
			uploaderName = u.Name
		}
		files = append(files, viewmodel.FileFromDB(f, uploaderName))
	}
	return folders, files
}

// buildBreadcrumbs walks up the folder tree to build the breadcrumb path.
func (h *FilesHandler) buildBreadcrumbs(ctx context.Context, leaf db.Folder) []viewmodel.BreadcrumbItem {
	var crumbs []viewmodel.BreadcrumbItem
	current := &leaf
	for current != nil {
		crumbs = append([]viewmodel.BreadcrumbItem{
			{Name: current.Name, ID: current.ID.String(), URL: "/files/" + current.ID.String()},
		}, crumbs...)
		if current.ParentID == nil {
			break
		}
		parent, err := h.queries.GetFolder(ctx, *current.ParentID)
		if err != nil {
			break
		}
		current = &parent
	}
	return append([]viewmodel.BreadcrumbItem{{Name: "My Files", URL: "/files"}}, crumbs...)
}

func renderFileBrowser(w http.ResponseWriter, r *http.Request, data viewmodel.FileBrowserData, user viewmodel.UserView) {
	// templates.FilesPage(data, user).Render(r.Context(), w) — uncommented in Task 12
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.Error(w, "file browser – templates not yet generated", http.StatusServiceUnavailable)
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/handler/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/handler/files.go
git commit -m "feat: folder and file listing/create/delete/rename handlers"
```

---

## Task 11: CSS + Login Template

**Files:**
- Create: `web/static/app.css`
- Create: `web/templates/login.templ`

- [ ] **Step 1: Write `web/static/app.css`**

```css
:root {
  --bg: #0e1117;
  --bg-surface: #161b22;
  --bg-card: #1c2130;
  --bg-card-hover: #222840;
  --border: rgba(255, 255, 255, 0.07);
  --border-focus: rgba(240, 165, 0, 0.5);
  --text: #e6edf3;
  --text-muted: #8b949e;
  --text-faint: #484f58;
  --accent: #f0a500;
  --accent-hover: #fbbf24;
  --danger: #f85149;
  --success: #3fb950;
  --radius: 8px;
  --radius-sm: 4px;
  --font: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  --sidebar-width: 240px;
}

*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

html, body {
  height: 100%;
  background: var(--bg);
  color: var(--text);
  font-family: var(--font);
  font-size: 14px;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}

a { color: inherit; text-decoration: none; }
button { cursor: pointer; font: inherit; }

/* ── Layout ── */
.app {
  display: flex;
  height: 100vh;
  overflow: hidden;
}

.sidebar {
  width: var(--sidebar-width);
  flex-shrink: 0;
  background: var(--bg-surface);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  transition: transform 0.2s ease;
}

.main-content {
  flex: 1;
  overflow-y: auto;
  padding: 24px 28px;
}

/* ── Sidebar ── */
.sidebar-logo {
  padding: 20px 16px 12px;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: -0.5px;
  color: var(--text);
}
.sidebar-logo span { color: var(--accent); }

.sidebar-nav { padding: 8px 8px; flex: 1; }

.nav-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 13.5px;
  transition: background 0.15s, color 0.15s;
}
.nav-item:hover, .nav-item.active {
  background: var(--bg-card);
  color: var(--text);
}
.nav-item svg { width: 16px; height: 16px; flex-shrink: 0; }

.sidebar-section-label {
  padding: 12px 10px 4px;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-faint);
}

/* ── Breadcrumb ── */
.breadcrumb {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 20px;
  font-size: 13px;
  color: var(--text-muted);
}
.breadcrumb a:hover { color: var(--text); }
.breadcrumb-sep { color: var(--text-faint); }
.breadcrumb-current { color: var(--text); font-weight: 500; }

/* ── Grid ── */
.file-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 12px;
}

.section-heading {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-faint);
  margin-bottom: 10px;
  margin-top: 20px;
}
.section-heading:first-child { margin-top: 0; }

/* ── Cards ── */
.card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 14px;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s, transform 0.1s;
  position: relative;
}
.card:hover {
  background: var(--bg-card-hover);
  border-color: rgba(255,255,255,0.12);
}

.card-icon {
  width: 36px;
  height: 36px;
  border-radius: var(--radius-sm);
  background: rgba(240,165,0,0.12);
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 10px;
}
.card-icon svg { width: 20px; height: 20px; color: var(--accent); }

.card-folder-icon {
  background: rgba(96,165,250,0.12);
}
.card-folder-icon svg { color: #60a5fa; }

.card-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin-bottom: 4px;
}

.card-meta {
  font-size: 11.5px;
  color: var(--text-muted);
  display: flex;
  gap: 6px;
  align-items: center;
}
.card-meta-sep { color: var(--text-faint); }

.card-actions {
  position: absolute;
  top: 10px;
  right: 10px;
  display: none;
  gap: 4px;
}
.card:hover .card-actions { display: flex; }

.card-btn {
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 4px 8px;
  font-size: 11px;
  color: var(--text-muted);
  transition: color 0.15s, background 0.15s;
}
.card-btn:hover { color: var(--text); background: var(--bg-card); }
.card-btn-danger:hover { color: var(--danger); }

/* ── Empty State ── */
.empty-state {
  grid-column: 1/-1;
  text-align: center;
  padding: 60px 20px;
  color: var(--text-muted);
}
.empty-state svg { width: 40px; height: 40px; margin-bottom: 12px; opacity: 0.3; }
.empty-state p { font-size: 13px; }

/* ── Forms ── */
.input {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 9px 12px;
  color: var(--text);
  font: inherit;
  font-size: 14px;
  width: 100%;
  transition: border-color 0.15s;
  outline: none;
}
.input:focus { border-color: var(--border-focus); }
.input::placeholder { color: var(--text-faint); }

.btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 9px 16px;
  border-radius: var(--radius-sm);
  font-size: 13.5px;
  font-weight: 500;
  border: none;
  transition: background 0.15s, opacity 0.15s;
  white-space: nowrap;
}
.btn-primary { background: var(--accent); color: #0e1117; }
.btn-primary:hover { background: var(--accent-hover); }
.btn-ghost { background: transparent; border: 1px solid var(--border); color: var(--text-muted); }
.btn-ghost:hover { background: var(--bg-card); color: var(--text); }
.btn-danger { background: rgba(248,81,73,0.15); color: var(--danger); border: 1px solid rgba(248,81,73,0.2); }
.btn-danger:hover { background: rgba(248,81,73,0.25); }

/* ── Login Page ── */
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg);
}

.login-card {
  width: 100%;
  max-width: 400px;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 36px 32px;
}

.login-logo {
  font-size: 22px;
  font-weight: 700;
  letter-spacing: -0.5px;
  margin-bottom: 6px;
}
.login-logo span { color: var(--accent); }

.login-subtitle {
  color: var(--text-muted);
  font-size: 13px;
  margin-bottom: 28px;
}

.form-group { margin-bottom: 16px; }
.form-label {
  display: block;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--text-muted);
  margin-bottom: 6px;
  letter-spacing: 0.02em;
}

.error-msg {
  background: rgba(248,81,73,0.1);
  border: 1px solid rgba(248,81,73,0.25);
  border-radius: var(--radius-sm);
  color: var(--danger);
  padding: 9px 12px;
  font-size: 13px;
  margin-bottom: 16px;
}

/* ── Toasts ── */
.toast-container {
  position: fixed;
  bottom: 20px;
  right: 20px;
  z-index: 9999;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.toast {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 12px 16px;
  font-size: 13px;
  min-width: 240px;
  max-width: 320px;
  box-shadow: 0 4px 20px rgba(0,0,0,0.4);
  display: flex;
  align-items: center;
  gap: 8px;
  animation: slideIn 0.2s ease;
}
.toast-success { border-left: 3px solid var(--success); }
.toast-error { border-left: 3px solid var(--danger); }
.toast-info { border-left: 3px solid #60a5fa; }

@keyframes slideIn {
  from { transform: translateX(20px); opacity: 0; }
  to   { transform: translateX(0);    opacity: 1; }
}

/* ── Toolbar ── */
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
}
.toolbar-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--text);
}

/* ── Mobile ── */
@media (max-width: 768px) {
  .sidebar {
    position: fixed;
    inset: 0 auto 0 0;
    z-index: 100;
    transform: translateX(-100%);
  }
  .sidebar.open { transform: translateX(0); }
  .main-content { padding: 16px; }
  .file-grid { grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); }
}
@media (max-width: 480px) {
  .file-grid { grid-template-columns: 1fr; }
}
```

- [ ] **Step 2: Write `web/templates/login.templ`**

```templ
package templates

templ LoginPage(errorMsg string) {
	<!DOCTYPE html>
	<html lang="en">
	<head>
		<meta charset="UTF-8"/>
		<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
		<title>Login — Vaulx</title>
		<link rel="stylesheet" href="/static/app.css"/>
	</head>
	<body>
		<div class="login-page">
			<div class="login-card">
				<div class="login-logo">Vaul<span>x</span></div>
				<p class="login-subtitle">Sign in to your workspace</p>
				if errorMsg != "" {
					<div class="error-msg">{ errorMsg }</div>
				}
				<form method="POST" action="/auth/login">
					<div class="form-group">
						<label class="form-label" for="email">Email</label>
						<input
							class="input"
							type="email"
							id="email"
							name="email"
							placeholder="you@brif.africa"
							required
							autofocus
						/>
					</div>
					<div class="form-group">
						<label class="form-label" for="password">Password</label>
						<input
							class="input"
							type="password"
							id="password"
							name="password"
							placeholder="••••••••"
							required
						/>
					</div>
					<button class="btn btn-primary" style="width:100%;margin-top:8px;justify-content:center;" type="submit">
						Sign in
					</button>
				</form>
			</div>
		</div>
	</body>
	</html>
}
```

- [ ] **Step 3: Verify templ syntax (no generation yet — just check the file is valid)**

```bash
templ fmt web/templates/login.templ
```

Expected: no error output. File may be reformatted.

- [ ] **Step 4: Commit**

```bash
git add web/static/app.css web/templates/login.templ
git commit -m "feat: dark-theme CSS and login page template"
```

---

## Task 12: Layout + Sidebar Templates

**Files:**
- Create: `web/templates/layout.templ`
- Create: `web/templates/sidebar.templ`

- [ ] **Step 1: Write `web/templates/sidebar.templ`**

```templ
package templates

import "github.com/brifafrica/vaulx/internal/viewmodel"

templ Sidebar(user viewmodel.UserView) {
	<aside class="sidebar" id="sidebar">
		<div class="sidebar-logo">Vaul<span>x</span></div>
		<nav class="sidebar-nav">
			<a class="nav-item" href="/files">
				<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor">
					<path stroke-linecap="round" stroke-linejoin="round" d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z"/>
				</svg>
				My Files
			</a>
			if user.Role == "admin" {
				<div class="sidebar-section-label">Admin</div>
				<a class="nav-item" href="/admin/users">
					<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor">
						<path stroke-linecap="round" stroke-linejoin="round" d="M15 19.128a9.38 9.38 0 0 0 2.625.372 9.337 9.337 0 0 0 4.121-.952 4.125 4.125 0 0 0-7.533-2.493M15 19.128v-.003c0-1.113-.285-2.16-.786-3.07M15 19.128v.106A12.318 12.318 0 0 1 8.624 21c-2.331 0-4.512-.645-6.374-1.766l-.001-.109a6.375 6.375 0 0 1 11.964-3.07M12 6.375a3.375 3.375 0 1 1-6.75 0 3.375 3.375 0 0 1 6.75 0Zm8.25 2.25a2.625 2.625 0 1 1-5.25 0 2.625 2.625 0 0 1 5.25 0Z"/>
					</svg>
					All Users
				</a>
			}
		</nav>
		<div style="padding:12px 16px;border-top:1px solid var(--border);margin-top:auto;">
			<div style="font-size:12.5px;font-weight:500;color:var(--text);margin-bottom:2px;">{ user.Name }</div>
			<div style="font-size:11.5px;color:var(--text-muted);margin-bottom:10px;">{ user.Email }</div>
			<form method="POST" action="/auth/logout">
				<button class="btn btn-ghost" style="width:100%;justify-content:center;font-size:12.5px;">Sign out</button>
			</form>
		</div>
	</aside>
}
```

- [ ] **Step 2: Write `web/templates/layout.templ`**

```templ
package templates

import "github.com/brifafrica/vaulx/internal/viewmodel"

templ Layout(title string, user viewmodel.UserView) {
	<!DOCTYPE html>
	<html lang="en">
	<head>
		<meta charset="UTF-8"/>
		<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
		<title>{ title } — Vaulx</title>
		<link rel="stylesheet" href="/static/app.css"/>
		<script src="https://unpkg.com/htmx.org@2.0.3" integrity="sha384-0895/pl2MU10Hqc6jd4RvrthNlDiE9U1tWmX7WRESftEDRosgxNsQG/Ze9YMRzHq" crossorigin="anonymous"></script>
		<script src="https://unpkg.com/alpinejs@3.14.3/dist/cdn.min.js" defer></script>
	</head>
	<body>
		<div
			class="app"
			x-data="{ toasts: [] }"
			x-on:showToast.window="toasts.push($event.detail); setTimeout(() => toasts.shift(), 3000)"
		>
			@Sidebar(user)
			<main class="main-content">
				{ children... }
			</main>
			<div class="toast-container">
				<template x-for="(t, i) in toasts" :key="i">
					<div class="toast" :class="'toast-' + (t.type || 'info')">
						<span x-text="t.message"></span>
					</div>
				</template>
			</div>
		</div>
		<script>
			// Forward HX-Trigger showToast events to Alpine
			document.body.addEventListener("showToast", function(e) {
				window.dispatchEvent(new CustomEvent("showToast", { detail: e.detail }));
			});
		</script>
	</body>
	</html>
}
```

- [ ] **Step 3: Verify templ formatting**

```bash
templ fmt web/templates/layout.templ web/templates/sidebar.templ
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add web/templates/layout.templ web/templates/sidebar.templ
git commit -m "feat: layout shell and sidebar navigation templates"
```

---

## Task 13: File Browser, File Card, Folder Card Templates

**Files:**
- Create: `web/templates/folder_card.templ`
- Create: `web/templates/file_card.templ`
- Create: `web/templates/file_browser.templ`

- [ ] **Step 1: Write `web/templates/folder_card.templ`**

```templ
package templates

import (
	"fmt"
	"github.com/brifafrica/vaulx/internal/viewmodel"
)

templ FolderCard(folder viewmodel.FolderView) {
	<div
		class="card"
		hx-get={ "/files/" + folder.ID }
		hx-target="#browser-content"
		hx-push-url={ "/files/" + folder.ID }
		title={ folder.Name }
	>
		<div class="card-actions">
			<button
				class="card-btn"
				hx-patch={ "/files/folders/" + folder.ID }
				hx-prompt="New folder name:"
				hx-vals={ `{"name": ""}` }
				onclick="event.stopPropagation()"
			>Rename</button>
			<button
				class="card-btn card-btn-danger"
				hx-delete={ "/files/folders/" + folder.ID }
				hx-confirm={ "Delete folder \"" + folder.Name + "\"? All contents will be removed." }
				hx-target="closest .card"
				hx-swap="outerHTML swap:0.3s"
				onclick="event.stopPropagation()"
			>Delete</button>
		</div>
		<div class="card-icon card-folder-icon">
			<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor">
				<path stroke-linecap="round" stroke-linejoin="round" d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z"/>
			</svg>
		</div>
		<div class="card-name">{ folder.Name }</div>
		<div class="card-meta">
			<span>{ fmt.Sprintf("%d items", folder.ItemCount) }</span>
			<span class="card-meta-sep">·</span>
			<span>{ viewmodel.RelativeTime(folder.CreatedAt) }</span>
		</div>
	</div>
}
```

- [ ] **Step 2: Write `web/templates/file_card.templ`**

```templ
package templates

import "github.com/brifafrica/vaulx/internal/viewmodel"

templ FileCard(file viewmodel.FileView) {
	<div class="card" title={ file.Name }>
		<div class="card-actions">
			<button class="card-btn" onclick="event.stopPropagation()">Download</button>
			<button class="card-btn" onclick="event.stopPropagation()">Share</button>
		</div>
		<div class="card-icon">
			@fileTypeIcon(file.MimeType)
		</div>
		<div class="card-name">{ file.Name }</div>
		<div class="card-meta">
			<span>{ file.SizeHuman }</span>
			<span class="card-meta-sep">·</span>
			<span>{ file.UploaderName }</span>
			<span class="card-meta-sep">·</span>
			<span>{ file.RelativeDate }</span>
		</div>
	</div>
}

templ fileTypeIcon(mimeType string) {
	switch {
	case len(mimeType) >= 5 && mimeType[:5] == "video":
		<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor">
			<path stroke-linecap="round" stroke-linejoin="round" d="m15.75 10.5 4.72-4.72a.75.75 0 0 1 1.28.53v11.38a.75.75 0 0 1-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25h-9A2.25 2.25 0 0 0 2.25 7.5v9a2.25 2.25 0 0 0 2.25 2.25Z"/>
		</svg>
	case len(mimeType) >= 5 && mimeType[:5] == "image":
		<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor">
			<path stroke-linecap="round" stroke-linejoin="round" d="m2.25 15.75 5.159-5.159a2.25 2.25 0 0 1 3.182 0l5.159 5.159m-1.5-1.5 1.409-1.409a2.25 2.25 0 0 1 3.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 0 0 1.5-1.5V6a1.5 1.5 0 0 0-1.5-1.5H3.75A1.5 1.5 0 0 0 2.25 6v12a1.5 1.5 0 0 0 1.5 1.5Zm10.5-11.25h.008v.008h-.008V8.25Zm.375 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0Z"/>
		</svg>
	default:
		<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor">
			<path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z"/>
		</svg>
	}
}
```

- [ ] **Step 3: Write `web/templates/file_browser.templ`**

```templ
package templates

import "github.com/brifafrica/vaulx/internal/viewmodel"

templ FilesPage(data viewmodel.FileBrowserData, user viewmodel.UserView) {
	@Layout("My Files", user) {
		@FileBrowserContent(data, user)
	}
}

templ FileBrowserContent(data viewmodel.FileBrowserData, user viewmodel.UserView) {
	<div id="browser-content">
		<div class="toolbar">
			<div>
				@Breadcrumb(data.Breadcrumbs)
			</div>
			if user.Role == "admin" || user.Role == "editor" {
				<button
					class="btn btn-primary"
					hx-get="/fragments/new-folder-modal"
					hx-target="body"
					hx-swap="beforeend"
				>
					<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" style="width:14px;height:14px;">
						<path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15"/>
					</svg>
					New Folder
				</button>
			}
		</div>
		if len(data.Folders) == 0 && len(data.Files) == 0 {
			<div class="file-grid">
				<div class="empty-state">
					<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1" stroke="currentColor">
						<path stroke-linecap="round" stroke-linejoin="round" d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z"/>
					</svg>
					<p>No files or folders yet</p>
				</div>
			</div>
		} else {
			if len(data.Folders) > 0 {
				<p class="section-heading">Folders</p>
				<div class="file-grid">
					for _, folder := range data.Folders {
						@FolderCard(folder)
					}
				</div>
			}
			if len(data.Files) > 0 {
				<p class="section-heading">Files</p>
				<div class="file-grid">
					for _, file := range data.Files {
						@FileCard(file)
					}
				</div>
			}
		}
	</div>
}

templ Breadcrumb(crumbs []viewmodel.BreadcrumbItem) {
	<nav class="breadcrumb">
		for i, crumb := range crumbs {
			if i == len(crumbs)-1 {
				<span class="breadcrumb-current">{ crumb.Name }</span>
			} else {
				<a href={ templ.SafeURL(crumb.URL) } hx-get={ crumb.URL } hx-target="#browser-content" hx-push-url={ crumb.URL }>{ crumb.Name }</a>
				<span class="breadcrumb-sep">/</span>
			}
		}
	</nav>
}
```

- [ ] **Step 4: Run templ format on all templates**

```bash
templ fmt web/templates/
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add web/templates/folder_card.templ web/templates/file_card.templ web/templates/file_browser.templ
git commit -m "feat: file browser, file card, and folder card templ components"
```

---

## Task 14: Wire Up Handlers to Templates + Main.go

**Files:**
- Modify: `internal/handler/auth.go` (replace `renderLogin` stub)
- Modify: `internal/handler/files.go` (replace `renderFileBrowser` stub)
- Create: `cmd/server/main.go`

- [ ] **Step 1: Run `templ generate` to compile all templates**

```bash
templ generate
```

Expected: creates `web/templates/*_templ.go` files for each `.templ` file. No errors.

- [ ] **Step 2: Replace `renderLogin` stub in `internal/handler/auth.go`**

Replace the entire `renderLogin` function (the placeholder body from Task 7, Step 2):

Old:
```go
func renderLogin(w http.ResponseWriter, r *http.Request, errMsg string) {
	// Imported in Task 9 once templates exist.
	// For now: placeholder that writes plain HTML.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	// templates.LoginPage(errMsg).Render(r.Context(), w) — uncommented in Task 12
	http.Error(w, "login page – templates not yet generated", http.StatusServiceUnavailable)
}
```

New:
```go
func renderLogin(w http.ResponseWriter, r *http.Request, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	templates.LoginPage(errMsg).Render(r.Context(), w)
}
```

Also add the import at the top of `internal/handler/auth.go`:
```go
import (
	"net/http"

	"github.com/brifafrica/vaulx/internal/db"
	"github.com/brifafrica/vaulx/web/templates"
	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
)
```

- [ ] **Step 3: Replace `renderFileBrowser` stub in `internal/handler/files.go`**

Old:
```go
func renderFileBrowser(w http.ResponseWriter, r *http.Request, data viewmodel.FileBrowserData, user viewmodel.UserView) {
	// templates.FilesPage(data, user).Render(r.Context(), w) — uncommented in Task 12
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.Error(w, "file browser – templates not yet generated", http.StatusServiceUnavailable)
}
```

New:
```go
func renderFileBrowser(w http.ResponseWriter, r *http.Request, data viewmodel.FileBrowserData, user viewmodel.UserView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") == "true" {
		templates.FileBrowserContent(data, user).Render(r.Context(), w)
		return
	}
	templates.FilesPage(data, user).Render(r.Context(), w)
}
```

Also add to the imports in `internal/handler/files.go`:
```go
"github.com/brifafrica/vaulx/web/templates"
```

Also fix the `renderFileBrowser` calls in `List` and `ListFolder` — they build `viewmodel.UserView` manually. Replace those ad-hoc constructions with a proper conversion. In `List`:

Replace:
```go
renderFileBrowser(w, r, data, viewmodel.UserFromDB(db.User{
    ID:    pgtype.UUID{},
    Email: user.Email,
    Name:  user.Name,
    Role:  user.Role,
}))
```
With:
```go
renderFileBrowser(w, r, data, viewmodel.UserView{
    ID:    user.ID,
    Email: user.Email,
    Name:  user.Name,
    Role:  user.Role,
})
```

Apply the same fix in `ListFolder`.

- [ ] **Step 4: Write `cmd/server/main.go`**

```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/antonlindstrom/pgstore"
	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/db"
	"github.com/brifafrica/vaulx/internal/handler"
	"github.com/brifafrica/vaulx/internal/seed"
	"github.com/brifafrica/vaulx/internal/storage"
	"github.com/brifafrica/vaulx/migrations"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"database/sql"
)

func main() {
	_ = godotenv.Load()

	ctx := context.Background()

	runMigrations()

	db.Connect(ctx)

	if err := storage.Connect(ctx); err != nil {
		log.Printf("storage: %v (continuing — not required for Phase 1)", err)
	}

	queries := db.New(db.DB)
	seed.AdminUser(ctx, queries)

	sessionStore, err := pgstore.NewPGStore(os.Getenv("DATABASE_URL"), []byte(os.Getenv("SESSION_SECRET")))
	if err != nil {
		log.Fatalf("session store: %v", err)
	}
	defer sessionStore.StopCleanup(sessionStore.Cleanup(5 * time.Minute))
	sessionStore.Options = &gorillasessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}

	authHandler := handler.NewAuthHandler(queries, sessionStore)
	filesHandler := handler.NewFilesHandler(queries)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Static files
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// Auth routes (no session required)
	r.Route("/auth", func(r chi.Router) {
		r.Get("/login", authHandler.LoginPage)
		r.Post("/login", authHandler.LoginPage)
		r.Post("/logout", authHandler.Logout)
	})

	// Root redirect
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/files", http.StatusFound)
	})

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth(sessionStore))
		r.Get("/files", filesHandler.List)
		r.Get("/files/{folderID}", filesHandler.ListFolder)
		r.Post("/files/folders", filesHandler.CreateFolder)
		r.Delete("/files/folders/{folderID}", filesHandler.DeleteFolder)
		r.Patch("/files/folders/{folderID}", filesHandler.RenameFolder)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("server: listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func runMigrations() {
	databaseURL := os.Getenv("DATABASE_URL")

	sqlDB, err := sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatalf("migrations: open db: %v", err)
	}
	defer sqlDB.Close()

	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		log.Fatalf("migrations: driver: %v", err)
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		log.Fatalf("migrations: source: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		log.Fatalf("migrations: init: %v", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("migrations: up: %v", err)
	}
	log.Println("migrations: applied")
}
```

> **Note on import:** `gorillasessions` in the session store options line needs a named import. Add at the top:
> ```go
> gorillasessions "github.com/gorilla/sessions"
> ```
> And `sessionStore.Options` assignment uses `&gorillasessions.Options{...}`.

- [ ] **Step 5: Build the full project**

```bash
go build ./...
```

Expected: `bin/server` binary (or no output, exit 0 for `go build ./...`). Fix any import or type errors before proceeding.

Common issues to watch for:
- sqlc may generate `db.go` in `internal/db/` — rename our hand-written file to `pool.go` if there's a name clash
- pgtype nullable fields: sqlc with `emit_pointers_for_null_types: true` generates pointer types; verify the generated `models.go` matches the pointer/non-pointer usage in handlers

- [ ] **Step 6: Run all tests**

```bash
go test ./...
```

Expected: all unit tests pass.

- [ ] **Step 7: Commit**

```bash
git add cmd/server/main.go internal/handler/auth.go internal/handler/files.go
git commit -m "feat: wire all handlers, session store, migrations, and HTTP server"
```

---

## Task 15: Integration Smoke Test

This task requires a running PostgreSQL instance.

- [ ] **Step 1: Start Postgres (Docker)**

```bash
docker run -d \
  --name vaulx-pg \
  -e POSTGRES_USER=vaulx \
  -e POSTGRES_PASSWORD=vaulx \
  -e POSTGRES_DB=vaulx \
  -p 5432:5432 \
  postgres:16-alpine
```

- [ ] **Step 2: Create `.env` from `.env.example`**

```bash
cp .env.example .env
```

Edit `.env`:
```env
DATABASE_URL=postgres://vaulx:vaulx@localhost:5432/vaulx?sslmode=disable
SESSION_SECRET=dev-secret-at-least-32-bytes-long!!
SEED_ADMIN_EMAIL=admin@brif.africa
SEED_ADMIN_PASSWORD=changeme
PORT=8080
```

- [ ] **Step 3: Run the server**

```bash
go run ./cmd/server
```

Expected output:
```
migrations: applied
db: connected
seed: admin user created — email: admin@brif.africa  password: changeme
server: listening on :8080
```

- [ ] **Step 4: Verify login flow**

Open `http://localhost:8080` in a browser.

- [ ] Redirects to `/auth/login` ✓
- [ ] Login page renders with dark theme ✓
- [ ] Submit wrong password → error message shown ✓
- [ ] Submit `admin@brif.africa` / `changeme` → redirects to `/files` ✓
- [ ] `/files` shows sidebar with "My Files" nav item ✓
- [ ] Empty state grid shows (no files yet) ✓
- [ ] "Sign out" button → redirects to `/auth/login` ✓
- [ ] Accessing `/files` after logout redirects to `/auth/login` ✓

- [ ] **Step 5: Verify second run does not re-seed**

Stop and restart the server:
```bash
go run ./cmd/server
```

Expected: no `seed: admin user created` line in output.

- [ ] **Step 6: Final commit**

```bash
git add .
git commit -m "feat: Phase 1 complete — auth, file browser, seed admin, dark UI"
```

---

## Self-Review Checklist

### Spec Coverage

| Requirement | Task |
|-------------|------|
| go mod init github.com/brifafrica/vaulx | Task 1 |
| sqlc.yaml configured for PostgreSQL | Task 4 |
| Migration runner on startup | Task 14 (main.go) |
| internal/db/db.go pgxpool singleton | Task 3 |
| POST /auth/login | Task 7 |
| POST /auth/logout | Task 7 |
| GET /auth/login | Task 7 |
| Session middleware, role in context | Tasks 6, 8 |
| GET / → /files redirect | Task 14 |
| GET /files | Task 10 |
| GET /files/{folderID} | Task 10 |
| POST /files/folders (editor+) | Task 10 |
| DELETE /files/folders/{folderID} | Task 10 |
| PATCH /files/folders/{folderID} | Task 10 |
| layout.templ | Task 12 |
| sidebar.templ | Task 12 |
| file_browser.templ | Task 13 |
| file_card.templ | Task 13 |
| folder_card.templ | Task 13 |
| login.templ | Task 11 |
| Seed admin on empty users table | Task 9 |
| Dark theme UI | Task 11 (CSS) |
| HTMX + Alpine CDN in layout | Task 12 |
| Toast via HX-Trigger | Tasks 10, 12 |
| S3 client wired | Task 8 |
| Makefile generate/build/dev | Task 1 |
| .env.example | Task 1 |

All Phase 1 requirements covered. ✓

### Known Gaps / Follow-ups

- `audit_log` writes are not yet wired into handlers (spec requires them). Add `queries.CreateAuditLog(...)` calls in CreateFolder, DeleteFolder, RenameFolder, and LoginPage after each successful action.
- `GET /files/{folderID}` HTMX partial swap is wired in `renderFileBrowser`; the folder card uses `hx-target="#browser-content"` + `hx-push-url` for SPA-like navigation without full reload.
- The `New Folder` button in `FileBrowserContent` calls `GET /fragments/new-folder-modal` which is not yet implemented — replace with a simpler inline form or Alpine.js modal in a quick follow-up.
