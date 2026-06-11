package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/handler"
)

func TestDownload_BadUUID(t *testing.T) {
	h := handler.NewDownloadHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/files/bad-uuid/download", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "admin"})
	ctx = withChiParam(ctx, "fileID", "not-a-uuid")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.Download(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}
