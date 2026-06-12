package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/internal/viewmodel"
	"github.com/noelzappy/vaulx/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ShareHandler struct {
	queries db.Querier
}

func NewShareHandler(q db.Querier) *ShareHandler {
	return &ShareHandler{queries: q}
}

// POST /files/{fileID}/share — editor+ only, creates 7-day public share link
func (h *ShareHandler) CreateShare(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "fileID"))
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	// Only proceed if queries is non-nil (tests pass nil)
	if h.queries == nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	file, err := h.queries.GetFile(r.Context(), fileUUID)
	if err != nil || file.Status != "active" {
		http.NotFound(w, r)
		return
	}

	// Return existing active share if one already exists for this file
	existing, err := h.queries.GetActiveShareByFileID(r.Context(), fileUUID)
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": "/s/" + existing.Slug})
		return
	}
	// err != nil means no active share found — fall through to create a new one

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slug := hex.EncodeToString(b)

	creatorUUID, err := viewmodel.UUIDFromString(user.ID)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusInternalServerError)
		return
	}

	expiresAt := pgtype.Timestamptz{
		Time:  time.Now().UTC().Add(7 * 24 * time.Hour),
		Valid: true,
	}

	share, err := h.queries.CreateShare(r.Context(), db.CreateShareParams{
		FileID:    fileUUID,
		Slug:      slug,
		ExpiresAt: expiresAt,
		CreatedBy: creatorUUID,
	})
	if err != nil {
		http.Error(w, "failed to create share", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": "/s/" + share.Slug})
}

// GET /s/{slug} — public, no auth required
func (h *ShareHandler) ResolveShare(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		http.Error(w, "missing slug", http.StatusBadRequest)
		return
	}

	if h.queries == nil {
		http.NotFound(w, r)
		return
	}

	share, err := h.queries.GetShareBySlug(r.Context(), slug)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = templates.ErrorPage(404, "Not found",
			"This file or folder doesn't exist, or it's been deleted.",
		).Render(r.Context(), w)
		return
	}

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

	file, err := h.queries.GetFile(r.Context(), share.FileID)
	if err != nil || file.Status != "active" {
		http.NotFound(w, r)
		return
	}

	downloadURL, err := storage.PresignGET(r.Context(), file.S3Key)
	if err != nil {
		http.Error(w, "failed to generate download URL", http.StatusInternalServerError)
		return
	}

	_ = h.queries.IncrementShareViewCount(r.Context(), share.ID)
	http.Redirect(w, r, downloadURL, http.StatusFound)
}

func shareViewFrom(id, fileName, fileStatus, slug string, creatorName *string, createdAt, expiresAt pgtype.Timestamptz, viewCount int32) viewmodel.ShareView {
	creator := ""
	if creatorName != nil {
		creator = *creatorName
	}
	expires := "Never"
	expired := false
	if expiresAt.Valid {
		expires = expiresAt.Time.Format("Jan 2, 2006")
		expired = time.Now().UTC().After(expiresAt.Time)
	}
	return viewmodel.ShareView{
		ID:          id,
		FileName:    fileName,
		FileActive:  fileStatus == "active",
		Slug:        slug,
		CreatorName: creator,
		Created:     createdAt.Time.Format("Jan 2, 2006"),
		Expires:     expires,
		Expired:     expired,
		ViewCount:   viewCount,
	}
}

// GET /shares — share management. Admin sees all shares; editors see their own.
func (h *ShareHandler) SharesPage(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	if !auth.CanEdit(user) {
		http.Redirect(w, r, "/files", http.StatusFound)
		return
	}

	var views []viewmodel.ShareView
	if user.Role == "admin" {
		rows, err := h.queries.ListAllShares(r.Context())
		if err != nil {
			http.Error(w, "failed to list shares", http.StatusInternalServerError)
			return
		}
		views = make([]viewmodel.ShareView, 0, len(rows))
		for _, s := range rows {
			views = append(views, shareViewFrom(s.ID.String(), s.FileName, s.FileStatus, s.Slug, s.CreatorName, s.CreatedAt, s.ExpiresAt, s.ViewCount))
		}
	} else {
		userUUID, err := viewmodel.UUIDFromString(user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		rows, err := h.queries.ListSharesByCreator(r.Context(), userUUID)
		if err != nil {
			http.Error(w, "failed to list shares", http.StatusInternalServerError)
			return
		}
		views = make([]viewmodel.ShareView, 0, len(rows))
		for _, s := range rows {
			views = append(views, shareViewFrom(s.ID.String(), s.FileName, s.FileStatus, s.Slug, s.CreatorName, s.CreatedAt, s.ExpiresAt, s.ViewCount))
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.SharesPage(views, viewmodel.UserView{
		ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
	}).Render(r.Context(), w)
}

// DELETE /shares/{shareID} — revoke a share link. Admin or the share's creator.
func (h *ShareHandler) RevokeShare(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	shareUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "shareID"))
	if err != nil {
		http.Error(w, "invalid share id", http.StatusBadRequest)
		return
	}

	share, err := h.queries.GetShare(r.Context(), shareUUID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if user.Role != "admin" && share.CreatedBy.String() != user.ID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.queries.RevokeShare(r.Context(), shareUUID); err != nil {
		http.Error(w, "failed to revoke share", http.StatusInternalServerError)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	resourceType := "share"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       "share.revoke",
		ResourceType: &resourceType,
		ResourceID:   shareUUID,
	})

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Share link revoked","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}
