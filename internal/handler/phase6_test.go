package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/handler"
)

func TestTrash_NonAdminForbidden(t *testing.T) {
	h := handler.NewAdminHandler(nil)

	for _, role := range []string{"editor", "viewer"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/trash", nil)
		ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: role})
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		h.Trash(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("role %q: expected 403, got %d", role, rr.Code)
		}
	}
}

func TestRestoreFile_NonAdminForbidden(t *testing.T) {
	h := handler.NewAdminHandler(nil)

	for _, role := range []string{"editor", "viewer"} {
		req := httptest.NewRequest(http.MethodPost, "/admin/trash/some-id/restore", nil)
		ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: role})
		ctx = withChiParam(ctx, "fileID", "some-id")
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		h.RestoreFile(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("role %q: expected 403, got %d", role, rr.Code)
		}
	}
}

func TestRevokeShare_ViewerForbidden(t *testing.T) {
	h := handler.NewShareHandler(nil)

	req := httptest.NewRequest(http.MethodDelete, "/shares/some-id", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "viewer"})
	ctx = withChiParam(ctx, "shareID", "some-id")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.RevokeShare(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestRevokeShare_BadUUID(t *testing.T) {
	h := handler.NewShareHandler(nil)

	req := httptest.NewRequest(http.MethodDelete, "/shares/not-a-uuid", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "editor"})
	ctx = withChiParam(ctx, "shareID", "not-a-uuid")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.RevokeShare(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestSearch_Unauthenticated(t *testing.T) {
	h := handler.NewFilesHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/files/search?q=report", nil)
	rr := httptest.NewRecorder()

	h.Search(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestCreateFolderShare_ViewerForbidden(t *testing.T) {
	h := handler.NewShareHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/files/folders/some-id/share", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "viewer"})
	ctx = withChiParam(ctx, "folderID", "some-id")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.CreateFolderShare(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestSharedFileDownload_NoQueries404(t *testing.T) {
	h := handler.NewShareHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/s/some-slug/file/some-id", nil)
	ctx := withChiParam(req.Context(), "slug", "some-slug")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.SharedFileDownload(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}
