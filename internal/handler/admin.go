package handler

import (
	"net/http"
	"strconv"

	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/viewmodel"
	"github.com/noelzappy/vaulx/web/templates"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

type AdminHandler struct {
	queries db.Querier
}

func NewAdminHandler(q db.Querier) *AdminHandler {
	return &AdminHandler{queries: q}
}

// GET /admin/users
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || user.Role != "admin" {
		w.WriteHeader(http.StatusForbidden)
		_ = templates.AuthErrorPage(403, "Access denied",
			"You don't have permission to view this. Contact your admin to request access.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	users, err := h.queries.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "failed to list users", http.StatusInternalServerError)
		return
	}

	userViews := make([]viewmodel.UserView, 0, len(users))
	for _, u := range users {
		userViews = append(userViews, viewmodel.UserFromDB(u))
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.AdminUsersPage(userViews, viewmodel.UserView{
		ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
	}).Render(r.Context(), w)
}

// POST /admin/users
func (h *AdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || user.Role != "admin" {
		w.WriteHeader(http.StatusForbidden)
		_ = templates.AuthErrorPage(403, "Access denied",
			"You don't have permission to view this. Contact your admin to request access.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	email := r.FormValue("email")
	name := r.FormValue("name")
	role := r.FormValue("role")
	password := r.FormValue("password")

	if email == "" || name == "" || role == "" || password == "" {
		http.Error(w, "email, name, role and password are required", http.StatusBadRequest)
		return
	}

	validRoles := map[string]bool{"viewer": true, "editor": true, "admin": true}
	if !validRoles[role] {
		http.Error(w, "invalid role", http.StatusBadRequest)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if _, err = h.queries.CreateUser(r.Context(), db.CreateUserParams{
		Email:        email,
		Name:         name,
		Role:         role,
		PasswordHash: string(hash),
	}); err != nil {
		http.Error(w, "failed to create user (email may already exist)", http.StatusConflict)
		return
	}

	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

// PATCH /admin/users/{userID}
func (h *AdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || user.Role != "admin" {
		w.WriteHeader(http.StatusForbidden)
		_ = templates.AuthErrorPage(403, "Access denied",
			"You don't have permission to view this. Contact your admin to request access.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	targetUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "userID"))
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if role := r.FormValue("role"); role != "" {
		validRoles := map[string]bool{"viewer": true, "editor": true, "admin": true}
		if !validRoles[role] {
			http.Error(w, "invalid role", http.StatusBadRequest)
			return
		}
		if _, err := h.queries.UpdateUserRole(r.Context(), db.UpdateUserRoleParams{
			Role: role,
			ID:   targetUUID,
		}); err != nil {
			http.Error(w, "failed to update role", http.StatusInternalServerError)
			return
		}
	}

	if activeStr := r.FormValue("active"); activeStr != "" {
		active := activeStr == "true"
		if _, err := h.queries.DeactivateUser(r.Context(), db.DeactivateUserParams{
			Active: active,
			ID:     targetUUID,
		}); err != nil {
			http.Error(w, "failed to update user", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"User updated","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}

// GET /admin/audit?action=<optional>&page=<optional>
func (h *AdminHandler) AuditLog(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || user.Role != "admin" {
		w.WriteHeader(http.StatusForbidden)
		_ = templates.AuthErrorPage(403, "Access denied",
			"You don't have permission to view this. Contact your admin to request access.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	actionFilter := r.URL.Query().Get("action")

	page := 1
	limit := 50
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}

	var entries []viewmodel.AuditLogView
	var pagination *viewmodel.PaginationData

	if actionFilter != "" {
		rows, err := h.queries.ListAuditLogByAction(r.Context(), db.ListAuditLogByActionParams{
			Action: actionFilter,
			Limit:  100,
		})
		if err != nil {
			http.Error(w, "failed to load audit log", http.StatusInternalServerError)
			return
		}
		for _, row := range rows {
			entries = append(entries, viewmodel.AuditLogViewFromRow(
				row.ID, row.Action, row.ResourceType, row.ResourceID,
				row.CreatedAt, row.ActorName, row.ActorEmail,
			))
		}
	} else {
		offset := int32((page - 1) * limit)
		rows, err := h.queries.ListAuditLogPage(r.Context(), db.ListAuditLogPageParams{
			Limit:  int32(limit),
			Offset: offset,
		})
		if err != nil {
			http.Error(w, "failed to load audit log", http.StatusInternalServerError)
			return
		}
		for _, row := range rows {
			entries = append(entries, viewmodel.AuditLogViewFromRow(
				row.ID, row.Action, row.ResourceType, row.ResourceID,
				row.CreatedAt, row.ActorName, row.ActorEmail,
			))
		}
		total, _ := h.queries.CountAuditLog(r.Context())
		pagination = viewmodel.NewPagination(page, limit, total)
	}

	if entries == nil {
		entries = []viewmodel.AuditLogView{}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.AdminAuditPage(entries, actionFilter, pagination, viewmodel.UserView{
		ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
	}).Render(r.Context(), w)
}

// GET /admin/trash — soft-deleted files, admin-only.
func (h *AdminHandler) Trash(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || user.Role != "admin" {
		w.WriteHeader(http.StatusForbidden)
		_ = templates.AuthErrorPage(403, "Access denied",
			"You don't have permission to view this. Contact your admin to request access.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	rows, err := h.queries.ListDeletedFiles(r.Context())
	if err != nil {
		http.Error(w, "failed to list trash", http.StatusInternalServerError)
		return
	}

	items := make([]viewmodel.TrashItemView, 0, len(rows))
	for _, f := range rows {
		uploader := ""
		if f.UploaderName != nil {
			uploader = *f.UploaderName
		}
		items = append(items, viewmodel.TrashItemView{
			ID:           f.ID.String(),
			Name:         f.Name,
			SizeHuman:    viewmodel.HumanSize(f.SizeBytes),
			UploaderName: uploader,
			Date:         f.CreatedAt.Time.Format("Jan 2, 2006"),
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.AdminTrashPage(items, viewmodel.UserView{
		ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
	}).Render(r.Context(), w)
}

// POST /admin/trash/{fileID}/restore — admin-only.
func (h *AdminHandler) RestoreFile(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "fileID"))
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	if _, err := h.queries.RestoreFile(r.Context(), fileUUID); err != nil {
		http.NotFound(w, r)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	resourceType := "file"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       "file.restore",
		ResourceType: &resourceType,
		ResourceID:   fileUUID,
	})

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"File restored","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}
