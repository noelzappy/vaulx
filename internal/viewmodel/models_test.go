package viewmodel_test

import (
	"testing"
	"time"

	"github.com/brifafrica/vaulx/internal/viewmodel"
)

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tc := range tests {
		got := viewmodel.HumanSize(tc.bytes)
		if got != tc.expected {
			t.Errorf("HumanSize(%d) = %q, want %q", tc.bytes, got, tc.expected)
		}
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Now()
	if got := viewmodel.RelativeTime(now.Add(-30 * time.Second)); got != "just now" {
		t.Errorf("expected 'just now', got %q", got)
	}
	if got := viewmodel.RelativeTime(now.Add(-2 * time.Hour)); got != "2 hours ago" {
		t.Errorf("expected '2 hours ago', got %q", got)
	}
	if got := viewmodel.RelativeTime(now.Add(-1 * time.Minute)); got != "1 minute ago" {
		t.Errorf("expected '1 minute ago', got %q", got)
	}
}
