package auth

import (
	"net/http"

	"github.com/gorilla/sessions"
)

// RequireAuth checks for a valid session and populates the request context
// with the authenticated user. Redirects to /auth/login if missing or invalid.
func RequireAuth(store sessions.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := store.Get(r, "vaulx-session")
			if err != nil || session.IsNew {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}

			userID, ok1 := session.Values["user_id"].(string)
			role, ok2 := session.Values["role"].(string)
			email, _ := session.Values["email"].(string)
			name, _ := session.Values["name"].(string)

			if !ok1 || !ok2 || userID == "" || role == "" {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}

			ctx := SetCurrentUser(r.Context(), UserContext{
				ID:    userID,
				Email: email,
				Name:  name,
				Role:  role,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
