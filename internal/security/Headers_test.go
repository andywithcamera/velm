package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got == "" {
		t.Fatal("Referrer-Policy header is missing")
	}
	if got := rec.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("Content-Security-Policy header is missing")
	}
}

func TestSecurityHeadersCSPUsesNonceNotUnsafeInline(t *testing.T) {
	var nonce string
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce = NonceFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "'unsafe-inline'") && scriptSrcDirective(csp) != "" && strings.Contains(scriptSrcDirective(csp), "'unsafe-inline'") {
		t.Fatalf("script-src still allows 'unsafe-inline': %q", csp)
	}
	if nonce == "" {
		t.Fatal("handler saw no CSP nonce in the request context")
	}
	if !strings.Contains(scriptSrcDirective(csp), "'nonce-"+nonce+"'") {
		t.Fatalf("script-src %q does not carry the request nonce %q", scriptSrcDirective(csp), nonce)
	}
}

func TestSecurityHeadersNonceIsUniquePerResponse(t *testing.T) {
	var nonces []string
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonces = append(nonces, NonceFromContext(r.Context()))
	}))

	for i := 0; i < 2; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}
	if len(nonces) != 2 || nonces[0] == "" || nonces[0] == nonces[1] {
		t.Fatalf("nonces = %q, want two distinct non-empty values", nonces)
	}
}

func scriptSrcDirective(csp string) string {
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, "script-src") {
			return directive
		}
	}
	return ""
}
