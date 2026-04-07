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

func TestRequestClientIP(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/task", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if got := requestClientIP(req); got != "203.0.113.7" {
		t.Fatalf("requestClientIP() = %q, want %q", got, "203.0.113.7")
	}
}
