// Package zipbuild assembles store-only zip archives from object storage
// entries. It is shared by the streaming download handler and the
// prepared-zip background worker.
package zipbuild

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"
)

type Entry struct {
	Path  string // relative path inside the zip, "/"-separated, no leading slash
	S3Key string // object to fetch; empty for directory entries
	IsDir bool
}

type FetchFunc func(ctx context.Context, s3Key string) (io.ReadCloser, error)

// Build writes a store-only zip of entries to w. Duplicate file paths get
// " (2)"-style suffixes before the extension. Stops at the first error so
// callers never emit an archive that silently misses entries.
func Build(ctx context.Context, w io.Writer, entries []Entry, fetch FetchFunc) error {
	zw := zip.NewWriter(w)
	seen := map[string]bool{}

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.IsDir {
			name := strings.TrimSuffix(e.Path, "/") + "/"
			if seen[name] {
				continue
			}
			seen[name] = true
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store}); err != nil {
				return fmt.Errorf("zipbuild: dir %s: %w", name, err)
			}
			continue
		}

		name := dedupe(e.Path, seen)
		seen[name] = true
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return fmt.Errorf("zipbuild: entry %s: %w", name, err)
		}
		rc, err := fetch(ctx, e.S3Key)
		if err != nil {
			return fmt.Errorf("zipbuild: fetch %s: %w", e.S3Key, err)
		}
		_, err = io.Copy(fw, rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("zipbuild: copy %s: %w", e.S3Key, err)
		}
	}
	return zw.Close()
}

// dedupe returns p, or "base (n).ext" for the first n ≥ 2 not yet taken.
func dedupe(p string, seen map[string]bool) string {
	if !seen[p] {
		return p
	}
	dir, file := path.Split(p)
	ext := path.Ext(file)
	base := strings.TrimSuffix(file, ext)
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s%s (%d)%s", dir, base, n, ext)
		if !seen[cand] {
			return cand
		}
	}
}
