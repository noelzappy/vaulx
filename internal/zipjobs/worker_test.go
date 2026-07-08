package zipjobs_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/zipbuild"
	"github.com/noelzappy/vaulx/internal/zipjobs"
)

func uuid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

type jobQuerier struct {
	db.Querier
	pending    []db.ZipJob
	readyKey   string
	readySize  int64
	failedErr  string
	staleReset bool
	expired    []db.ZipJob
	expiredIDs []pgtype.UUID
}

func (m *jobQuerier) ClaimNextPendingZipJob(ctx context.Context) (db.ZipJob, error) {
	if len(m.pending) == 0 {
		return db.ZipJob{}, errors.New("no rows")
	}
	j := m.pending[0]
	m.pending = m.pending[1:]
	return j, nil
}

func (m *jobQuerier) MarkZipJobReady(ctx context.Context, arg db.MarkZipJobReadyParams) error {
	if arg.S3Key != nil {
		m.readyKey = *arg.S3Key
	}
	m.readySize = arg.SizeBytes
	return nil
}

func (m *jobQuerier) MarkZipJobFailed(ctx context.Context, arg db.MarkZipJobFailedParams) error {
	if arg.Error != nil {
		m.failedErr = *arg.Error
	}
	return nil
}

func (m *jobQuerier) FailStaleRunningZipJobs(ctx context.Context) error {
	m.staleReset = true
	return nil
}

func (m *jobQuerier) ListExpiredReadyZipJobs(ctx context.Context) ([]db.ZipJob, error) {
	return m.expired, nil
}

func (m *jobQuerier) MarkZipJobExpired(ctx context.Context, id pgtype.UUID) error {
	m.expiredIDs = append(m.expiredIDs, id)
	return nil
}

func testWorker(q db.Querier, uploadErr error) (*zipjobs.Worker, *[]string) {
	var uploaded []string
	w := &zipjobs.Worker{
		Queries: q,
		ListEntries: func(ctx context.Context, folderID pgtype.UUID) ([]zipbuild.Entry, error) {
			return []zipbuild.Entry{{Path: "a.txt", S3Key: "k1"}}, nil
		},
		Fetch: func(ctx context.Context, key string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("payload")), nil
		},
		Upload: func(ctx context.Context, key, contentType string, r io.Reader) (int64, error) {
			if uploadErr != nil {
				return 0, uploadErr
			}
			n, _ := io.Copy(io.Discard, r)
			uploaded = append(uploaded, key)
			return n, nil
		},
		Delete: func(ctx context.Context, key string) error { return nil },
	}
	return w, &uploaded
}

func TestRunOnce_Success(t *testing.T) {
	q := &jobQuerier{pending: []db.ZipJob{{ID: uuid(1), FolderID: uuid(2)}}}
	w, uploaded := testWorker(q, nil)

	if ran := w.RunOnce(context.Background()); !ran {
		t.Fatal("expected a job to run")
	}
	if len(*uploaded) != 1 || (*uploaded)[0] != zipjobs.ZipKey(db.ZipJob{ID: uuid(1), FolderID: uuid(2)}) {
		t.Errorf("uploaded = %v", *uploaded)
	}
	if q.readyKey == "" || q.readySize == 0 {
		t.Errorf("job not marked ready: key=%q size=%d", q.readyKey, q.readySize)
	}
}

func TestRunOnce_UploadFailureMarksFailed(t *testing.T) {
	q := &jobQuerier{pending: []db.ZipJob{{ID: uuid(1), FolderID: uuid(2)}}}
	w, _ := testWorker(q, errors.New("bucket unreachable"))

	w.RunOnce(context.Background())
	if q.failedErr == "" {
		t.Error("expected job marked failed")
	}
}

func TestRunOnce_NoPendingJobs(t *testing.T) {
	w, _ := testWorker(&jobQuerier{}, nil)
	if ran := w.RunOnce(context.Background()); ran {
		t.Error("expected no job to run")
	}
}

func TestRecoverStale(t *testing.T) {
	q := &jobQuerier{}
	w, _ := testWorker(q, nil)
	if err := w.RecoverStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !q.staleReset {
		t.Error("expected FailStaleRunningZipJobs called")
	}
}

func TestCleanupExpired(t *testing.T) {
	key := "zips/x/y.zip"
	q := &jobQuerier{expired: []db.ZipJob{{ID: uuid(9), S3Key: &key}}}
	w, _ := testWorker(q, nil)
	w.CleanupExpired(context.Background())
	if len(q.expiredIDs) != 1 {
		t.Errorf("expected 1 job marked expired, got %d", len(q.expiredIDs))
	}
}
