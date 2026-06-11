package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/handler"
)

func TestCreateShare_ViewerForbidden(t *testing.T) {
	h := handler.NewShareHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/files/some-id/share", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "viewer"})
	ctx = withChiParam(ctx, "fileID", "some-id")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.CreateShare(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestCreateShare_BadFileUUID(t *testing.T) {
	h := handler.NewShareHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/files/not-a-uuid/share", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "editor"})
	ctx = withChiParam(ctx, "fileID", "not-a-uuid")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.CreateShare(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestResolveShare_EmptySlug(t *testing.T) {
	h := handler.NewShareHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/s/", nil)
	ctx := withChiParam(req.Context(), "slug", "")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.ResolveShare(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}
