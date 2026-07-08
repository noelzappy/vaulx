package handler

import (
	"context"
	"fmt"
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
