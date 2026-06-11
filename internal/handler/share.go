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
