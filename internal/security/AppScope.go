package security

import (
	"net/http"
	"regexp"
	"velm/internal/auth"
	"strings"
)

var appIDRegex = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func WithAppScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
		if appID == "" {
			appID = strings.TrimSpace(r.Header.Get("X-App-ID"))
		}

		if appID != "" && !appIDRegex.MatchString(appID) {
			http.Error(w, "Invalid app_id", http.StatusBadRequest)
			return
		}

		ctx := auth.WithAppContext(r.Context(), appID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
