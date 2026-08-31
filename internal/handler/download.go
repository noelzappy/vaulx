package handler

import (
	"net/http"

	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/internal/viewmodel"
	"github.com/noelzappy/vaulx/web/templates"
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
		w.WriteHeader(http.StatusNotFound)
		_ = templates.AuthErrorPage(404, "Not found",
			"This file or folder doesn't exist, or it's been deleted.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	if file.Status != "active" {
		w.WriteHeader(http.StatusNotFound)
		_ = templates.AuthErrorPage(404, "Not found",
			"This file or folder doesn't exist, or it's been deleted.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	if !auth.CanAccess(user, file.UploadedBy.String()) {
		w.WriteHeader(http.StatusForbidden)
		_ = templates.AuthErrorPage(403, "Access denied",
			"You don't have permission to view this. Contact your admin to request access.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	downloadURL, err := storage.PresignGETDownload(r.Context(), file.S3Key, file.Name)
	if err != nil {
		http.Error(w, "failed to generate download URL", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, downloadURL, http.StatusFound)
}
