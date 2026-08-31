package handler_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/handler"
)

const (
	selfUUID  = "00000000-0000-0000-0000-000000000001"
	otherUUID = "00000000-0000-0000-0000-000000000002"
)

func uid(b byte) pgtype.UUID {
	var a [16]byte
	a[15] = b
	return pgtype.UUID{Bytes: a, Valid: true}
}

type bulkQuerier struct {
	db.Querier
	files       []db.File
	movedFolder pgtype.UUID
	softIDs     []pgtype.UUID
	hardIDs     []pgtype.UUID
	actions     []string
}

func (m *bulkQuerier) GetFilesByIDs(ctx context.Context, ids []pgtype.UUID) ([]db.File, error) {
	return m.files, nil
}

func (m *bulkQuerier) MoveFilesToFolder(ctx context.Context, arg db.MoveFilesToFolderParams) error {
	m.movedFolder = arg.FolderID
	return nil
}

func (m *bulkQuerier) SoftDeleteFilesMany(ctx context.Context, ids []pgtype.UUID) error {
	m.softIDs = ids
	return nil
}

func (m *bulkQuerier) HardDeleteFilesMany(ctx context.Context, ids []pgtype.UUID) error {
	m.hardIDs = ids
	return nil
}

func (m *bulkQuerier) CreateAuditLog(ctx context.Context, arg db.CreateAuditLogParams) (db.AuditLog, error) {
	m.actions = append(m.actions, arg.Action)
	return db.AuditLog{}, nil
}

func bulkReq(t *testing.T, method, target, body string, role, userID string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: userID, Role: role})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	return rr, req
}

func TestBulkMove_ViewerForbidden(t *testing.T) {
	h := handler.NewFilesHandler(&bulkQuerier{})
	rr, req := bulkReq(t, http.MethodPost, "/files/bulk/move",
		`{"file_ids":["00000000-0000-0000-0000-000000000001"],"folder_id":""}`, "viewer", selfUUID)
	h.BulkMove(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestBulkMove_EditorCannotMoveOthersFile(t *testing.T) {
	mime := "image/png"
	q := &bulkQuerier{files: []db.File{{ID: uid(1), UploadedBy: uid(2), Status: "active", S3Key: "files/x/a.png", MimeType: &mime}}}
	h := handler.NewFilesHandler(q)
	rr, req := bulkReq(t, http.MethodPost, "/files/bulk/move",
		`{"file_ids":["00000000-0000-0000-0000-000000000001"],"folder_id":""}`, "editor", selfUUID)
	h.BulkMove(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestBulkMove_EditorMovesOwnFile(t *testing.T) {
	mime := "image/png"
	q := &bulkQuerier{files: []db.File{{ID: uid(0x0a), UploadedBy: uid(1), Status: "active", S3Key: "files/x/a.png", MimeType: &mime}}}
	h := handler.NewFilesHandler(q)
	body := `{"file_ids":["00000000-0000-0000-0000-00000000000a"],"folder_id":"00000000-0000-0000-0000-00000000000b"}`
	rr, req := bulkReq(t, http.MethodPost, "/files/bulk/move", body, "editor", selfUUID)
	h.BulkMove(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !q.movedFolder.Valid || q.movedFolder.String() != "00000000-0000-0000-0000-00000000000b" {
		t.Fatalf("folder not moved to expected target: %v", q.movedFolder)
	}
	if len(q.actions) != 1 || q.actions[0] != "file.move" {
		t.Fatalf("audit actions = %v, want [file.move]", q.actions)
	}
}

func TestBulkMove_EmptyIDsBadRequest(t *testing.T) {
	h := handler.NewFilesHandler(&bulkQuerier{})
	rr, req := bulkReq(t, http.MethodPost, "/files/bulk/move", `{"file_ids":[]}`, "editor", selfUUID)
	h.BulkMove(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestBulkDelete_EditorSoftDeletesOwn(t *testing.T) {
	mime := "image/png"
	q := &bulkQuerier{files: []db.File{{ID: uid(0x0a), UploadedBy: uid(1), Status: "active", S3Key: "files/x/a.png", MimeType: &mime}}}
	h := handler.NewFilesHandler(q)
	body := `{"file_ids":["00000000-0000-0000-0000-00000000000a"],"permanent":false}`
	rr, req := bulkReq(t, http.MethodPost, "/files/bulk/delete", body, "editor", selfUUID)
	h.BulkDelete(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if len(q.softIDs) != 1 {
		t.Fatalf("soft delete ids = %v, want 1", q.softIDs)
	}
	if len(q.actions) != 1 || q.actions[0] != "file.delete" {
		t.Fatalf("audit actions = %v, want [file.delete]", q.actions)
	}
}

func TestBulkDelete_EditorCannotPermanent(t *testing.T) {
	mime := "image/png"
	q := &bulkQuerier{files: []db.File{{ID: uid(0x0a), UploadedBy: uid(1), Status: "active", S3Key: "files/x/a.png", MimeType: &mime}}}
	h := handler.NewFilesHandler(q)
	body := `{"file_ids":["00000000-0000-0000-0000-00000000000a"],"permanent":true}`
	rr, req := bulkReq(t, http.MethodPost, "/files/bulk/delete", body, "editor", selfUUID)
	h.BulkDelete(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestBulkDelete_AdminPermanent(t *testing.T) {
	mime := "image/png"
	thumb := "thumbs/x.jpg"
	q := &bulkQuerier{files: []db.File{{ID: uid(1), UploadedBy: uid(3), Status: "active", S3Key: "files/x/a.png", MimeType: &mime, ThumbS3Key: &thumb}}}
	h := handler.NewFilesHandler(q)
	body := `{"file_ids":["00000000-0000-0000-0000-000000000001"],"permanent":true}`
	rr, req := bulkReq(t, http.MethodPost, "/files/bulk/delete", body, "admin", selfUUID)
	h.BulkDelete(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if len(q.hardIDs) != 1 {
		t.Fatalf("hard delete ids = %v, want 1", q.hardIDs)
	}
	if len(q.actions) != 1 || q.actions[0] != "file.hard_delete" {
		t.Fatalf("audit actions = %v, want [file.hard_delete]", q.actions)
	}
}
