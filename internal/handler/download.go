package handler

import (
	"net/http"

	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/internal/viewmodel"
	"github.com/go-chi/chi/v5"
)

type DownloadHandler struct {
	queries db.Querier
}

func NewDownloadHandler(q db.Querier) *DownloadHandler {
	return &DownloadHandler{queries: q}
}

// GET /files/{fileID}/download
// Generates a presigned S3 GET URL and redirects the browser to it.
func (h *DownloadHandler) Download(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "fileID"))
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	file, err := h.queries.GetFile(r.Context(), fileUUID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if file.Status != "active" {
		http.NotFound(w, r)
		return
	}

	if !auth.CanAccess(user, file.UploadedBy.String()) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	downloadURL, err := storage.PresignGET(r.Context(), file.S3Key)
	if err != nil {
		http.Error(w, "failed to generate download URL", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, downloadURL, http.StatusFound)
}
