package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/gorilla/sessions"
)

func TestRequireAuth_RedirectsWhenNoSession(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret-32-bytes-long-padded!"))
	mw := auth.RequireAuth(store)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/files", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/auth/login" {
		t.Errorf("expected redirect to /auth/login, got %q", loc)
	}
}

func TestRequireAuth_SetsUserInContext(t *testing.T) {
	store := sessions.NewCookieStore([]byte("test-secret-32-bytes-long-padded!"))
	mw := auth.RequireAuth(store)

	// Seed a valid session via a fake save.
	seedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	seedRec := httptest.NewRecorder()
	sess, _ := store.Get(seedReq, "vaulx-session")
	sess.Values["user_id"] = "u1"
	sess.Values["role"] = "editor"
	sess.Values["email"] = "test@example.com"
	sess.Values["name"] = "Tester"
	_ = sess.Save(seedReq, seedRec)

	// Replay those cookies on the actual request.
	req := httptest.NewRequest(http.MethodGet, "/files", nil)
	for _, c := range seedRec.Result().Cookies() {
		req.AddCookie(c)
	}

	var gotUser auth.UserContext
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, _ = auth.GetCurrentUser(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if gotUser.ID != "u1" || gotUser.Role != "editor" {
		t.Errorf("unexpected user in context: %+v", gotUser)
	}
}
