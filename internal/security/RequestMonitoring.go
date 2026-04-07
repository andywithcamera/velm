package security

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"velm/internal/db"
	"velm/internal/monitoring"

	"github.com/gorilla/sessions"
)

func Monitor(next http.Handler, store *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldSkipRequestMetrics(r) {
			next.ServeHTTP(w, r)
			return
		}

		startedAt := time.Now()
		observer := newResponseObserver(w)
		metrics := monitoring.NewRequestMetrics(RequestIDFromContext(r.Context()), startedAt)
		ctx := monitoring.WithRequestMetrics(r.Context(), metrics)
		ctx = db.WithRequestMetadataCache(ctx)

		next.ServeHTTP(observer, r.WithContext(ctx))

		snapshot := metrics.Snapshot()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		userID, userEmail, userRole := requestMetricUser(store, r)

		err := db.UpsertRequestMetricServer(ctx, db.RequestMetricRecord{
			RequestID:      snapshot.RequestID,
			StartedAt:      startedAt,
			FinishedAt:     time.Now(),
			RequestSource:  classifyRequestSource(r),
			Method:         r.Method,
			Path:           requestPath(r),
			QueryString:    requestQueryString(r),
			Status:         observer.Status(),
			UserID:         userID,
			UserEmail:      userEmail,
			UserRole:       userRole,
			IP:             requestClientIP(r),
			UserAgent:      r.UserAgent(),
			Referer:        strings.TrimSpace(r.Referer()),
			ContentType:    strings.TrimSpace(r.Header.Get("Content-Type")),
			ServerDuration: time.Since(startedAt),
			DBDuration:     snapshot.DBDuration,
			DBQueryCount:   snapshot.DBQueryCount,
			DBSlowest:      snapshot.DBSlowest,
			DBSlowestQuery: snapshot.DBSlowestQuery,
		})
		if err != nil {
			log.Printf("request metrics write failed: request_id=%s err=%v", snapshot.RequestID, err)
		}
	})
}

func requestMetricUser(store *sessions.CookieStore, r *http.Request) (string, string, string) {
	if store == nil || r == nil {
		return "", "", ""
	}
	session, err := store.Get(r, "mysession")
	if err != nil {
		return "", "", ""
	}
	userID, _ := session.Values["user_id"].(string)
	userEmail, _ := session.Values["user_email"].(string)
	userRole, _ := session.Values["user_role"].(string)
	return strings.TrimSpace(userID), strings.TrimSpace(userEmail), strings.TrimSpace(userRole)
}

func shouldSkipRequestMetrics(r *http.Request) bool {
	path := requestPath(r)
	switch {
	case strings.HasPrefix(path, "/static/"):
		return true
	case path == "/api/monitor/client":
		return true
	case path == "/api/realtime/stream":
		return true
	default:
		return false
	}
}

func classifyRequestSource(r *http.Request) string {
	path := requestPath(r)
	switch {
	case strings.HasPrefix(path, "/static/"):
		return "asset"
	case path == "/api/monitor/client":
		return "client_beacon"
	case path == "/api/realtime/stream":
		return "stream"
	case strings.TrimSpace(r.Header.Get("HX-Request")) != "":
		return "htmx"
	case strings.HasPrefix(path, "/api/"):
		return "api"
	case acceptsHTML(r):
		return "document"
	default:
		return "request"
	}
}

func acceptsHTML(r *http.Request) bool {
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	if accept == "" {
		return r.Method == http.MethodGet
	}
	return strings.Contains(accept, "text/html") || strings.Contains(accept, "application/xhtml+xml")
}

func requestClientIP(r *http.Request) string {
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		return strings.TrimSpace(first)
	}

	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return strings.TrimSpace(host)
	}
	return remoteAddr
}

func requestPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return strings.TrimSpace(r.URL.Path)
}

func requestQueryString(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return strings.TrimSpace(r.URL.RawQuery)
}
