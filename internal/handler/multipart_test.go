package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/handler"
	"github.com/noelzappy/vaulx/internal/storage"
)

type multipartQuerier struct {
	db.Querier
	updatedStatus string
	updatedSize   int64
}

func (m *multipartQuerier) CreateFile(ctx context.Context, arg db.CreateFileParams) (db.File, error) {
	return db.File{
		ID:     pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		S3Key:  arg.S3Key,
		Status: "pending",
	}, nil
}

func (m *multipartQuerier) GetFile(ctx context.Context, id pgtype.UUID) (db.File, error) {
	return db.File{
		ID:         id,
		S3Key:      "uploads/2026/06/test-key/file.mp4",
		Status:     "pending",
		UploadedBy: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
	}, nil
}

func (m *multipartQuerier) UpdateFileSizeAndStatus(ctx context.Context, arg db.UpdateFileSizeAndStatusParams) (db.File, error) {
	m.updatedStatus = arg.Status
	m.updatedSize = arg.SizeBytes
	return db.File{ID: arg.ID, Status: arg.Status, SizeBytes: arg.SizeBytes}, nil
}

func (m *multipartQuerier) SoftDeleteFile(ctx context.Context, id pgtype.UUID) error {
	return nil
}

func (m *multipartQuerier) CreateAuditLog(ctx context.Context, arg db.CreateAuditLogParams) (db.AuditLog, error) {
	return db.AuditLog{}, nil
}

func TestCreateMultipartUpload_ViewerForbidden(t *testing.T) {
	h := handler.NewMultipartHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/s3/multipart",
		strings.NewReader(`{"filename":"video.mp4","contentType":"video/mp4"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "viewer"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.CreateMultipartUpload(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for viewer, got %d", rr.Code)
	}
}

func TestCompleteMultipartUpload_SetsStatusActive(t *testing.T) {
	mock := &multipartQuerier{}
	h := handler.NewMultipartHandler(mock)
	h.CompleteS3 = func(ctx context.Context, key, uploadID string, parts []storage.Part) (string, error) {
		return "https://s3.example.com/location", nil
	}

	body := `{"key":"uploads/2026/06/test/file.mp4","fileId":"01000000-0000-0000-0000-000000000000","parts":[{"PartNumber":1,"ETag":"\"abc123\""}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/s3/multipart/upload-id-123/complete",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "editor"})
	ctx = withChiParam(ctx, "uploadId", "upload-id-123")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.CompleteMultipartUpload(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if mock.updatedStatus != "active" {
		t.Errorf("expected status 'active', got %q", mock.updatedStatus)
	}
}
