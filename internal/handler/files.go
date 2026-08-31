package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/internal/viewmodel"
	"github.com/noelzappy/vaulx/web/templates"
	"github.com/go-chi/chi/v5"
)

type FilesHandler struct {
	queries db.Querier
}

func NewFilesHandler(q db.Querier) *FilesHandler {
	return &FilesHandler{queries: q}
}

func parsePage(r *http.Request) (page, limit int) {
	page = 1
	limit = 48
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	return
}

// GET /files — root-level folders and files.
func (h *FilesHandler) List(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetCurrentUser(r.Context())
	ctx := r.Context()

	page, limit := parsePage(r)
	offset := int32((page - 1) * limit)

	var (
		dbFolders  []db.Folder
		dbFiles    []db.File
		totalFiles int64
		err        error
	)

	userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)
	if user.Role == "admin" || uuidErr != nil {
		dbFolders, err = h.queries.ListRootFolders(ctx)
		if err == nil {
			dbFiles, err = h.queries.ListFilesPage(ctx, db.ListFilesPageParams{
				FolderID: pgtype.UUID{Valid: false},
				Limit:    int32(limit),
				Offset:   offset,
			})
		}
		if err == nil {
			totalFiles, err = h.queries.CountFiles(ctx, pgtype.UUID{Valid: false})
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
	var pagination *viewmodel.PaginationData
	if user.Role == "admin" {
		pagination = viewmodel.NewPagination(page, limit, totalFiles)
	}
	data := viewmodel.FileBrowserData{
		Folders:     folders,
		Files:       files,
		Breadcrumbs: []viewmodel.BreadcrumbItem{{Name: "My Files", URL: "/files"}},
		Pagination:  pagination,
	}
	renderFileBrowser(w, r, data, viewmodel.UserView{
		ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
	})
}

// GET /files/search?q= — name search across all active files and folders.
func (h *FilesHandler) Search(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		h.List(w, r) // empty query falls back to the normal root listing
		return
	}
	ctx := r.Context()

	dbFiles, err := h.queries.SearchFiles(ctx, &q)
	if err != nil {
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}
	dbFolders, err := h.queries.SearchFolders(ctx, &q)
	if err != nil {
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}

	folders, files := h.buildViews(ctx, dbFolders, dbFiles)
	uv := viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") == "true" {
		_ = templates.SearchResults(q, folders, files, uv).Render(ctx, w)
		return
	}
	_ = templates.SearchPage(q, folders, files, uv).Render(ctx, w)
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
		w.WriteHeader(http.StatusNotFound)
		_ = templates.AuthErrorPage(404, "Not found",
			"This file or folder doesn't exist, or it's been deleted.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}
	if !auth.CanAccess(user, folder.OwnerID.String()) {
		userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)
		hasPerm := false
		if uuidErr == nil {
			hasPerm, _ = h.queries.CheckPermission(r.Context(), db.CheckPermissionParams{
				UserID:       userUUID,
				ResourceType: "folder",
				ResourceID:   folderUUID,
			})
		}
		if !hasPerm {
			w.WriteHeader(http.StatusForbidden)
			_ = templates.AuthErrorPage(403, "Access denied",
				"You don't have permission to view this. Contact your admin to request access.",
				viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
			).Render(r.Context(), w)
			return
		}
	}

	page, limit := parsePage(r)
	offset := int32((page - 1) * limit)

	var (
		dbFolders  []db.Folder
		dbFiles    []db.File
		totalFiles int64
	)

	userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)
	if user.Role == "admin" || uuidErr != nil {
		dbFolders, err = h.queries.ListFoldersByParent(ctx, folderUUID)
		if err == nil {
			dbFiles, err = h.queries.ListFilesPage(ctx, db.ListFilesPageParams{
				FolderID: folderUUID,
				Limit:    int32(limit),
				Offset:   offset,
			})
		}
		if err == nil {
			totalFiles, err = h.queries.CountFiles(ctx, folderUUID)
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
		http.Error(w, "failed to load folder contents", http.StatusInternalServerError)
		return
	}

	breadcrumbs := h.buildBreadcrumbs(ctx, folder)
	folders, files := h.buildViews(ctx, dbFolders, dbFiles)

	var pagination *viewmodel.PaginationData
	if user.Role == "admin" {
		pagination = viewmodel.NewPagination(page, limit, totalFiles)
	}
	data := viewmodel.FileBrowserData{
		Folders:     folders,
		Files:       files,
		Breadcrumbs: breadcrumbs,
		FolderID:    folderIDStr,
		Pagination:  pagination,
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

	if permanent {
		_ = storage.DeleteObject(r.Context(), file.S3Key) // original may already be gone
		if file.ThumbS3Key != nil && *file.ThumbS3Key != "" {
			_ = storage.DeleteObject(r.Context(), *file.ThumbS3Key)
		}
		if err := h.queries.HardDeleteFile(r.Context(), fileUUID); err != nil {
			http.Error(w, "failed to delete file", http.StatusInternalServerError)
			return
		}
	} else {
		if err := h.queries.SoftDeleteFile(r.Context(), fileUUID); err != nil {
			http.Error(w, "failed to delete file", http.StatusInternalServerError)
			return
		}
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	resourceType := "file"
	action := "file.delete"
	if permanent {
		action = "file.hard_delete"
	}
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       action,
		ResourceType: &resourceType,
		ResourceID:   fileUUID,
	})

	if permanent {
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
	canHardDelete := user.Role == "admin"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", `{"showToast":{"message":"File renamed","type":"success"}}`)
	_ = templates.FileCard(fv, canEdit, canHardDelete).Render(r.Context(), w)
}

// GET /api/file/{fileID}/preview — HTMX partial, returns PreviewPanel HTML
func (h *FilesHandler) PreviewFile(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "fileID"))
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	file, err := h.queries.GetFile(r.Context(), fileUUID)
	if err != nil || file.Status != "active" {
		w.WriteHeader(http.StatusNotFound)
		_ = templates.AuthErrorPage(404, "Not found",
			"This file or folder doesn't exist, or it's been deleted.",
			viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
		).Render(r.Context(), w)
		return
	}

	if !auth.CanAccess(user, file.UploadedBy.String()) {
		userUUID, uuidErr := viewmodel.UUIDFromString(user.ID)
		hasPerm := false
		if uuidErr == nil {
			hasPerm, _ = h.queries.CheckPermission(r.Context(), db.CheckPermissionParams{
				UserID:       userUUID,
				ResourceType: "file",
				ResourceID:   fileUUID,
			})
		}
		if !hasPerm {
			w.WriteHeader(http.StatusForbidden)
			_ = templates.AuthErrorPage(403, "Access denied",
				"You don't have permission to view this. Contact your admin to request access.",
				viewmodel.UserView{ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role},
			).Render(r.Context(), w)
			return
		}
	}

	presignedURL, err := storage.PresignGETWithTTL(r.Context(), file.S3Key, 5*time.Minute)
	if err != nil {
		http.Error(w, "failed to generate preview URL", http.StatusInternalServerError)
		return
	}

	uploaderName := ""
	if u, err := h.queries.GetUserByID(r.Context(), file.UploadedBy); err == nil {
		uploaderName = u.Name
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.PreviewPanel(file, uploaderName, presignedURL).Render(r.Context(), w)
}

// GET /upload — dedicated upload page with a destination folder picker.
func (h *FilesHandler) UploadPage(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	if !auth.CanEdit(user) {
		http.Redirect(w, r, "/files", http.StatusFound)
		return
	}
	dbFolders, err := h.queries.ListAllFolders(r.Context())
	if err != nil {
		http.Error(w, "failed to list folders", http.StatusInternalServerError)
		return
	}
	folders := make([]viewmodel.FolderView, len(dbFolders))
	for i, f := range dbFolders {
		folders[i] = viewmodel.FolderView{ID: f.ID.String(), Name: f.Name}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.UploadPage(folders, viewmodel.UserView{
		ID: user.ID, Email: user.Email, Name: user.Name, Role: user.Role,
	}).Render(r.Context(), w)
}

// GET /api/folders — returns all folders as JSON for the move-to-folder picker.
func (h *FilesHandler) ListFoldersJSON(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetCurrentUser(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	folders, err := h.queries.ListAllFolders(r.Context())
	if err != nil {
		http.Error(w, "failed to list folders", http.StatusInternalServerError)
		return
	}
	type row struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := make([]row, len(folders))
	for i, f := range folders {
		out[i] = row{ID: f.ID.String(), Name: f.Name}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// PATCH /files/{fileID}/folder — move a file to a different folder (or root).
// Body: {"folderId": "<uuid>"} or {"folderId": ""} to move to root.
func (h *FilesHandler) MoveFile(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetCurrentUser(r.Context())
	if !ok || !auth.CanEdit(user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	fileUUID, err := viewmodel.UUIDFromString(chi.URLParam(r, "fileID"))
	if err != nil {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	var body struct {
		FolderID string `json:"folderId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	var folderID pgtype.UUID
	if body.FolderID != "" {
		folderID, err = viewmodel.UUIDFromString(body.FolderID)
		if err != nil {
			http.Error(w, "invalid folder id", http.StatusBadRequest)
			return
		}
	}

	updatedFile, err := h.queries.MoveFileToFolder(r.Context(), db.MoveFileToFolderParams{
		FolderID: folderID,
		ID:       fileUUID,
	})
	if err != nil {
		http.Error(w, "failed to move file", http.StatusInternalServerError)
		return
	}

	userUUID, _ := viewmodel.UUIDFromString(user.ID)
	resourceType := "file"
	_, _ = h.queries.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		UserID:       userUUID,
		Action:       "file.move",
		ResourceType: &resourceType,
		ResourceID:   fileUUID,
	})

	uploaderName := ""
	if u, err := h.queries.GetUserByID(r.Context(), updatedFile.UploadedBy); err == nil {
		uploaderName = u.Name
	}
	fv := viewmodel.FileFromDB(updatedFile, uploaderName)
	canEdit := user.Role == "admin" || (user.Role == "editor" && fv.UploaderID == user.ID)
	canHardDelete := user.Role == "admin"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", `{"showToast":{"message":"File moved","type":"success"}}`)
	_ = templates.FileCard(fv, canEdit, canHardDelete).Render(r.Context(), w)
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
		fv := viewmodel.FileFromDB(f, uploaderName)
		if f.MimeType != nil && strings.HasPrefix(*f.MimeType, "image/") {
			if f.ThumbS3Key != nil && *f.ThumbS3Key != "" {
				if url, err := storage.PresignGET(ctx, *f.ThumbS3Key); err == nil {
					fv.ThumbURL = url
				}
			}
		}
		files = append(files, fv)
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
