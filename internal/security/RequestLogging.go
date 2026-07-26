package security

import (
	"log"
	"net/http"
	"strings"
	"time"
	"velm/internal/auth"
)

// quietPaths are high-frequency polling/streaming paths logged only on server errors.
var quietPaths = []string{
	"/api/notifications/panel",
	"/api/menu/",
	"/api/monitor/client",
	"/api/realtime/stream",
}

func RequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := newResponseObserver(w)

		next.ServeHTTP(recorder, r)

		status := recorder.Status()
		if status < 500 {
			for _, p := range quietPaths {
				if strings.HasPrefix(r.URL.Path, p) {
					return
				}
			}
		}

		durationMs := time.Since(start).Milliseconds()
		log.Printf(
			"level=info event=request request_id=%s method=%s path=%s status=%d duration_ms=%d user_id=%s remote=%q ua=%q",
			RequestIDFromContext(r.Context()),
			r.Method,
			r.URL.Path,
			status,
			durationMs,
			auth.UserIDFromRequest(r),
			r.RemoteAddr,
			r.UserAgent(),
		)
	})
}
