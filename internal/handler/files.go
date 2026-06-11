package handler

import (
	"context"
	"net/http"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/db"
	"github.com/brifafrica/vaulx/internal/viewmodel"
	"github.com/brifafrica/vaulx/web/templates"
	"github.com/go-chi/chi/v5"
)

type FilesHandler struct {
	queries db.Querier
}

func NewFilesHandler(q db.Querier) *FilesHandler {
	return &FilesHandler{queries: q}
}

// GET /files — root-level folders and files.
func (h *FilesHandler) List(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	ctx := r.Context()

	var (
		dbFolders []db.Folder
		dbFiles   []db.File
		err       error
	)

	userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)
	if user.Role == "admin" || uuidErr != nil {
		dbFolders, err = h.queries.ListRootFolders(ctx)
		if err == nil {
			dbFiles, err = h.queries.ListRootFiles(ctx)
		}
	} else {
		dbFolders, err = h.queries.ListRootFoldersForUser(ctx, userUUID)
		if err == nil {
			dbFiles, err = h.queries.ListRootFilesForUser(ctx, userUUID)
		}
	}
	if err != nil {
		http.Error(w, "failed to list files", http.StatusInternalServerError)
		return
	}

	folders, files := h.buildViews(ctx, dbFolders, dbFiles)
	data := viewmodel.FileBrowserData{
		Folders:     folders,
		Files:       files,
		Breadcrumbs: []viewmodel.BreadcrumbItem{{Name: "My Files", URL: "/files"}},
	}
	renderFileBrowser(w, r, data, viewmodel.UserView{
		ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
	})
}

// GET /files/{folderID} — folder contents.
func (h *FilesHandler) ListFolder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	ctx := r.Context()
	folderIDStr := chi.URLParam(r, "folderID")

	folderUUID, err := viewmodel.UUIDFromString(folderIDStr)
	if err != nil {
		http.Error(w, "invalid folder id", http.StatusBadRequest)
		return
	}

	folder, err := h.queries.GetFolder(ctx, folderUUID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !auth.CanAccess(user, folder.OwnerID.String()) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var dbFolders []db.Folder
	var dbFiles []db.File
	userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)

	if user.Role == "admin" || uuidErr != nil {
		dbFolders, err = h.queries.ListFoldersByParent(ctx, folderUUID)
		if err == nil {
			dbFiles, err = h.queries.ListFilesByFolder(ctx, folderUUID)
		}
	} else {
		dbFolders, err = h.queries.ListFoldersByParentForUser(ctx, db.ListFoldersByParentForUserParams{
			ParentID: folderUUID,
			UserID:   userUUID,
		})
		if err == nil {
			dbFiles, err = h.queries.ListFilesByFolderForUser(ctx, db.ListFilesByFolderForUserParams{
				FolderID: folderUUID,
				UserID:   userUUID,
			})
		}
	}
	if err != nil {
		http.Error(w, "failed to list folder contents", http.StatusInternalServerError)
		return
	}

	breadcrumbs := h.buildBreadcrumbs(ctx, folder)
	folders, files := h.buildViews(ctx, dbFolders, dbFiles)

	data := viewmodel.FileBrowserData{
		Folders:     folders,
		Files:       files,
		Breadcrumbs: breadcrumbs,
		FolderID:    folderIDStr,
	}
	renderFileBrowser(w, r, data, viewmodel.UserView{
		ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
	})
}

// POST /files/folders — create a new folder (editor+ only).
func (h *FilesHandler) CreateFolder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	if !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	ownerUUID, err := viewmodel.UUIDFromString(user.ID)
	if err != nil {
		http.Error(w, "invalid user", http.StatusInternalServerError)
		return
	}

	parentIDStr := r.FormValue("parent_id")
	parentUUID, _ := viewmodel.UUIDFromString(parentIDStr)

	_, err = h.queries.CreateFolder(r.Context(), db.CreateFolderParams{
		Name:     name,
		ParentID: parentUUID, // zero value = NULL when !Valid
		OwnerID:  ownerUUID,
	})
	if err != nil {
		http.Error(w, "failed to create folder", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Folder created","type":"success"}}`)
	if parentIDStr != "" {
		http.Redirect(w, r, "/files/"+parentIDStr, http.StatusFound)
	} else {
		http.Redirect(w, r, "/files", http.StatusFound)
	}
}

// DELETE /files/folders/{folderID}
func (h *FilesHandler) DeleteFolder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	folderUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "folderID"))
	if err != nil {
		http.Error(w, "invalid folder id", http.StatusBadRequest)
		return
	}

	folder, err := h.queries.GetFolder(r.Context(), folderUUID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !auth.CanAccess(user, folder.OwnerID.String()) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.queries.DeleteFolder(r.Context(), folderUUID); err != nil {
		http.Error(w, "failed to delete folder", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Folder deleted","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}

// PATCH /files/folders/{folderID}
func (h *FilesHandler) RenameFolder(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	folderUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "folderID"))
	if err != nil {
		http.Error(w, "invalid folder id", http.StatusBadRequest)
		return
	}

	folder, err := h.queries.GetFolder(r.Context(), folderUUID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !auth.CanAccess(user, folder.OwnerID.String()) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	newName := r.FormValue("name")
	if newName == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	if _, err = h.queries.UpdateFolderName(r.Context(), db.UpdateFolderNameParams{
		Name: newName,
		ID:   folderUUID,
	}); err != nil {
		http.Error(w, "failed to rename folder", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Folder renamed","type":"success"}}`)
	w.WriteHeader(http.StatusOK)
}

func (h *FilesHandler) buildViews(ctx context.Context, dbFolders []db.Folder, dbFiles []db.File) ([]viewmodel.FolderView, []viewmodel.FileView) {
	folders := make([]viewmodel.FolderView, 0, len(dbFolders))
	for _, f := range dbFolders {
		count, _ := h.queries.CountFolderItems(ctx, f.ID)
		folders = append(folders, viewmodel.FolderFromDB(f, count))
	}
	files := make([]viewmodel.FileView, 0, len(dbFiles))
	for _, f := range dbFiles {
		uploaderName := ""
		if u, err := h.queries.GetUserByID(ctx, f.UploadedBy); err == nil {
			uploaderName = u.Name
		}
		files = append(files, viewmodel.FileFromDB(f, uploaderName))
	}
	return folders, files
}

func (h *FilesHandler) buildBreadcrumbs(ctx context.Context, leaf db.Folder) []viewmodel.BreadcrumbItem {
	var crumbs []viewmodel.BreadcrumbItem
	current := &leaf
	for current != nil {
		crumbs = append([]viewmodel.BreadcrumbItem{
			{Name: current.Name, ID: current.ID.String(), URL: "/files/" + current.ID.String()},
		}, crumbs...)
		if !current.ParentID.Valid {
			break
		}
		parent, err := h.queries.GetFolder(ctx, current.ParentID)
		if err != nil {
			break
		}
		current = &parent
	}
	return append([]viewmodel.BreadcrumbItem{{Name: "My Files", URL: "/files"}}, crumbs...)
}

func renderFileBrowser(w http.ResponseWriter, r *http.Request, data viewmodel.FileBrowserData, user viewmodel.UserView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") == "true" {
		_ = templates.FileBrowserContent(data, user).Render(r.Context(), w)
		return
	}
	_ = templates.FilesPage(data, user).Render(r.Context(), w)
}
