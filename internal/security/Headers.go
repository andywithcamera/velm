package security

import (
	"net/http"
	"os"
	"strings"
)

func SecurityHeaders(next http.Handler) http.Handler {
	csp := buildCSP()
	enableHSTS := strings.EqualFold(os.Getenv("SECURITY_HSTS"), "true")
	if !enableHSTS {
		enableHSTS = strings.EqualFold(os.Getenv("SESSION_COOKIE_SECURE"), "true")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func buildCSP() string {
	return strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src 'self' https://esm.sh",
		"form-action 'self'",
		"script-src 'self' 'unsafe-inline' https://unpkg.com https://cdn.jsdelivr.net https://esm.sh",
		"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://esm.sh",
	}, "; ")
}
