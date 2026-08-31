// Command thumbnail-backfill generates thumbnails for images that were
// uploaded before the thumbnail pipeline existed, or that failed earlier.
//
// Usage:
//   go run ./cmd/thumbnail-backfill [--retry-failed] [--limit N]
//
// It is idempotent: files that already have a thumbnail are skipped, and it
// can be run as often as needed.
package main

import (
	"bytes"
	"context"
	"flag"
	"log"

	"github.com/joho/godotenv"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/internal/thumbnail"
)

func main() {
	retryFailed := flag.Bool("retry-failed", false, "re-process files that previously failed")
	limit := flag.Int("limit", 0, "max image files to process (0 = all)")
	flag.Parse()

	_ = godotenv.Load()
	ctx := context.Background()

	if err := storage.Connect(ctx); err != nil {
		log.Fatalf("fatal: cannot connect to S3 storage: %v", err)
	}
	db.Connect(ctx)
	queries := db.New(db.DB)

	total := *limit
	if total <= 0 {
		total = 50000 // practical cap for a one-shot script
	}

	files, err := queries.ListFilesMissingThumbnails(ctx, int32(total))
	if err != nil {
		log.Fatalf("list files: %v", err)
	}

	processed := 0
	skipped := 0
	for _, f := range files {
		if f.ThumbStatus == "failed" && !*retryFailed {
			skipped++
			continue
		}
		if err := process(ctx, queries, f); err != nil {
			log.Printf("skip %s: %v", f.ID.String(), err)
			msg := err.Error()
			_ = queries.MarkThumbnailFailed(ctx, db.MarkThumbnailFailedParams{
				ThumbError: &msg,
				ID:         f.ID,
			})
			continue
		}
		processed++
		log.Printf("thumbnailed %s", f.ID.String())
	}

	log.Printf("thumbnail-backfill: %d processed, %d skipped (%d missing matched)",
		processed, skipped, len(files))
}

func process(ctx context.Context, q db.Querier, f db.File) error {
	rc, err := storage.GetObjectStream(ctx, f.S3Key)
	if err != nil {
		return err
	}
	defer rc.Close()

	thumb, w, h, err := thumbnail.Generate(rc, thumbnail.DefaultMaxDim, thumbnail.DefaultQuality)
	if err != nil {
		return err
	}

	key := thumbnail.ThumbKey(f.ID.String())
	if _, err := storage.UploadStream(ctx, key, thumbnail.ThumbContentType, bytes.NewReader(thumb)); err != nil {
		return err
	}

	keyStr := key
	w32, h32 := int32(w), int32(h)
	return q.MarkThumbnailReady(ctx, db.MarkThumbnailReadyParams{
		ThumbS3Key:  &keyStr,
		ThumbWidth:  &w32,
		ThumbHeight: &h32,
		ID:          f.ID,
	})
}
