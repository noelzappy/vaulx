package handler

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/db"
	"github.com/brifafrica/vaulx/internal/storage"
	"github.com/brifafrica/vaulx/internal/viewmodel"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type UploadHandler struct {
	queries db.Querier
}

func NewUploadHandler(q db.Querier) *UploadHandler {
	return &UploadHandler{queries: q}
}

// POST /api/upload/init
// Body: { "name": "video.mp4", "size": 123456, "mime_type": "video/mp4", "folder_id": "uuid-or-empty" }
// Returns: { "file_id": "uuid", "upload_url": "https://..." }
func (h *UploadHandler) InitUpload(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	if !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		MimeType string `json:"mime_type"`
		FolderID string `json:"folder_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Build a unique S3 key: files/<16-hex-bytes>/<sanitized-name>
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	safe := sanitizeFilename(req.Name)
	if safe == "" {
		safe = "file"
	}
	s3Key := fmt.Sprintf("files/%x/%s", b, safe)

	// Resolve folder UUID (zero value = NULL = root)
	var folderID pgtype.UUID
	if req.FolderID != "" {
		folderID, _ = viewmodel.UUIDFromString(req.FolderID)
	}

	uploaderUUID, err := viewmodel.UUIDFromString(user.ID)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusInternalServerError)
		return
	}

	var mimeType *string
	if req.MimeType != "" {
		mimeType = &req.MimeType
	}

	mt := ""
	if mimeType != nil {
		mt = *mimeType
	}
	uploadURL, err := storage.PresignPUT(r.Context(), s3Key, mt, req.Size)
	if err != nil {
		http.Error(w, "failed to generate upload URL", http.StatusInternalServerError)
		return
	}

	file, err := h.queries.CreateFile(r.Context(), db.CreateFileParams{
		FolderID:   folderID,
		Name:       req.Name,
		S3Key:      s3Key,
		SizeBytes:  req.Size,
		MimeType:   mimeType,
		UploadedBy: uploaderUUID,
	})
	if err != nil {
		http.Error(w, "failed to create file record", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"file_id":    file.ID.String(),
		"upload_url": uploadURL,
	})
}

// POST /api/upload/confirm/{fileID}
// Marks the file as active after the browser has finished the S3 PUT.
func (h *UploadHandler) ConfirmUpload(w http.ResponseWriter, r *http.Request) {
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

	file, err := h.queries.ActivateFile(r.Context(), fileUUID)
	if err != nil {
		http.Error(w, "file not found or already active", http.StatusNotFound)
		return
	}

	// Only the uploader or an admin can confirm their own upload
	if user.Role != "admin" && file.UploadedBy.String() != user.ID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	uploaderUUID, _ := viewmodel.UUIDFromString(user.ID)
	action := "file.upload"
	resourceType := "file"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       uploaderUUID,
		Action:       action,
		ResourceType: &resourceType,
		ResourceID:   file.ID,
		Meta:         nil,
	})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("HX-Trigger", `{"showToast":{"message":"File uploaded","type":"success"}}`)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "file_id": file.ID.String()})
}

func sanitizeFilename(name string) string {
	if idx := strings.LastIndexAny(name, "/\\"); idx >= 0 {
		name = name[idx+1:]
	}
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
