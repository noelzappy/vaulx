package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/db"
	"github.com/brifafrica/vaulx/internal/storage"
	"github.com/brifafrica/vaulx/internal/viewmodel"
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
		http.NotFound(w, r)
		return
	}

	if share.ExpiresAt.Valid && time.Now().UTC().After(share.ExpiresAt.Time) {
		http.Error(w, "share link has expired", http.StatusGone)
		return
	}

	if share.MaxViews != nil && share.ViewCount >= *share.MaxViews {
		http.Error(w, "share link has reached its view limit", http.StatusGone)
		return
	}

	file, err := h.queries.GetFile(r.Context(), share.FileID)
	if err != nil || file.Status != "active" {
		http.NotFound(w, r)
		return
	}

	_ = h.queries.IncrementShareViewCount(r.Context(), share.ID)

	downloadURL, err := storage.PresignGET(r.Context(), file.S3Key)
	if err != nil {
		http.Error(w, "failed to generate download URL", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, downloadURL, http.StatusFound)
}
