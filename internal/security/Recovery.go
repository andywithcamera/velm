package security

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

func RecoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				requestID := RequestIDFromContext(r.Context())
				log.Printf(
					"level=error event=panic request_id=%s method=%s path=%s panic=%v",
					requestID,
					r.Method,
					r.URL.Path,
					rec,
				)

				if r.Header.Get("HX-Request") != "" || strings.HasPrefix(r.URL.Path, "/api/") {
					http.Error(w, "Internal server error", http.StatusInternalServerError)
					return
				}

				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(fmt.Sprintf(`
<!doctype html>
<html lang="en">
  <body style="font-family:sans-serif;padding:24px;max-width:700px;margin:0 auto;">
    <h1>Something went wrong</h1>
    <p>The server hit an unexpected error.</p>
    <p><strong>Request ID:</strong> %s</p>
    <p><a href="/">Return home</a></p>
  </body>
</html>
`, requestID)))
			}
		}()

		next.ServeHTTP(w, r)
	})
}
