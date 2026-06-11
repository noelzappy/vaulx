package handler

import (
	"net/http"

	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/viewmodel"
	"github.com/noelzappy/vaulx/web/templates"
	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
)

type AuthHandler struct {
	queries db.Querier
	store   sessions.Store
}

func NewAuthHandler(q db.Querier, s sessions.Store) *AuthHandler {
	return &AuthHandler{queries: q, store: s}
}

func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderLogin(w, r, "")
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	user, err := h.queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		renderLogin(w, r, "Invalid email or password")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		renderLogin(w, r, "Invalid email or password")
		return
	}

	session, err := h.store.Get(r, "vaulx-session")
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	session.Values["user_id"] = user.ID.String()
	session.Values["role"] = user.Role
	session.Values["email"] = user.Email
	session.Values["name"] = user.Name

	if err := session.Save(r, w); err != nil {
		http.Error(w, "failed to save session", http.StatusInternalServerError)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID.String())
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID: userUUID,
		Action: "auth.login",
	})

	http.Redirect(w, r, "/files", http.StatusFound)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	session, err := h.store.Get(r, "vaulx-session")
	if err == nil {
		session.Options.MaxAge = -1
		_ = session.Save(r, w)
	}
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

func renderLogin(w http.ResponseWriter, r *http.Request, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	_ = templates.LoginPage(errMsg).Render(r.Context(), w)
}
