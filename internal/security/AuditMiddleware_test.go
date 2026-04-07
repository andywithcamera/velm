package security

import (
	"net/http"
	"testing"
)

func TestShouldAuditMethod(t *testing.T) {
	t.Parallel()

	if !shouldAuditMethod(http.MethodPost) {
		t.Fatal("POST should be audited")
	}
	if !shouldAuditMethod(http.MethodDelete) {
		t.Fatal("DELETE should be audited")
	}
	if shouldAuditMethod(http.MethodGet) {
		t.Fatal("GET should not be audited")
	}
}
