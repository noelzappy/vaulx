package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brifafrica/vaulx/internal/auth"
	"github.com/brifafrica/vaulx/internal/handler"
)

func TestDeleteFile_HardDelete_NonAdminForbidden(t *testing.T) {
	h := handler.NewFilesHandler(nil)

	for _, role := range []string{"editor", "viewer"} {
		req := httptest.NewRequest(http.MethodDelete, "/files/some-id?permanent=true", nil)
		ctx := auth.SetCurrentUser(req.Context(), auth.UserContext{ID: "u1", Role: role})
		ctx = withChiParam(ctx, "fileID", "some-id")
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		h.DeleteFile(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("role %q: expected 403, got %d", role, rr.Code)
		}
	}
}
