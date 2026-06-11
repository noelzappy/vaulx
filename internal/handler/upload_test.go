package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/handler"
)

func TestInitUpload_ViewerForbidden(t *testing.T) {
	h := handler.NewUploadHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/upload/init",
		strings.NewReader(`{"name":"video.mp4","size":1024,"mime_type":"video/mp4"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "viewer"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.InitUpload(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestInitUpload_MissingName(t *testing.T) {
	h := handler.NewUploadHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/upload/init",
		strings.NewReader(`{"size":1024}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "editor"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.InitUpload(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestConfirmUpload_BadUUID(t *testing.T) {
	h := handler.NewUploadHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/upload/confirm/not-a-uuid", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "editor"})
	ctx = withChiParam(ctx, "fileID", "not-a-uuid")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.ConfirmUpload(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}
