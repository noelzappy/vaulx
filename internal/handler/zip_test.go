package handler_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/handler"
)

func uuid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

// zipQuerier serves a two-level tree: root(0x0A) with sub(0x0B);
// f1.txt owned by 0xA1 in root, f2.txt owned by 0xA2 in sub.
type zipQuerier struct {
	db.Querier
	audited []string
}

func (m *zipQuerier) GetFolder(ctx context.Context, id pgtype.UUID) (db.Folder, error) {
	if id == uuid(0x0B) {
		return db.Folder{ID: id, Name: "sub", ParentID: uuid(0x0A)}, nil
	}
	return db.Folder{ID: id, Name: "root"}, nil
}

func (m *zipQuerier) ListFolderTreeFolders(ctx context.Context, id pgtype.UUID) ([]db.ListFolderTreeFoldersRow, error) {
	return []db.ListFolderTreeFoldersRow{
		{ID: uuid(0x0A), Relpath: ""},
		{ID: uuid(0x0B), Relpath: "sub"},
	}, nil
}

func (m *zipQuerier) ListFolderTreeFiles(ctx context.Context, id pgtype.UUID) ([]db.ListFolderTreeFilesRow, error) {
	return []db.ListFolderTreeFilesRow{
		{ID: uuid(1), Name: "f1.txt", S3Key: "k1", SizeBytes: 3, UploadedBy: uuid(0xA1), Relpath: ""},
		{ID: uuid(2), Name: "f2.txt", S3Key: "k2", SizeBytes: 3, UploadedBy: uuid(0xA2), Relpath: "sub"},
	}, nil
}

func (m *zipQuerier) CreateAuditLog(ctx context.Context, arg db.CreateAuditLogParams) (db.AuditLog, error) {
	m.audited = append(m.audited, arg.Action)
	return db.AuditLog{}, nil
}

func fetchFrom(objects map[string]string) func(context.Context, string) (io.ReadCloser, error) {
	return func(_ context.Context, key string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(objects[key])), nil
	}
}

func streamZipRequest(t *testing.T, q db.Querier, user auth.UserContext) *httptest.ResponseRecorder {
	t.Helper()
	h := handler.NewZipHandler(q, handler.NewShareHandler(q))
	h.Fetch = fetchFrom(map[string]string{"k1": "one", "k2": "two"})

	req := httptest.NewRequest(http.MethodGet, "/files/0a000000-0000-0000-0000-000000000000/zip", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("folderID", "0a000000-0000-0000-0000-000000000000")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.SetCurrentUser(req.Context(), user))
	rr := httptest.NewRecorder()
	h.StreamZip(rr, req)
	return rr
}

func entryNames(t *testing.T, body *bytes.Buffer) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body.Bytes()), int64(body.Len()))
	if err != nil {
		t.Fatalf("response is not a zip: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

func TestStreamZip_AdminGetsFullTree(t *testing.T) {
	q := &zipQuerier{}
	rr := streamZipRequest(t, q, auth.UserContext{ID: "admin", Role: "admin"})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="root.zip"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	names := entryNames(t, rr.Body)
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "f1.txt") || !strings.Contains(joined, "sub/f2.txt") {
		t.Errorf("entries = %v", names)
	}
	if len(q.audited) == 0 || q.audited[0] != "folder.zip_download" {
		t.Errorf("audit log = %v", q.audited)
	}
}

func TestStreamZip_EditorOnlyOwnFiles(t *testing.T) {
	// editor owns only f1 (uploadedBy 0xA1)
	ownerID := pgtype.UUID{Bytes: [16]byte{0xA1}, Valid: true}.String()
	rr := streamZipRequest(t, &zipQuerier{}, auth.UserContext{ID: ownerID, Role: "editor"})
	names := strings.Join(entryNames(t, rr.Body), ",")
	if !strings.Contains(names, "f1.txt") {
		t.Errorf("expected own file present, entries = %v", names)
	}
	if strings.Contains(names, "f2.txt") {
		t.Errorf("expected other user's file excluded, entries = %v", names)
	}
}

func TestStreamZip_Unauthenticated(t *testing.T) {
	h := handler.NewZipHandler(&zipQuerier{}, handler.NewShareHandler(&zipQuerier{}))
	req := httptest.NewRequest(http.MethodGet, "/files/x/zip", nil)
	rr := httptest.NewRecorder()
	h.StreamZip(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// shareZipQuerier wraps zipQuerier with a folder share for root 0x0A.
type shareZipQuerier struct {
	zipQuerier
	expired bool
}

func (m *shareZipQuerier) GetShareBySlug(ctx context.Context, slug string) (db.Share, error) {
	sh := db.Share{ID: uuid(0x51), Slug: slug, FolderID: uuid(0x0A)}
	if m.expired {
		sh.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	}
	return sh, nil
}

func sharedZipRequest(t *testing.T, q db.Querier, folderParam string) *httptest.ResponseRecorder {
	t.Helper()
	h := handler.NewZipHandler(q, handler.NewShareHandler(q))
	h.Fetch = fetchFrom(map[string]string{"k1": "one", "k2": "two"})
	url := "/s/abc/zip"
	if folderParam != "" {
		url += "?folder=" + folderParam
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", "abc")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.SharedStreamZip(rr, req)
	return rr
}

func TestSharedStreamZip_FullTreeNoOwnerFilter(t *testing.T) {
	rr := sharedZipRequest(t, &shareZipQuerier{}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	names := strings.Join(entryNames(t, rr.Body), ",")
	if !strings.Contains(names, "f1.txt") || !strings.Contains(names, "sub/f2.txt") {
		t.Errorf("entries = %v", names)
	}
}

func TestSharedStreamZip_SubfolderInsideTree(t *testing.T) {
	rr := sharedZipRequest(t, &shareZipQuerier{}, "0b000000-0000-0000-0000-000000000000")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestSharedStreamZip_ExpiredShareGone(t *testing.T) {
	rr := sharedZipRequest(t, &shareZipQuerier{expired: true}, "")
	if rr.Code != http.StatusGone {
		t.Errorf("status = %d, want 410", rr.Code)
	}
}

// prepQuerier extends zipQuerier with zip-job bookkeeping.
type prepQuerier struct {
	zipQuerier
	created  []db.CreateZipJobParams
	reusable *db.ZipJob
	byID     map[[16]byte]db.ZipJob
}

func (m *prepQuerier) CreateZipJob(ctx context.Context, arg db.CreateZipJobParams) (db.ZipJob, error) {
	m.created = append(m.created, arg)
	return db.ZipJob{ID: uuid(0x77), FolderID: arg.FolderID, Status: "pending"}, nil
}

func (m *prepQuerier) GetReusableZipJob(ctx context.Context, arg db.GetReusableZipJobParams) (db.ZipJob, error) {
	if m.reusable != nil {
		return *m.reusable, nil
	}
	return db.ZipJob{}, errors.New("no rows")
}

func (m *prepQuerier) GetZipJob(ctx context.Context, id pgtype.UUID) (db.ZipJob, error) {
	if j, ok := m.byID[id.Bytes]; ok {
		return j, nil
	}
	return db.ZipJob{}, errors.New("no rows")
}

func prepareRequest(t *testing.T, q db.Querier) *httptest.ResponseRecorder {
	t.Helper()
	h := handler.NewZipHandler(q, handler.NewShareHandler(q))
	req := httptest.NewRequest(http.MethodPost, "/files/0a000000-0000-0000-0000-000000000000/zip/prepare", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("folderID", "0a000000-0000-0000-0000-000000000000")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "admin", Role: "admin"}))
	rr := httptest.NewRecorder()
	h.PrepareZip(rr, req)
	return rr
}

func TestPrepareZip_CreatesJobWithSnapshot(t *testing.T) {
	q := &prepQuerier{}
	rr := prepareRequest(t, q)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if len(q.created) != 1 {
		t.Fatalf("created %d jobs, want 1", len(q.created))
	}
	// zipQuerier tree: 2 files x 3 bytes
	if q.created[0].FileCount != 2 || q.created[0].ContentBytes != 6 {
		t.Errorf("snapshot = %+v", q.created[0])
	}
	if !strings.Contains(rr.Body.String(), "Preparing zip") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestPrepareZip_ReusesExistingJob(t *testing.T) {
	q := &prepQuerier{reusable: &db.ZipJob{ID: uuid(0x66), Status: "running"}}
	rr := prepareRequest(t, q)
	if len(q.created) != 0 {
		t.Errorf("expected no new job, created %d", len(q.created))
	}
	if !strings.Contains(rr.Body.String(), "Preparing zip") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestZipStatus_Ready(t *testing.T) {
	key := "zips/a/b.zip"
	job := db.ZipJob{ID: uuid(0x66), Status: "ready", S3Key: &key}
	q := &prepQuerier{reusable: &job, byID: map[[16]byte]db.ZipJob{{0x66}: job}}
	h := handler.NewZipHandler(q, handler.NewShareHandler(q))
	h.Presign = func(ctx context.Context, k, filename string) (string, error) {
		return "https://signed.example/" + k, nil
	}
	req := httptest.NewRequest(http.MethodGet, "/files/0a000000-0000-0000-0000-000000000000/zip/status", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("folderID", "0a000000-0000-0000-0000-000000000000")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "admin", Role: "admin"}))
	rr := httptest.NewRecorder()
	h.ZipStatus(rr, req)
	if !strings.Contains(rr.Body.String(), "https://signed.example/zips/a/b.zip") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestSharedPrepareZip_ExpiredShareGone(t *testing.T) {
	sq := &shareZipQuerier{expired: true}
	h := handler.NewZipHandler(sq, handler.NewShareHandler(sq))
	req := httptest.NewRequest(http.MethodPost, "/s/abc/zip/prepare", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", "abc")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.SharedPrepareZip(rr, req)
	if rr.Code != http.StatusGone {
		t.Errorf("status = %d, want 410", rr.Code)
	}
}
