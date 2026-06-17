package handler

import (
	"net/http"
	"strings"

	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/viewmodel"
	"github.com/noelzappy/vaulx/web/templates"
	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
)

type ProfileHandler struct {
	queries db.Querier
	store   sessions.Store
}

func NewProfileHandler(q db.Querier, s sessions.Store) *ProfileHandler {
	return &ProfileHandler{queries: q, store: s}
}

func (h *ProfileHandler) Page(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var successMsg string
	switch r.URL.Query().Get("success") {
	case "name":
		successMsg = "Display name updated."
	case "password":
		successMsg = "Password updated successfully."
	}
	user := viewmodel.UserView{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.ProfilePage(user, "", successMsg).Render(r.Context(), w)
}

func (h *ProfileHandler) UpdateName(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		user := viewmodel.UserView{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = templates.ProfilePage(user, "Name cannot be empty.", "").Render(r.Context(), w)
		return
	}
	if len(name) > 100 {
		user := viewmodel.UserView{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = templates.ProfilePage(user, "Name is too long (max 100 characters).", "").Render(r.Context(), w)
		return
	}

	userUUID, err := viewmodel.UUIDFromString(u.ID)
	if err != nil {
		http.Error(w, "bad user id", http.StatusInternalServerError)
		return
	}

	_, err = h.queries.UpdateUserName(r.Context(), db.UpdateUserNameParams{
		Name: name,
		ID:   userUUID,
	})
	if err != nil {
		http.Error(w, "could not update name", http.StatusInternalServerError)
		return
	}

	// Refresh the session so the sidebar reflects the new name immediately.
	session, err := h.store.Get(r, "vaulx-session")
	if err == nil {
		session.Values["name"] = name
		_ = session.Save(r, w)
	}

	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID: userUUID,
		Action: "profile.name_updated",
	})

	http.Redirect(w, r, "/profile?success=name", http.StatusSeeOther)
}

func (h *ProfileHandler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	currentPw := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirmPw := r.FormValue("confirm_password")

	user := viewmodel.UserView{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role}

	renderErr := func(msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = templates.ProfilePage(user, msg, "").Render(r.Context(), w)
	}

	if newPw != confirmPw {
		renderErr("New passwords do not match.")
		return
	}
	if len(newPw) < 8 {
		renderErr("New password must be at least 8 characters.")
		return
	}

	userUUID, err := viewmodel.UUIDFromString(u.ID)
	if err != nil {
		http.Error(w, "bad user id", http.StatusInternalServerError)
		return
	}

	dbUser, err := h.queries.GetUserByID(r.Context(), userUUID)
	if err != nil {
		http.Error(w, "user not found", http.StatusInternalServerError)
		return
	}
	// Deactivated users cannot change their password. Active=false is set by an
	// admin; the user sees a 403 rather than a misleading "incorrect password"
	// error. (Tested via integration test with a real DB; skipped in unit tests
	// because GetUserByID requires a live query layer.)
	if !dbUser.Active {
		http.Error(w, "account is deactivated", http.StatusForbidden)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(dbUser.PasswordHash), []byte(currentPw)); err != nil {
		renderErr("Current password is incorrect.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPw), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "could not hash password", http.StatusInternalServerError)
		return
	}

	_, err = h.queries.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{
		PasswordHash: string(hash),
		ID:           userUUID,
	})
	if err != nil {
		http.Error(w, "could not update password", http.StatusInternalServerError)
		return
	}

	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID: userUUID,
		Action: "profile.password_updated",
	})

	http.Redirect(w, r, "/profile?success=password", http.StatusSeeOther)
}
