package handler

import (
	"net/http"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/db"
	"github.com/brifafrica/vaulx/internal/viewmodel"
	"github.com/brifafrica/vaulx/web/templates"
	"github.com/go-chi/chi/v5"
)

type PermissionHandler struct {
	queries db.Querier
}

func NewPermissionHandler(q db.Querier) *PermissionHandler {
	return &PermissionHandler{queries: q}
}

// GET /admin/permissions?type=file&id={uuid}
func (h *PermissionHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	resourceType := r.URL.Query().Get("type")
	resourceIDStr := r.URL.Query().Get("id")
	if resourceType == "" || resourceIDStr == "" {
		http.Error(w, "type and id params required", http.StatusBadRequest)
		return
	}
	if resourceType != "file" && resourceType != "folder" {
		http.Error(w, "type must be 'file' or 'folder'", http.StatusBadRequest)
		return
	}

	resourceUUID, err := viewmodel.UUIDFromString(resourceIDStr)
	if err != nil {
		http.Error(w, "invalid resource id", http.StatusBadRequest)
		return
	}

	rows, err := h.queries.ListPermissionsForResource(r.Context(), db.ListPermissionsForResourceParams{
		ResourceType: resourceType,
		ResourceID:   resourceUUID,
	})
	if err != nil {
		http.Error(w, "failed to load permissions", http.StatusInternalServerError)
		return
	}

	perms := make([]viewmodel.PermissionView, 0, len(rows))
	for _, row := range rows {
		perms = append(perms, viewmodel.PermissionView{
			ID:           row.ID.String(),
			GranteeName:  row.GranteeName,
			GranteeEmail: row.GranteeEmail,
			Level:        row.Level,
			CreatedAt:    row.CreatedAt.Time.Format("Jan 2, 2006"),
		})
	}

	dbUsers, err := h.queries.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "failed to load users", http.StatusInternalServerError)
		return
	}
	users := make([]viewmodel.UserView, 0, len(dbUsers))
	for _, u := range dbUsers {
		if u.Active {
			users = append(users, viewmodel.UserFromDB(u))
		}
	}

	resourceName := resourceIDStr[:8] + "…"
	if resourceType == "file" {
		if f, err := h.queries.GetFile(r.Context(), resourceUUID); err == nil {
			resourceName = f.Name
		}
	} else {
		if f, err := h.queries.GetFolder(r.Context(), resourceUUID); err == nil {
			resourceName = f.Name
		}
	}

	data := viewmodel.PermissionsPageData{
		ResourceType: resourceType,
		ResourceID:   resourceIDStr,
		ResourceName: resourceName,
		Permissions:  perms,
		Users:        users,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.AdminPermissionsPage(data, viewmodel.UserView{
		ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
	}).Render(r.Context(), w)
}

// POST /admin/permissions
func (h *PermissionHandler) Grant(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	resourceType := r.FormValue("resource_type")
	resourceIDStr := r.FormValue("resource_id")
	userIDStr := r.FormValue("user_id")
	level := r.FormValue("level")

	if resourceType == "" || resourceIDStr == "" || userIDStr == "" || level == "" {
		http.Error(w, "all fields required", http.StatusBadRequest)
		return
	}
	validLevels := map[string]bool{"view": true, "edit": true, "manage": true}
	if !validLevels[level] {
		http.Error(w, "invalid level", http.StatusBadRequest)
		return
	}

	resourceUUID, err := viewmodel.UUIDFromString(resourceIDStr)
	if err != nil {
		http.Error(w, "invalid resource id", http.StatusBadRequest)
		return
	}
	granteeUUID, err := viewmodel.UUIDFromString(userIDStr)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	granterUUID, _ := viewmodel.UUIDFromString(user.ID)

	_, err = h.queries.GrantPermission(r.Context(), db.GrantPermissionParams{
		UserID:       granteeUUID,
		ResourceType: resourceType,
		ResourceID:   resourceUUID,
		Level:        level,
		GrantedBy:    granterUUID,
	})
	if err != nil {
		http.Error(w, "failed to grant permission", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Access granted","type":"success"}}`)
	http.Redirect(w, r, "/admin/permissions?type="+resourceType+"&id="+resourceIDStr, http.StatusFound)
}

// DELETE /admin/permissions/{permID}
func (h *PermissionHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	permUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "permID"))
	if err != nil {
		http.Error(w, "invalid permission id", http.StatusBadRequest)
		return
	}

	if err := h.queries.RevokePermission(r.Context(), permUUID); err != nil {
		http.Error(w, "failed to revoke permission", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Access revoked","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}
