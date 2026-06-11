package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/handler"
)

func TestDeleteFile_HardDelete_NonAdminForbidden(t *testing.T) {
	h := handler.NewFilesHandler(nil)

	for _, role := range []string{"editor", "viewer"} {
		req := httptest.NewRequest(http.MethodDelete, "/files/some-id?permanent=true", nil)
		ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: role})
		ctx = withChiParam(ctx, "fileID", "some-id")
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		h.DeleteFile(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("role %q: expected 403, got %d", role, rr.Code)
		}
	}
}

func TestRenameFile_ViewerForbidden(t *testing.T) {
	h := handler.NewFilesHandler(nil)

	req := httptest.NewRequest(http.MethodPatch, "/files/some-id/name", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "viewer"})
	ctx = withChiParam(ctx, "fileID", "some-id")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.RenameFile(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestRenameFile_BadUUID(t *testing.T) {
	h := handler.NewFilesHandler(nil)

	req := httptest.NewRequest(http.MethodPatch, "/files/not-a-uuid/name", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "editor"})
	ctx = withChiParam(ctx, "fileID", "not-a-uuid")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.RenameFile(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}
