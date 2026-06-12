package handler

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/internal/viewmodel"
)

type MultipartHandler struct {
	queries    db.Querier
	CompleteS3 func(ctx context.Context, key, uploadID string, parts []storage.Part) (string, error)
}

func NewMultipartHandler(q db.Querier) *MultipartHandler {
	return &MultipartHandler{
		queries:    q,
		CompleteS3: storage.CompleteMultipartUpload,
	}
}

// POST /api/s3/multipart
func (h *MultipartHandler) CreateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Filename    string `json:"filename"`
		ContentType string `json:"contentType"`
		FolderID    string `json:"folderId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Filename == "" {
		http.Error(w, "filename is required", http.StatusBadRequest)
		return
	}

	safe := sanitizeFilename(body.Filename)
	if safe == "" {
		safe = "file"
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	s3Key := fmt.Sprintf("uploads/%d/%02d/%x/%s", now.Year(), now.Month(), b, safe)

	uploadID, err := storage.CreateMultipartUpload(r.Context(), s3Key, body.ContentType)
	if err != nil {
		http.Error(w, "failed to initiate upload", http.StatusInternalServerError)
		return
	}

	uploaderUUID, err := viewmodel.UUIDFromString(user.ID)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusInternalServerError)
		return
	}

	var folderID pgtype.UUID
	if body.FolderID != "" {
		folderID, _ = viewmodel.UUIDFromString(body.FolderID)
	}

	var ctPtr *string
	if body.ContentType != "" {
		ct := body.ContentType
		ctPtr = &ct
	}

	file, err := h.queries.CreateFile(r.Context(), db.CreateFileParams{
		FolderID:   folderID,
		Name:       body.Filename,
		S3Key:      s3Key,
		SizeBytes:  0,
		MimeType:   ctPtr,
		UploadedBy: uploaderUUID,
	})
	if err != nil {
		http.Error(w, "failed to create file record", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"uploadId": uploadID,
		"key":      s3Key,
		"fileId":   file.ID.String(),
	})
}

// GET /api/s3/multipart/{uploadId}?key=...
func (h *MultipartHandler) ListParts(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetCurrentUser(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	uploadID := chi.URLParam(r, "uploadId")
	key := r.URL.Query().Get("key")
	if uploadID == "" || key == "" {
		http.Error(w, "uploadId and key required", http.StatusBadRequest)
		return
	}
	parts, err := storage.ListMultipartParts(r.Context(), key, uploadID)
	if err != nil {
		http.Error(w, "failed to list parts", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(parts)
}

// GET /api/s3/multipart/{uploadId}/{partNumber}?key=...
func (h *MultipartHandler) PresignPart(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetCurrentUser(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	uploadID := chi.URLParam(r, "uploadId")
	partStr := chi.URLParam(r, "partNumber")
	key := r.URL.Query().Get("key")
	partNum, err := strconv.Atoi(partStr)
	if err != nil || uploadID == "" || key == "" {
		http.Error(w, "invalid params", http.StatusBadRequest)
		return
	}
	url, err := storage.PresignUploadPart(r.Context(), key, uploadID, int32(partNum))
	if err != nil {
		http.Error(w, "failed to presign part", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": url})
}

// DELETE /api/s3/multipart/{uploadId}?key=...
func (h *MultipartHandler) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	uploadID := chi.URLParam(r, "uploadId")
	key := r.URL.Query().Get("key")
	if uploadID == "" || key == "" {
		http.Error(w, "uploadId and key required", http.StatusBadRequest)
		return
	}
	_ = storage.AbortMultipartUpload(r.Context(), key, uploadID)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/s3/multipart/{uploadId}/complete
func (h *MultipartHandler) CompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Key    string `json:"key"`
		FileID string `json:"fileId"`
		Size   int64  `json:"size"`
		Parts  []struct {
			PartNumber int32  `json:"PartNumber"`
			ETag       string `json:"ETag"`
		} `json:"parts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" || body.FileID == "" {
		http.Error(w, "key and fileId required", http.StatusBadRequest)
		return
	}

	uploadID := chi.URLParam(r, "uploadId")

	parts := make([]storage.Part, len(body.Parts))
	for i, p := range body.Parts {
		parts[i] = storage.Part{PartNumber: p.PartNumber, ETag: p.ETag}
	}

	location, err := h.CompleteS3(r.Context(), body.Key, uploadID, parts)
	if err != nil {
		http.Error(w, "failed to complete upload", http.StatusInternalServerError)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(body.FileID)
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	file, err := h.queries.UpdateFileSizeAndStatus(r.Context(), db.UpdateFileSizeAndStatusParams{
		SizeBytes: body.Size,
		Status:    "active",
		ID:        fileUUID,
	})
	if err != nil {
		http.Error(w, "failed to activate file", http.StatusInternalServerError)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	action := "file.upload"
	resourceType := "file"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       action,
		ResourceType: &resourceType,
		ResourceID:   file.ID,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"location": location})
}
