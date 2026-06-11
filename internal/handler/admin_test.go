package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/handler"
)

func TestListUsers_NonAdminForbidden(t *testing.T) {
	h := handler.NewAdminHandler(nil)

	for _, role := range []string{"editor", "viewer"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
		ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: role})
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		h.ListUsers(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("role %q: expected 403, got %d", role, rr.Code)
		}
	}
}

func TestCreateUser_NonAdminForbidden(t *testing.T) {
	h := handler.NewAdminHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/users", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "editor"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.CreateUser(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestUpdateUser_NonAdminForbidden(t *testing.T) {
	h := handler.NewAdminHandler(nil)

	req := httptest.NewRequest(http.MethodPatch, "/admin/users/some-id", nil)
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "editor"})
	ctx = withChiParam(ctx, "userID", "some-id")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.UpdateUser(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}
