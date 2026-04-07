package auth

import (
	"context"
	"net/http"
	"testing"
)

func TestWithUserContextAndGetters(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}

	ctx := WithUserContext(context.Background(), "u-1", "a@example.com", "Alice", "admin")
	req = req.WithContext(ctx)

	if got := UserIDFromRequest(req); got != "u-1" {
		t.Fatalf("UserIDFromRequest() = %q, want %q", got, "u-1")
	}
	if got := UserEmailFromRequest(req); got != "a@example.com" {
		t.Fatalf("UserEmailFromRequest() = %q, want %q", got, "a@example.com")
	}
	if got := UserNameFromRequest(req); got != "Alice" {
		t.Fatalf("UserNameFromRequest() = %q, want %q", got, "Alice")
	}
	if got := UserRoleFromRequest(req); got != "admin" {
		t.Fatalf("UserRoleFromRequest() = %q, want %q", got, "admin")
	}
}

func TestGettersWithNilRequest(t *testing.T) {
	t.Parallel()

	if got := UserIDFromRequest(nil); got != "" {
		t.Fatalf("expected empty user id, got %q", got)
	}
	if got := UserEmailFromRequest(nil); got != "" {
		t.Fatalf("expected empty user email, got %q", got)
	}
	if got := UserNameFromRequest(nil); got != "" {
		t.Fatalf("expected empty user name, got %q", got)
	}
	if got := UserRoleFromRequest(nil); got != "" {
		t.Fatalf("expected empty user role, got %q", got)
	}
}
