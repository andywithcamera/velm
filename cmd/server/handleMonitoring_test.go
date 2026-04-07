package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleMonitoringViewRedirectsToRequestMetricTable(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/admin/monitoring", nil)
	rec := httptest.NewRecorder()

	handleMonitoringView(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/t/_request_metric" {
		t.Fatalf("location = %q, want %q", got, "/t/_request_metric")
	}
}

func TestHandleMonitoringViewPreservesQueryString(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/admin/monitoring?q=status=500", nil)
	rec := httptest.NewRecorder()

	handleMonitoringView(rec, req)

	if got := rec.Header().Get("Location"); got != "/t/_request_metric?q=status=500" {
		t.Fatalf("location = %q, want %q", got, "/t/_request_metric?q=status=500")
	}
}

func TestParseMetricInt(t *testing.T) {
	t.Parallel()

	if got := parseMetricInt("123"); got != 123 {
		t.Fatalf("parseMetricInt(valid) = %d, want %d", got, 123)
	}
	if got := parseMetricInt(""); got != -1 {
		t.Fatalf("parseMetricInt(empty) = %d, want %d", got, -1)
	}
	if got := parseMetricInt("oops"); got != -1 {
		t.Fatalf("parseMetricInt(invalid) = %d, want %d", got, -1)
	}
}
