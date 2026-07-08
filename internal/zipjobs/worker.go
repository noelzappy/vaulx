// Package zipjobs runs prepared-zip background jobs: it claims pending
// zip_jobs rows, builds the archive into the bucket, and cleans up expired
// archives. One Worker runs inside the server process.
package zipjobs

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/zipbuild"
)

type ListEntriesFunc func(ctx context.Context, folderID pgtype.UUID) ([]zipbuild.Entry, error)

type Worker struct {
	Queries     db.Querier
	ListEntries ListEntriesFunc
	Fetch       zipbuild.FetchFunc
	Upload      func(ctx context.Context, key, contentType string, r io.Reader) (int64, error)
	Delete      func(ctx context.Context, key string) error
	Concurrency int           // running-job cap; default 2
	PollEvery   time.Duration // claim-loop interval; default 2s
}

func ZipKey(job db.ZipJob) string {
	return fmt.Sprintf("zips/%s/%s.zip", job.FolderID.String(), job.ID.String())
}

// RecoverStale marks jobs left 'running' by a previous process as failed.
// Call once at startup, before Start.
func (w *Worker) RecoverStale(ctx context.Context) error {
	return w.Queries.FailStaleRunningZipJobs(ctx)
}

// Start launches Concurrency claim loops and an hourly cleanup loop, all
// exiting when ctx is done. Returns immediately.
func (w *Worker) Start(ctx context.Context) {
	n := w.Concurrency
	if n <= 0 {
		n = 2
	}
	poll := w.PollEvery
	if poll <= 0 {
		poll = 2 * time.Second
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
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.CleanupExpired(ctx)
			}
		}
	}()
}

// RunOnce claims and runs a single pending job. Returns false when the
// queue is empty.
func (w *Worker) RunOnce(ctx context.Context) bool {
	job, err := w.Queries.ClaimNextPendingZipJob(ctx)
	if err != nil {
		return false // no pending rows (or db error — next tick retries)
	}
	w.run(ctx, job)
	return true
}

func (w *Worker) run(ctx context.Context, job db.ZipJob) {
	entries, err := w.ListEntries(ctx, job.FolderID)
	if err != nil {
		w.fail(ctx, job, fmt.Sprintf("list folder: %v", err))
		return
	}

	key := ZipKey(job)
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(zipbuild.Build(ctx, pw, entries, w.Fetch))
	}()
	size, err := w.Upload(ctx, key, "application/zip", pr)
	if err != nil {
		pr.CloseWithError(err)
		w.fail(ctx, job, fmt.Sprintf("upload: %v", err))
		return
	}

	if err := w.Queries.MarkZipJobReady(ctx, db.MarkZipJobReadyParams{
		ID:        job.ID,
		S3Key:     &key,
		SizeBytes: size,
	}); err != nil {
		log.Printf("zipjobs: mark ready %s: %v", job.ID.String(), err)
	}
}

func (w *Worker) fail(ctx context.Context, job db.ZipJob, msg string) {
	log.Printf("zipjobs: job %s failed: %s", job.ID.String(), msg)
	if err := w.Queries.MarkZipJobFailed(ctx, db.MarkZipJobFailedParams{ID: job.ID, Error: &msg}); err != nil {
		log.Printf("zipjobs: mark failed %s: %v", job.ID.String(), err)
	}
}

// CleanupExpired deletes bucket objects for expired ready zips and marks
// their rows.
func (w *Worker) CleanupExpired(ctx context.Context) {
	jobs, err := w.Queries.ListExpiredReadyZipJobs(ctx)
	if err != nil {
		log.Printf("zipjobs: list expired: %v", err)
		return
	}
	for _, j := range jobs {
		if j.S3Key != nil {
			if err := w.Delete(ctx, *j.S3Key); err != nil {
				log.Printf("zipjobs: delete %s: %v", *j.S3Key, err)
				continue // retry next sweep; keep the row until the object is gone
			}
		}
		if err := w.Queries.MarkZipJobExpired(ctx, j.ID); err != nil {
			log.Printf("zipjobs: mark expired %s: %v", j.ID.String(), err)
		}
	}
}
