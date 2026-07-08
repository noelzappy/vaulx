package handler

import (
	"context"
	"fmt"
	"html"
	"log"
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
	// Fetch and Presign are handler seams so tests can substitute in-memory
	// objects and canned URLs.
	Fetch   zipbuild.FetchFunc
	Presign func(ctx context.Context, key, filename string) (string, error)
}

func NewZipHandler(q db.Querier, sh *ShareHandler) *ZipHandler {
	return &ZipHandler{
		queries: q,
		shares:  sh,
		Fetch:   storage.GetObjectStream,
		Presign: storage.PresignGETDownload,
	}
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
		log.Printf("zip stream %s: %v", folder.Name, err)
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

// EntriesForWorker adapts collectEntries for the background worker (no
// owner filter: prepared zips carry the folder's full active tree — the
// dashboard prepare button sits behind auth like streaming, and share
// prepares are tree-scoped by resolveShareForZip).
func (h *ZipHandler) EntriesForWorker() func(ctx context.Context, folderID pgtype.UUID) ([]zipbuild.Entry, error) {
	return func(ctx context.Context, folderID pgtype.UUID) ([]zipbuild.Entry, error) {
		entries, _, err := h.collectEntries(ctx, folderID, nil)
		return entries, err
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
		fmt.Fprintf(w, `<div class="zip-status">Zip failed: %s</div>`, html.EscapeString(msg))
	default: // pending / running
		fmt.Fprintf(w, `<div class="zip-status" hx-get="%s" hx-trigger="every 3s" hx-swap="outerHTML">Preparing zip…</div>`, statusURL)
	}
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
	if _, ok := auth.GetCurrentUser(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
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
