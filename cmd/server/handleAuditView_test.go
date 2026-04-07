package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleAuditViewRedirectsToSystemLogTable(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
	rec := httptest.NewRecorder()

	handleAuditView(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/t/_audit_log" {
		t.Fatalf("location = %q, want %q", got, "/t/_audit_log")
	}
}

func TestHandleAuditViewPreservesQueryString(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/audit?q=status=500", nil)
	rec := httptest.NewRecorder()

	handleAuditView(rec, req)

	if got := rec.Header().Get("Location"); got != "/t/_audit_log?q=status=500" {
		t.Fatalf("location = %q, want %q", got, "/t/_audit_log?q=status=500")
	}
}
