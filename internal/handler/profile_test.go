package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/handler"
)

func TestProfilePage_Unauthenticated(t *testing.T) {
	h := handler.NewProfileHandler(nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/profile", nil)
	rr := httptest.NewRecorder()

	h.Page(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestUpdateName_Unauthenticated(t *testing.T) {
	h := handler.NewProfileHandler(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader("name=Alice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.UpdateName(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestUpdatePassword_Unauthenticated(t *testing.T) {
	h := handler.NewProfileHandler(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/profile/password",
		strings.NewReader("current_password=old&new_password=new&confirm_password=new"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.UpdatePassword(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestUpdatePassword_MismatchedNewPasswords(t *testing.T) {
	h := handler.NewProfileHandler(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/profile/password",
		strings.NewReader("current_password=old&new_password=new1&confirm_password=new2"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: "editor", Name: "Bob", Email: "bob@example.com"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	h.UpdatePassword(rr, req)

	// Should re-render the profile page with an error (200) or 400
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusOK {
		t.Errorf("expected 400 or 200, got %d", rr.Code)
	}
}
