package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyRequestSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *http.Request
		want string
	}{
		{
			name: "asset",
			req:  httptest.NewRequest(http.MethodGet, "/static/app.js", nil),
			want: "asset",
		},
		{
			name: "client beacon",
			req:  httptest.NewRequest(http.MethodPost, "/api/monitor/client", nil),
			want: "client_beacon",
		},
		{
			name: "stream",
			req:  httptest.NewRequest(http.MethodGet, "/api/realtime/stream?table=t", nil),
			want: "stream",
		},
		{
			name: "htmx",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil)
				req.Header.Set("HX-Request", "true")
				return req
			}(),
			want: "htmx",
		},
		{
			name: "api",
			req:  httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil),
			want: "api",
		},
		{
			name: "document",
			req:  httptest.NewRequest(http.MethodGet, "/task", nil),
			want: "document",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyRequestSource(tt.req); got != tt.want {
				t.Fatalf("classifyRequestSource() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShouldSkipRequestMetrics(t *testing.T) {
	t.Parallel()

	if !shouldSkipRequestMetrics(httptest.NewRequest(http.MethodGet, "/static/app.js", nil)) {
		t.Fatal("static requests should be skipped")
	}
	if !shouldSkipRequestMetrics(httptest.NewRequest(http.MethodPost, "/api/monitor/client", nil)) {
		t.Fatal("client beacon requests should be skipped")
	}
	if !shouldSkipRequestMetrics(httptest.NewRequest(http.MethodGet, "/api/realtime/stream", nil)) {
		t.Fatal("realtime stream requests should be skipped")
	}
	if shouldSkipRequestMetrics(httptest.NewRequest(http.MethodGet, "/task", nil)) {
		t.Fatal("document requests should not be skipped")
	}
}

// Probes are high frequency and low signal; recording them costs a goja emit
// per check on whatever uptime monitor is watching the deployment.
func TestShouldSkipRequestMetricsProbes(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/health", "/healthz", "/version", "/robots.txt"} {
		if !shouldSkipRequestMetrics(httptest.NewRequest(http.MethodGet, path, nil)) {
			t.Fatalf("probe %s should be skipped", path)
		}
	}

	// Near misses must still be recorded.
	for _, path := range []string{"/healthcheck", "/versioned", "/healthz/x"} {
		if shouldSkipRequestMetrics(httptest.NewRequest(http.MethodGet, path, nil)) {
			t.Fatalf("path %s should not be skipped", path)
		}
	}
}

func TestRequestClientIP(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/task", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if got := requestClientIP(req); got != "203.0.113.7" {
		t.Fatalf("requestClientIP() = %q, want %q", got, "203.0.113.7")
	}
}
