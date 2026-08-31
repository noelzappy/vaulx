package handler

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/internal/viewmodel"
)

// BulkMove handles POST /files/bulk/move. It moves every selected active file
// to the given folder (or root when folder_id is empty), rejecting the whole
// batch if any file isn't editable by the actor (admin or the file's uploader).
func (h *FilesHandler) BulkMove(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		FileIDs  []string `json:"file_ids"`
		FolderID string   `json:"folder_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.FileIDs) == 0 {
		http.Error(w, "file_ids required", http.StatusBadRequest)
		return
	}

	ids, err := parseUUIDs(body.FileIDs)
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	var folderID pgtype.UUID
	if body.FolderID != "" {
		folderID, err = viewmodel.UUIDFromString(body.FolderID)
		if err != nil {
			http.Error(w, "invalid folder id", http.StatusBadRequest)
			return
		}
	}

	files, err := h.queries.GetFilesByIDs(r.Context(), ids)
	if err != nil {
		http.Error(w, "failed to load files", http.StatusInternalServerError)
		return
	}
	if len(files) != len(ids) {
		http.Error(w, "one or more files not found", http.StatusNotFound)
		return
	}

	for _, f := range files {
		if f.Status != "active" {
			http.Error(w, "one or more files are not active", http.StatusBadRequest)
			return
		}
		if user.Role != "admin" && (!f.UploadedBy.Valid || f.UploadedBy.String() != user.ID) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	if err := h.queries.MoveFilesToFolder(r.Context(), db.MoveFilesToFolderParams{
		FolderID: folderID,
		FileIds:  ids,
	}); err != nil {
		http.Error(w, "failed to move files", http.StatusInternalServerError)
		return
	}

	writeBulkAudit(r, h.queries, user.ID, "file.move", "file", files)
	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Files moved","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}

// BulkDelete handles POST /files/bulk/delete. Editors soft-delete files they
// uploaded; admins may also permanently delete any selected file (which removes
// the original and its thumbnail from storage).
func (h *FilesHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		FileIDs   []string `json:"file_ids"`
		Permanent bool     `json:"permanent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.FileIDs) == 0 {
		http.Error(w, "file_ids required", http.StatusBadRequest)
		return
	}
	if body.Permanent && user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	ids, err := parseUUIDs(body.FileIDs)
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	files, err := h.queries.GetFilesByIDs(r.Context(), ids)
	if err != nil {
		http.Error(w, "failed to load files", http.StatusInternalServerError)
		return
	}
	if len(files) != len(ids) {
		http.Error(w, "one or more files not found", http.StatusNotFound)
		return
	}

	for _, f := range files {
		if f.Status != "active" {
			http.Error(w, "one or more files are not active", http.StatusBadRequest)
			return
		}
		if user.Role != "admin" && (!f.UploadedBy.Valid || f.UploadedBy.String() != user.ID) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	if body.Permanent {
		for _, f := range files {
			_ = storage.DeleteObject(r.Context(), f.S3Key) // original may already be gone
			if f.ThumbS3Key != nil && *f.ThumbS3Key != "" {
				_ = storage.DeleteObject(r.Context(), *f.ThumbS3Key)
			}
		}
		if err := h.queries.HardDeleteFilesMany(r.Context(), ids); err != nil {
			http.Error(w, "failed to delete files", http.StatusInternalServerError)
			return
		}
	} else {
		if err := h.queries.SoftDeleteFilesMany(r.Context(), ids); err != nil {
			http.Error(w, "failed to delete files", http.StatusInternalServerError)
			return
		}
	}

	action := "file.delete"
	if body.Permanent {
		action = "file.hard_delete"
	}
	writeBulkAudit(r, h.queries, user.ID, action, "file", files)
	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Files deleted","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}

func parseUUIDs(ids []string) ([]pgtype.UUID, error) {
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		u, err := viewmodel.UUIDFromString(id)
		if err != nil {
			return nil, err
		}
		out[i] = u
	}
	return out, nil
}

func writeBulkAudit(r *http.Request, q db.Querier, actorID, action, resourceType string, files []db.File) {
	userUUID, _ := viewmodel.UUIDFromString(actorID)
	for _, f := range files {
		_, _ = q.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
			UserID:       userUUID,
			Action:       action,
			ResourceType: &resourceType,
			ResourceID:   f.ID,
		})
	}
}
