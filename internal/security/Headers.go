package security

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"os"
	"strings"
)

type nonceContextKey string

const nonceKey nonceContextKey = "csp_nonce"

// NonceFromContext returns the per-response CSP nonce, or "" when the request
// did not pass through SecurityHeaders (tests, background work).
func NonceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	nonce, _ := ctx.Value(nonceKey).(string)
	return nonce
}

// newNonce returns 144 bits of randomness in URL-safe base64. The alphabet is
// [A-Za-z0-9_-]: CSP's base64-value grammar allows those characters, and unlike
// standard base64 it never emits '+', '/', or '=', so html/template writes the
// nonce into the attribute verbatim. Standard base64 broke that: '+' came out as
// "&#43;", so the markup no longer matched the header byte for byte.
func newNonce() string {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		// Without a nonce the CSP blocks every inline script, which fails closed.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func SecurityHeaders(next http.Handler) http.Handler {
	enableHSTS := strings.EqualFold(os.Getenv("SECURITY_HSTS"), "true")
	if !enableHSTS {
		enableHSTS = strings.EqualFold(os.Getenv("SESSION_COOKIE_SECURE"), "true")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := newNonce()
		csp := buildCSP(nonce)
		if nonce != "" {
			r = r.WithContext(context.WithValue(r.Context(), nonceKey, nonce))
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")

		if enableHSTS {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

func buildCSP(nonce string) string {
	scriptSrc := "script-src 'self' https://unpkg.com https://cdn.jsdelivr.net https://esm.sh"
	if nonce != "" {
		scriptSrc = "script-src 'self' 'nonce-" + nonce + "' https://unpkg.com https://cdn.jsdelivr.net https://esm.sh"
	}
	return strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src 'self' https://esm.sh",
		"form-action 'self'",
		scriptSrc,
		"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://esm.sh",
	}, "; ")
}
