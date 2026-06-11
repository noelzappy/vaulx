package handler

import (
	"context"
	"net/http"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/db"
	"github.com/brifafrica/vaulx/internal/storage"
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

	newFolder, err := h.queries.CreateFolder(r.Context(), db.CreateFolderParams{
		Name:     name,
		ParentID: parentUUID, // zero value = NULL when !Valid
		OwnerID:  ownerUUID,
	})
	if err != nil {
		http.Error(w, "failed to create folder", http.StatusInternalServerError)
		return
	}

	resourceType := "folder"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       ownerUUID,
		Action:       "folder.create",
		ResourceType: &resourceType,
		ResourceID:   newFolder.ID,
	})

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

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	resourceType := "folder"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       "folder.delete",
		ResourceType: &resourceType,
		ResourceID:   folderUUID,
	})

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

	updatedFolder, err := h.queries.UpdateFolderName(r.Context(), db.UpdateFolderNameParams{
		Name: newName,
		ID:   folderUUID,
	})
	if err != nil {
		http.Error(w, "failed to rename folder", http.StatusInternalServerError)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	resourceType := "folder"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       "folder.rename",
		ResourceType: &resourceType,
		ResourceID:   folderUUID,
	})

	count, _ := h.queries.CountFolderItems(r.Context(), updatedFolder.ID)
	fv := viewmodel.FolderFromDB(updatedFolder, count)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", `{"showToast":{"message":"Folder renamed","type":"success"}}`)
	_ = templates.FolderCard(fv).Render(r.Context(), w)
}

// DELETE /files/{fileID}
func (h *FilesHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	permanent := r.URL.Query().Get("permanent") == "true"
	if permanent && user.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "fileID"))
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	file, err := h.queries.GetFile(r.Context(), fileUUID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if user.Role != "admin" && (!file.UploadedBy.Valid || file.UploadedBy.String() != user.ID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.queries.SoftDeleteFile(r.Context(), fileUUID); err != nil {
		http.Error(w, "failed to delete file", http.StatusInternalServerError)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	resourceType := "file"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       "file.delete",
		ResourceType: &resourceType,
		ResourceID:   fileUUID,
	})

	if permanent {
		if err := storage.DeleteObject(r.Context(), file.S3Key); err != nil {
			_ = err // log but don't fail — record already soft-deleted
		}
		w.Header().Set("HX-Trigger", `{"showToast":{"message":"File permanently deleted","type":"success"}}`)
	} else {
		w.Header().Set("HX-Trigger", `{"showToast":{"message":"File deleted","type":"success"}}`)
	}
	w.WriteHeader(http.StatusOK)
}

// PATCH /files/{fileID}/name
// Admin can rename any active file. Editor can only rename files they uploaded.
// Returns the updated FileCard partial.
func (h *FilesHandler) RenameFile(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "fileID"))
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	file, err := h.queries.GetFile(r.Context(), fileUUID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if user.Role != "admin" && (!file.UploadedBy.Valid || file.UploadedBy.String() != user.ID) {
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

	updatedFile, err := h.queries.UpdateFileName(r.Context(), db.UpdateFileNameParams{
		Name: newName,
		ID:   fileUUID,
	})
	if err != nil {
		http.Error(w, "failed to rename file", http.StatusInternalServerError)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	resourceType := "file"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       "file.rename",
		ResourceType: &resourceType,
		ResourceID:   fileUUID,
	})

	uploaderName := ""
	if u, err := h.queries.GetUserByID(r.Context(), updatedFile.UploadedBy); err == nil {
		uploaderName = u.Name
	}
	fv := viewmodel.FileFromDB(updatedFile, uploaderName)
	canEdit := user.Role == "admin" || (user.Role == "editor" && fv.UploaderID == user.ID)
	// TODO(Task 6): add canHardDelete bool (user.Role == "admin") once FileCard signature updated
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", `{"showToast":{"message":"File renamed","type":"success"}}`)
	_ = templates.FileCard(fv, canEdit).Render(r.Context(), w)
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
