package thumbnail_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/thumbnail"
)

func uuid(b byte) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte{b}, Valid: true}
}

func tinyPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for x := 0; x < 20; x++ {
		for y := 0; y < 10; y++ {
			img.SetRGBA(x, y, color.RGBA{1, 2, 3, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

type querier struct {
	db.Querier
	pending   []db.File
	readyKey  *string
	readyW    *int32
	readyH    *int32
	failedErr *string
	reset     bool
	resetFail bool
}

func (m *querier) ClaimPendingThumbnail(ctx context.Context) (db.File, error) {
	if len(m.pending) == 0 {
		return db.File{}, errors.New("no rows")
	}
	f := m.pending[0]
	m.pending = m.pending[1:]
	return f, nil
}

func (m *querier) MarkThumbnailReady(ctx context.Context, arg db.MarkThumbnailReadyParams) error {
	m.readyKey = arg.ThumbS3Key
	m.readyW = arg.ThumbWidth
	m.readyH = arg.ThumbHeight
	return nil
}

func (m *querier) MarkThumbnailFailed(ctx context.Context, arg db.MarkThumbnailFailedParams) error {
	m.failedErr = arg.ThumbError
	return nil
}

func (m *querier) ResetPendingThumbnails(ctx context.Context) error {
	m.reset = true
	return nil
}

func (m *querier) ResetFailedThumbnails(ctx context.Context) error {
	m.resetFail = true
	return nil
}

func TestWorkerSuccess(t *testing.T) {
	mime := "image/png"
	q := &querier{pending: []db.File{{ID: uuid(1), S3Key: "files/x/a.png", Status: "active", MimeType: &mime}}}
	var uploadedKey, uploadCT string
	w := &thumbnail.Worker{
		Queries: q,
		Fetch: func(ctx context.Context, key string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(tinyPNG())), nil
		},
		Upload: func(ctx context.Context, key, ct string, r io.Reader) (int64, error) {
			uploadedKey = key
			uploadCT = ct
			return 1, nil
		},
	}

	if !w.RunOnce(context.Background()) {
		t.Fatal("expected a job")
	}
	wantKey := "thumbs/01000000-0000-0000-0000-000000000000.jpg"
	if uploadedKey != wantKey {
		t.Fatalf("upload key = %q, want %q", uploadedKey, wantKey)
	}
	if uploadCT != thumbnail.ThumbContentType {
		t.Fatalf("content type = %q", uploadCT)
	}
	if q.readyKey == nil || *q.readyKey != wantKey {
		t.Fatalf("ready key = %v", q.readyKey)
	}
	if q.readyW == nil || q.readyH == nil || *q.readyW <= 0 || *q.readyH <= 0 {
		t.Fatalf("ready dims = %v x %v", q.readyW, q.readyH)
	}
	if q.failedErr != nil {
		t.Fatalf("unexpected failure: %v", *q.failedErr)
	}
}

func TestWorkerFetchError(t *testing.T) {
	mime := "image/png"
	q := &querier{pending: []db.File{{ID: uuid(2), S3Key: "files/x/b.png", Status: "active", MimeType: &mime}}}
	w := &thumbnail.Worker{
		Queries: q,
		Fetch:   func(ctx context.Context, key string) (io.ReadCloser, error) { return nil, errors.New("boom") },
	}
	if !w.RunOnce(context.Background()) {
		t.Fatal("expected a job")
	}
	if q.failedErr == nil {
		t.Fatal("expected failure recorded")
	}
}

func TestWorkerRecoverStale(t *testing.T) {
	q := &querier{}
	w := &thumbnail.Worker{Queries: q}
	if err := w.RecoverStale(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !q.reset {
		t.Fatal("expected RecoverStale to reset pending")
	}
	if !q.resetFail {
		t.Fatal("expected RecoverStale to reset failed")
	}
}
