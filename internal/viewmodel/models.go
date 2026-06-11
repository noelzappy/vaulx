package viewmodel

import (
	"fmt"
	"time"

	"github.com/brifafrica/vaulx/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

type UserView struct {
	ID     string
	Email  string
	Name   string
	Role   string
	Active string // "true" or "false" — string for templ comparisons
}

type FileView struct {
	ID           string
	Name         string
	SizeBytes    int64
	SizeHuman    string
	MimeType     string
	UploaderID   string
	UploaderName string
	FolderID     string
	Status       string
	CreatedAt    time.Time
	RelativeDate string
}

type FolderView struct {
	ID        string
	Name      string
	ParentID  string
	OwnerID   string
	CreatedAt time.Time
	ItemCount int64
}

type BreadcrumbItem struct {
	Name string
	ID   string
	URL  string
}

type FileBrowserData struct {
	Folders     []FolderView
	Files       []FileView
	Breadcrumbs []BreadcrumbItem
	FolderID    string
}

func HumanSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case bytes < KB:
		return fmt.Sprintf("%d B", bytes)
	case bytes < MB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	case bytes < GB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	default:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	}
}

func RelativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}

func UserFromDB(u db.User) UserView {
	active := "false"
	if u.Active {
		active = "true"
	}
	return UserView{
		ID:     u.ID.String(),
		Email:  u.Email,
		Name:   u.Name,
		Role:   u.Role,
		Active: active,
	}
}

func FileFromDB(f db.File, uploaderName string) FileView {
	var mimeType, folderID string
	if f.MimeType != nil {
		mimeType = *f.MimeType
	}
	if f.FolderID.Valid {
		folderID = f.FolderID.String()
	}
	createdAt := f.CreatedAt.Time
	return FileView{
		ID:           f.ID.String(),
		Name:         f.Name,
		SizeBytes:    f.SizeBytes,
		SizeHuman:    HumanSize(f.SizeBytes),
		MimeType:     mimeType,
		UploaderID:   f.UploadedBy.String(),
		UploaderName: uploaderName,
		FolderID:     folderID,
		Status:       f.Status,
		CreatedAt:    createdAt,
		RelativeDate: RelativeTime(createdAt),
	}
}

func FolderFromDB(f db.Folder, itemCount int64) FolderView {
	var parentID string
	if f.ParentID.Valid {
		parentID = f.ParentID.String()
	}
	return FolderView{
		ID:        f.ID.String(),
		Name:      f.Name,
		ParentID:  parentID,
		OwnerID:   f.OwnerID.String(),
		CreatedAt: f.CreatedAt.Time,
		ItemCount: itemCount,
	}
}

// UUIDFromString parses a UUID string into a pgtype.UUID.
func UUIDFromString(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	err := u.Scan(s)
	return u, err
}

type AuditLogView struct {
	ID           string
	ActorName    string
	ActorEmail   string
	Action       string
	ResourceType string
	ResourceID   string
	CreatedAt    string // formatted: "Jan 2, 2006 15:04"
}

func AuditLogViewFromRow(
	id pgtype.UUID,
	action string,
	resourceType *string,
	resourceID pgtype.UUID,
	createdAt pgtype.Timestamptz,
	actorName *string,
	actorEmail *string,
) AuditLogView {
	rt := ""
	if resourceType != nil {
		rt = *resourceType
	}
	name := ""
	if actorName != nil {
		name = *actorName
	}
	email := ""
	if actorEmail != nil {
		email = *actorEmail
	}
	rid := ""
	if resourceID.Valid {
		rid = resourceID.String()
	}
	ts := ""
	if createdAt.Valid {
		ts = createdAt.Time.Format("Jan 2, 2006 15:04")
	}
	return AuditLogView{
		ID:           id.String(),
		ActorName:    name,
		ActorEmail:   email,
		Action:       action,
		ResourceType: rt,
		ResourceID:   rid,
		CreatedAt:    ts,
	}
}
