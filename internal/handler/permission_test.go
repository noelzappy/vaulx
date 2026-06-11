package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/handler"
)

func TestGrantPermission_NonAdminForbidden(t *testing.T) {
	h := handler.NewPermissionHandler(nil)

	for _, role := range []string{"editor", "viewer"} {
		req := httptest.NewRequest(http.MethodPost, "/admin/permissions", nil)
		ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: role})
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		h.Grant(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("role %q: expected 403, got %d", role, rr.Code)
		}
	}
}

func TestRevokePermission_NonAdminForbidden(t *testing.T) {
	h := handler.NewPermissionHandler(nil)

	for _, role := range []string{"editor", "viewer"} {
		req := httptest.NewRequest(http.MethodDelete, "/admin/permissions/some-id", nil)
		ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: role})
		ctx = withChiParam(ctx, "permID", "some-id")
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		h.Revoke(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("role %q: expected 403, got %d", role, rr.Code)
		}
	}
}
