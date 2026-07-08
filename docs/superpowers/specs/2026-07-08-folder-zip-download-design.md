# Folder Zip Download — Design

**Date:** 2026-07-08
**Status:** Approved

## Goal

Download an entire folder — every active file in it, recursively, regardless of pagination — as a single zip. Available on the authenticated dashboard and on public shared-folder pages. Two paths:

1. **Streaming zip** — instant download, assembled on the fly.
2. **Prepared zip** — background job writes the zip to the bucket; user downloads via presigned URL (resumable via HTTP range).

Both paths are offered as separate actions in the UI ("Download zip" / "Prepare zip"). Prepared zips are available to both dashboard users and anonymous share visitors, with abuse controls.

## Endpoints

| Route | Auth | Purpose |
|---|---|---|
| `GET /files/{folderID}/zip` | session | Stream zip of folder |
| `POST /files/{folderID}/zip/prepare` | session | Start/reuse prepared-zip job |
| `GET /files/{folderID}/zip/status` | session | Job status fragment (htmx poll) |
| `GET /s/{slug}/zip?folder=<id>` | share slug | Stream zip; `folder` defaults to share root, must pass `folderInTree` |
| `POST /s/{slug}/zip/prepare?folder=<id>` | share slug + IP rate limit | Start/reuse job |
| `GET /s/{slug}/zip/status?folder=<id>` | share slug | Job status fragment |

Share routes revalidate slug status/expiry exactly like existing `SharedFileDownload`.

## Streaming zip

- One recursive CTE (sqlc) returns every active (`status = 'active'`, not soft-deleted) file under the folder: id, name, s3_key, size, and relative directory path derived from the folder tree.
- Handler sets `Content-Type: application/zip`, `Content-Disposition: attachment; filename="<sanitized-folder-name>.zip"`, then per file: S3 `GetObject` → `io.Copy` into an `archive/zip` entry on the response writer.
- **Store method** (no compression) — content is mostly media/binary; zip64 handled by stdlib for >4GB.
- Duplicate filenames within the same directory get ` (2)`, ` (3)` … suffixes (before extension).
- Empty subfolders are written as directory entries so the extracted tree matches.
- Mid-stream S3 failure: log, stop writing — client receives a truncated archive (headers already sent; unavoidable). The prepared path is the remedy for flaky connections.
- Audit log `file.zip_download` (resource = folder) on start, same pattern as single-file download logging.

## Prepared zip

### Data

```sql
CREATE TABLE zip_jobs (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  folder_id   UUID NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  share_id    UUID REFERENCES shares(id) ON DELETE SET NULL, -- set when requested via a share page
  status      TEXT NOT NULL DEFAULT 'pending',               -- pending | running | ready | failed
  s3_key      TEXT,
  size_bytes  BIGINT NOT NULL DEFAULT 0,
  error       TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at  TIMESTAMPTZ
);
CREATE INDEX idx_zip_jobs_folder_id ON zip_jobs(folder_id);
```

### Lifecycle

- **Prepare** returns an existing job when one is `pending`/`running` for the folder, or `ready`, unexpired, and `created_at` newer than the folder's latest content change (max of file created/deleted timestamps under the tree, from the same recursive query). Otherwise inserts `pending`.
- A worker pool inside the server process (global cap: **2 concurrent** `running` jobs; excess stays `pending` and is picked up FIFO) builds the zip with the same recursive listing, streaming into the bucket via the existing multipart-upload helpers — constant memory. Key: `zips/<folderID>/<jobID>.zip`.
- On success: `status = 'ready'`, `expires_at = now() + 24h`. On failure: `status = 'failed'` + error, multipart upload aborted.
- **Download**: status endpoint renders a presigned GET (existing `PresignGETDownload`) when ready — S3 range support makes it browser-resumable.
- **Cleanup**: ticker goroutine (hourly) deletes expired zip objects and marks rows expired/failed. On startup, stale `running` rows (from a killed process) are marked `failed`.
- **Rate limiting**: `httprate.LimitByIP` on the share prepare route (e.g. 5/min). Dashboard prepare is behind auth; job dedupe already collapses repeat clicks.

## UI

- **Dashboard folder view**: toolbar gains "Download zip" (link to stream route) and "Prepare zip" (htmx POST). After prepare, a status chip polls the status fragment every ~3s (`hx-trigger="every 3s"`) until it swaps to "Ready — download (expires in Xh)" or an error message.
- **Shared folder page**: same two actions for the folder currently in view (root or validated subfolder).

## Testing

- Handler tests following the `multipart_test.go` mock-Querier pattern: recursive listing → zip entries, name dedupe, empty folder, share-tree validation (403 outside tree, expired share), job reuse logic, global concurrency cap, stale-`running` recovery.
- Zip correctness asserted by opening the produced archive with `archive/zip` in tests.
- End-to-end via the project verify skill (headless Chrome; real bucket; clean up `zips/` objects after).

## Out of scope

- Compression (store-only).
- Download progress totals for the streaming path (no Content-Length by design).
- Zipping arbitrary multi-select file sets; folders only.
