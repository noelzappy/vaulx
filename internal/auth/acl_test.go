package auth_test

import (
	"testing"

	"github.com/noelzappy/vaulx/internal/auth"
)

func TestCanAccess(t *testing.T) {
	admin  := auth.UserContext{ID: "a1", Role: "admin"}
	editor := auth.UserContext{ID: "u1", Role: "editor"}
	viewer := auth.UserContext{ID: "u2", Role: "viewer"}

	tests := []struct {
		name     string
		user     auth.UserContext
		ownerID  string
		expected bool
	}{
		{"admin sees anything",           admin,  "other", true},
		{"editor sees own resource",      editor, "u1",    true},
		{"viewer sees own resource",      viewer, "u2",    true},
		{"editor blocked on other owner", editor, "other", false},
		{"viewer blocked on other owner", viewer, "other", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := auth.CanAccess(tc.user, tc.ownerID)
			if got != tc.expected {
				t.Errorf("CanAccess(%+v, %q) = %v, want %v", tc.user, tc.ownerID, got, tc.expected)
			}
		})
	}
}

func TestCanEdit(t *testing.T) {
	if !auth.CanEdit(auth.UserContext{Role: "admin"}) {
		t.Error("admin should be able to edit")
	}
	if !auth.CanEdit(auth.UserContext{Role: "editor"}) {
		t.Error("editor should be able to edit")
	}
	if auth.CanEdit(auth.UserContext{Role: "viewer"}) {
		t.Error("viewer should not be able to edit")
	}
}
