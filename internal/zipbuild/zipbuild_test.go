package zipbuild_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/noelzappy/vaulx/internal/zipbuild"
)

func fakeFetch(objects map[string]string) zipbuild.FetchFunc {
	return func(_ context.Context, key string) (io.ReadCloser, error) {
		body, ok := objects[key]
		if !ok {
			return nil, errors.New("no such key: " + key)
		}
		return io.NopCloser(strings.NewReader(body)), nil
	}
}

func readZip(t *testing.T, buf *bytes.Buffer) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("invalid zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestBuild_TreePathsAndContent(t *testing.T) {
	var buf bytes.Buffer
	entries := []zipbuild.Entry{
		{Path: "a.txt", S3Key: "k1"},
		{Path: "sub/b.txt", S3Key: "k2"},
	}
	err := zipbuild.Build(context.Background(), &buf, entries, fakeFetch(map[string]string{"k1": "one", "k2": "two"}))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := readZip(t, &buf)
	if got["a.txt"] != "one" || got["sub/b.txt"] != "two" {
		t.Errorf("unexpected zip contents: %#v", got)
	}
}

func TestBuild_StoreMethodOnly(t *testing.T) {
	var buf bytes.Buffer
	_ = zipbuild.Build(context.Background(), &buf,
		[]zipbuild.Entry{{Path: "a.txt", S3Key: "k1"}},
		fakeFetch(map[string]string{"k1": "payload"}))
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("invalid zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Errorf("%s: method = %d, want zip.Store", f.Name, f.Method)
		}
	}
}

func TestBuild_DedupesDuplicateNames(t *testing.T) {
	var buf bytes.Buffer
	entries := []zipbuild.Entry{
		{Path: "report.pdf", S3Key: "k1"},
		{Path: "report.pdf", S3Key: "k2"},
		{Path: "report.pdf", S3Key: "k3"},
	}
	err := zipbuild.Build(context.Background(), &buf, entries,
		fakeFetch(map[string]string{"k1": "1", "k2": "2", "k3": "3"}))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := readZip(t, &buf)
	for _, name := range []string{"report.pdf", "report (2).pdf", "report (3).pdf"} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing entry %q; have %#v", name, got)
		}
	}
}

func TestBuild_EmptyDirectoryEntries(t *testing.T) {
	var buf bytes.Buffer
	entries := []zipbuild.Entry{{Path: "empty-folder", IsDir: true}}
	if err := zipbuild.Build(context.Background(), &buf, entries, fakeFetch(nil)); err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := readZip(t, &buf)
	if _, ok := got["empty-folder/"]; !ok {
		t.Errorf("missing directory entry, have %#v", got)
	}
}

func TestBuild_FetchErrorStops(t *testing.T) {
	var buf bytes.Buffer
	entries := []zipbuild.Entry{{Path: "a.txt", S3Key: "missing"}}
	if err := zipbuild.Build(context.Background(), &buf, entries, fakeFetch(nil)); err == nil {
		t.Fatal("expected error for missing object")
	}
}
