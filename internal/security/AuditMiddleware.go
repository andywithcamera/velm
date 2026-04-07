package security

import (
	"context"
	"log"
	"net/http"
	"velm/internal/auth"
	"velm/internal/db"
	"time"
)

func Audit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := newResponseObserver(w)

		next.ServeHTTP(recorder, r)

		if !shouldAuditMethod(r.Method) {
			return
		}

		ip := r.Header.Get("X-Forwarded-For")
		if ip == "" {
			ip = r.RemoteAddr
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err := db.WriteAuditLog(ctx, db.AuditEvent{
			UserID:    auth.UserIDFromRequest(r),
			UserEmail: auth.UserEmailFromRequest(r),
			UserRole:  auth.UserRoleFromRequest(r),
			Method:    r.Method,
			Path:      r.URL.Path,
			Status:    recorder.Status(),
			IP:        ip,
			UserAgent: r.UserAgent(),
		})
		if err != nil {
			log.Printf("audit log write failed: %v", err)
		}
	})
}

func shouldAuditMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
