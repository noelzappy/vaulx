package thumbnail

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/db"
)

// Worker generates thumbnails for raster images in the background. It mirrors
// the zipjobs worker: claim a pending file, generate + upload the thumbnail,
// then mark the file ready (or failed). One Worker runs inside the server.
type Worker struct {
	Queries     db.Querier
	Fetch       func(ctx context.Context, key string) (io.ReadCloser, error)
	Upload      func(ctx context.Context, key, contentType string, r io.Reader) (int64, error)
	MaxDim      int           // longest edge of the generated thumbnail
	Quality     int           // JPEG quality (1-100)
	Concurrency int           // running jobs; default 2
	PollEvery   time.Duration // claim-loop interval; default 3s
}

// RecoverStale resets thumbnails that a previous process left 'pending' back to
// 'none' so they can be re-claimed. Call once at startup, before Start.
func (w *Worker) RecoverStale(ctx context.Context) error {
	return w.Queries.ResetPendingThumbnails(ctx)
}

// Start launches Concurrency claim loops, all exiting when ctx is done.
func (w *Worker) Start(ctx context.Context) {
	n := w.Concurrency
	if n <= 0 {
		n = 2
	}
	poll := w.PollEvery
	if poll <= 0 {
		poll = 3 * time.Second
	}
	for i := 0; i < n; i++ {
		go func() {
			t := time.NewTicker(poll)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					for w.RunOnce(ctx) { // drain the queue while jobs exist
					}
				}
			}
		}()
	}
}

// RunOnce claims and processes a single pending file. Returns false when the
// queue is empty (or a DB error occurs — next tick retries).
func (w *Worker) RunOnce(ctx context.Context) bool {
	file, err := w.Queries.ClaimPendingThumbnail(ctx)
	if err != nil {
		return false
	}
	w.run(ctx, file)
	return true
}

func (w *Worker) run(ctx context.Context, file db.File) {
	key := ThumbKey(file.ID.String())

	rc, err := w.Fetch(ctx, file.S3Key)
	if err != nil {
		w.fail(ctx, file.ID, fmt.Sprintf("fetch: %v", err))
		return
	}
	defer rc.Close()

	thumb, dw, dh, err := Generate(rc, w.MaxDim, w.Quality)
	if err != nil {
		w.fail(ctx, file.ID, fmt.Sprintf("generate: %v", err))
		return
	}

	if _, err := w.Upload(ctx, key, ThumbContentType, bytes.NewReader(thumb)); err != nil {
		w.fail(ctx, file.ID, fmt.Sprintf("upload: %v", err))
		return
	}

	keyStr := key
	w32, h32 := int32(dw), int32(dh)
	if err := w.Queries.MarkThumbnailReady(ctx, db.MarkThumbnailReadyParams{
		ThumbS3Key:  &keyStr,
		ThumbWidth:  &w32,
		ThumbHeight: &h32,
		ID:          file.ID,
	}); err != nil {
		log.Printf("thumbnail: mark ready %s: %v", file.ID.String(), err)
	}
}

func (w *Worker) fail(ctx context.Context, id pgtype.UUID, msg string) {
	log.Printf("thumbnail: file %s failed: %s", id.String(), msg)
	if err := w.Queries.MarkThumbnailFailed(ctx, db.MarkThumbnailFailedParams{
		ThumbError: &msg,
		ID:         id,
	}); err != nil {
		log.Printf("thumbnail: mark failed %s: %v", id.String(), err)
	}
}
