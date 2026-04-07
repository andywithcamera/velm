package security

import (
	"log"
	"net/http"
	"velm/internal/auth"
	"time"
)

func RequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := newResponseObserver(w)

		next.ServeHTTP(recorder, r)

		durationMs := time.Since(start).Milliseconds()
		log.Printf(
			"level=info event=request request_id=%s method=%s path=%s status=%d duration_ms=%d user_id=%s remote=%q ua=%q",
			RequestIDFromContext(r.Context()),
			r.Method,
			r.URL.Path,
			recorder.Status(),
			durationMs,
			auth.UserIDFromRequest(r),
			r.RemoteAddr,
			r.UserAgent(),
		)
	})
}
