# Vaulx

A private file asset platform. Upload, organise, and share files with role-based access control and a clean, fast interface.

## Features

- **File management** — Upload directly to S3/Hetzner Object Storage via presigned URLs, browse files in a folder hierarchy, inline rename, soft-delete
- **Access control** — Three roles: `admin`, `editor`, `viewer`; per-resource permission grants
- **Share links** — Generate 7-day public share links with view-count tracking and expiry enforcement
- **Admin tools** — User management, audit log viewer with action filtering, permission grants
- **Audit trail** — Every login, upload, delete, rename, and folder operation is logged

## Tech Stack

| Layer | Tech |
|-------|------|
| Language | Go 1.25 |
| Router | Chi v5 |
| Templates | a-h/templ (server-side components) |
| Frontend | HTMX 2.0.3 + Alpine.js 3.14.3 (no build step) |
| Database | PostgreSQL 16 via pgx/v5 + sqlc |
| Sessions | gorilla/sessions backed by Postgres (pgstore) |
| Storage | S3-compatible (Hetzner Object Storage) via AWS SDK Go v2 |
| Migrations | golang-migrate (embedded via `embed.FS`) |

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | Yes | Postgres connection string, e.g. `postgres://user:pass@host:5432/db?sslmode=disable` |
| `SESSION_SECRET` | Yes | Random string ≥ 32 chars used to sign session cookies |
| `PORT` | No | HTTP port (default: `8080`) |
| `SEED_ADMIN_EMAIL` | Yes | Email for the seeded admin account created on first boot |
| `SEED_ADMIN_PASSWORD` | Yes | Password for the seeded admin (min 8 chars, at least one digit) |
| `HETZNER_ACCESS_KEY` | Yes | Hetzner Object Storage access key |
| `HETZNER_SECRET_KEY` | Yes | Hetzner Object Storage secret key |
| `HETZNER_S3_ENDPOINT` | Yes | Storage endpoint, e.g. `https://fsn1.your-objectstorage.com` |
| `HETZNER_BUCKET` | Yes | Bucket name |

## Local Development

**Prerequisites:** Go 1.25, PostgreSQL, [templ CLI](https://templ.guide/quick-start/installation), [sqlc](https://sqlc.dev/) (for schema changes only)

```bash
# 1. Start Postgres
docker run -d --name vaulx-dev \
  -e POSTGRES_USER=vaulx -e POSTGRES_PASSWORD=vaulx -e POSTGRES_DB=vaulx \
  -p 5432:5432 postgres:16

# 2. Copy and edit env
cp .env.example .env   # edit credentials as needed

# 3. Generate templ components (only needed after .templ file changes)
templ generate

# 4. Run the server (auto-runs migrations and seeds admin on first start)
go run ./cmd/server
```

The server is available at `http://localhost:8080`. Log in with the credentials from `SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD`.

### After changing SQL queries

```bash
sqlc generate   # regenerates internal/db/*.go
```

### After changing .templ files

```bash
templ generate  # regenerates web/templates/*_templ.go
```

## Docker

Build and run with Docker Compose:

```bash
docker compose up --build
```

The app will be at `http://localhost:8080`.

> Set `SESSION_SECRET` to a random value before use. The default in `docker-compose.yml` is a placeholder.

## Deployment on Dokploy

Vaulx ships as a single Docker image with all migrations embedded. Recommended setup:

### 1. Create a Postgres service in Dokploy

Use the built-in Postgres service (or an external managed database). Note the connection string.

### 2. Create an application in Dokploy

- **Source:** your Git repository
- **Build type:** Dockerfile (uses `Dockerfile` at repo root)
- **Port:** `8080`

### 3. Set environment variables in Dokploy

```
DATABASE_URL=postgres://user:pass@your-pg-host:5432/vaulx?sslmode=disable
SESSION_SECRET=<random 32+ char string>
PORT=8080
SEED_ADMIN_EMAIL=admin@yourdomain.com
SEED_ADMIN_PASSWORD=<strong password>
HETZNER_ACCESS_KEY=<your key>
HETZNER_SECRET_KEY=<your secret>
HETZNER_S3_ENDPOINT=https://fsn1.your-objectstorage.com
HETZNER_BUCKET=<your bucket>
```

### 4. Deploy

Trigger a deploy from Dokploy. On first start, the app will:
1. Run all pending migrations
2. Seed the admin account if it doesn't exist
3. Start serving on `$PORT`

### 5. (Recommended) Add a domain and enable HTTPS in Dokploy

The session cookie has `Secure: true`, so HTTPS is required for the login flow to work in production.

## Project Structure

```
.
├── cmd/server/          # main.go — router setup, handler wiring
├── internal/
│   ├── auth/            # session middleware, ACL helpers, UserContext
│   ├── db/              # sqlc-generated code + querier interface
│   │   └── queries/     # SQL source files
│   ├── handler/         # HTTP handlers (files, upload, download, share, admin, permissions)
│   ├── seed/            # Admin user seeding on first boot
│   ├── storage/         # S3 client, presign helpers, DeleteObject
│   └── viewmodel/       # View-layer structs and mapping functions
├── migrations/          # SQL migration files (embedded into binary)
├── web/
│   ├── static/          # CSS, JS (served directly)
│   └── templates/       # .templ components + generated Go code
├── Dockerfile
└── docker-compose.yml
```

## Default Credentials

On first boot the server seeds one admin account using the `SEED_ADMIN_EMAIL` and `SEED_ADMIN_PASSWORD` env vars. Change these before exposing the app publicly.

## Roles

| Role | Can do |
|------|--------|
| `admin` | Everything — manage users, hard-delete files, grant permissions, view audit log |
| `editor` | Upload files, create/rename/delete folders they own, rename/soft-delete their own files, create share links |
| `viewer` | Browse and download files they have access to |
