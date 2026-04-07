package authz

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequirePermissionFailsWithoutUser(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	RequirePermission(PermissionView, next).ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected status %d, got %d", http.StatusFound, rec.Code)
	}
	location := rec.Header().Get("Location")
	if location == "" {
		t.Fatal("expected redirect location for forbidden response")
	}
}
