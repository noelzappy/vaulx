# Shared File Landing Page

**Date:** 2026-06-23
**Status:** Approved

## Problem

When a Vaulx file share link (`/s/{slug}`) is opened, the server presigns a Hetzner/S3 URL and issues an HTTP 302 redirect directly to it. The browser renders or plays the file inline (or downloads it automatically). On mobile, there is no download affordance — the file opens in-browser with no save option. There is no Vaulx-branded page, no copy-link button, and no explicit download action.

Folder shares already have a proper landing page (`SharedFolderPage`). File shares do not.

## Goal

Replace the raw S3 redirect for file shares with a Vaulx-branded landing page that:
- Renders an inline preview of the file content
- Provides a clear Download button that forces a file-save on all devices (including mobile)
- Provides a Copy Link button
- Shows file metadata (name, size, type, expiry)

## Architecture

### New Route

```
GET /s/{slug}/download
```

Registered alongside the existing public share routes (no auth). Handled by `ShareHandler.SharedFileDirectDownload`.

### Modified Route

```
GET /s/{slug}
```

For **file** shares: previously redirected to a presigned S3 URL. Now renders `SharedFilePage`. Folder shares are unchanged.

### New Storage Function

`internal/storage/presign.go` — add `PresignGETDownload(ctx, key, filename string)`:

```go
func PresignGETDownload(ctx context.Context, key, filename string) (string, error) {
    // Strip quotes and backslashes from filename to avoid breaking the header value.
    safe := strings.NewReplacer(`"`, ``, `\`, ``).Replace(filename)
    req, err := PresignClient.PresignGetObject(ctx, &s3.GetObjectInput{
        Bucket:                     aws.String(Bucket),
        Key:                        aws.String(key),
        ResponseContentDisposition: aws.String(`attachment; filename="` + safe + `"`),
    }, s3.WithPresignExpires(15*time.Minute))
    ...
}
```

The `ResponseContentDisposition: attachment` header override instructs the browser to save the file rather than render it inline. This is part of the S3/Hetzner presigned URL spec and requires no server-side streaming.

### Modified Handler: `ResolveShare` (file path)

```
GET /s/{slug}
 → GetShareBySlug
 → check expiry / max-views (existing guards, unchanged)
 → if folder share → existing resolveFolderShare (unchanged)
 → GetFile
 → storage.PresignGET(ctx, file.S3Key)       ← preview URL (no content-disposition)
 → IncrementShareViewCount
 → render SharedFilePage(slug, fileView, previewURL, expiresAt)
```

### New Handler: `SharedFileDirectDownload`

```
GET /s/{slug}/download
 → GetShareBySlug
 → check expiry / max-views (same guards as ResolveShare)
 → validate share.FolderID is NOT valid (file-only shares)
 → GetFile (must be active)
 → storage.PresignGETDownload(ctx, file.S3Key, file.Name)
 → http.Redirect(302) → Hetzner URL (browser saves file)
```

No view count increment on download (already counted on page load).

## Template: `SharedFilePage`

New file: `web/templates/shared_file.templ`

Function signature:
```go
templ SharedFilePage(slug string, file viewmodel.FileView, previewURL string, expires string)
```

### Layout

```
┌─────────────────────────────────────────────┐
│  VaulX                                      │
│  document.pdf · Shared file · read only     │
├─────────────────────────────────────────────┤
│                                             │
│  ┌───────────────────────────────────────┐  │
│  │  [inline preview]                     │  │
│  │   <img> / <video> / <audio> / <embed> │  │
│  │   or: file-type icon for other types  │  │
│  └───────────────────────────────────────┘  │
│                                             │
│  document.pdf                               │
│  2.4 MB  ·  PDF  ·  Expires Jan 1, 2026    │
│                                             │
│  [↓ Download]       [⎘ Copy link]          │
│                                             │
└─────────────────────────────────────────────┘
```

### Preview Logic (by MIME type prefix)

| MIME prefix           | Rendered as                          |
|-----------------------|--------------------------------------|
| `image/*`             | `<img src=previewURL>`               |
| `video/*`             | `<video controls src=previewURL>`    |
| `audio/*`             | `<audio controls src=previewURL>`    |
| `application/pdf`     | `<iframe src=previewURL height=500>` |
| everything else       | Large `fileTypeIcon` + `.ext` badge  |

The `previewURL` is generated at page-render time (15 min TTL). For the inline preview, no `ResponseContentDisposition` override is set so the browser can render it natively.

### Actions

**Download button**: `<a href="/s/{slug}/download" class="btn btn-primary">↓ Download</a>` — plain anchor, no JS.

**Copy link button**: calls `navigator.clipboard.writeText(window.location.href)`. Button label flips to "Copied!" for 2 seconds via a small inline `<script>` block. No Alpine.js dependency (public page).

### Metadata row

Displays: `{file.Name}` / `{file.SizeHuman}` / MIME label (e.g., "PDF", "JPEG", "MP4") / `Expires {expires}` or "No expiry".

The `expires` string is formatted by the handler from `share.ExpiresAt` ("Jan 1, 2026") or "No expiry" if not set.

## CSS

Add to `web/static/app.css` using existing CSS variables (`--bg-card`, `--border`, `--radius`, `--text`, `--text-muted`, etc.):

```
.shared-file-preview     — preview card container
.shared-file-preview img — max-width:100%, border-radius
.shared-file-preview video / audio — width:100%
.shared-file-preview iframe — width:100%, height:500px, border:none
.shared-file-no-preview  — centered icon+ext for non-previewable types
.shared-file-meta        — name/size/type/expiry block below preview
.shared-file-name        — file name heading
.shared-file-info        — small metadata row (size · type · expiry)
.shared-file-actions     — row of action buttons with gap
```

## Error Handling

All error paths reuse the existing `templates.ErrorPage`:
- Share not found → 404
- Share expired or max-views hit → 410 "Link expired"
- File not active → 404
- Storage presign failure → 500

`SharedFileDirectDownload` mirrors the same checks as `ResolveShare`.

## Out of Scope

- Preview page for files inside shared **folder** shares (those still redirect directly)
- Expiry display on the shared folder page
- Share analytics / view tracking per download (view count increments on page load only)

## Files Changed

| File | Change |
|------|--------|
| `web/templates/shared_file.templ` | New — SharedFilePage template |
| `web/templates/shared_file_templ.go` | Generated by `templ generate` |
| `internal/storage/presign.go` | Add `PresignGETDownload` |
| `internal/handler/share.go` | Modify `ResolveShare` file path; add `SharedFileDirectDownload` |
| `cmd/server/main.go` | Register `GET /s/{slug}/download` |
| `web/static/app.css` | Add `.shared-file-*` CSS classes |
