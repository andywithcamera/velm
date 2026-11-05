package authz

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"velm/internal/auth"
	"velm/internal/db"
)

type Permission string

const (
	PermissionView  Permission = "view"
	PermissionWrite Permission = "write"
	PermissionAdmin Permission = "admin"
)

// PermissionResolver answers whether a user holds a permission in an app scope.
//
// Production points at db.UserHasPermission. It is a variable so tests can
// substitute a resolver and exercise the middleware without a database; nothing
// else should reassign it, and production code never does.
var PermissionResolver = db.UserHasPermission

func RequirePermission(permission Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := auth.UserIDFromRequest(r)
		appID := auth.AppIDFromRequest(r)
		if userID == "" {
			redirectForbidden(w, r, permission, appID)
			return
		}

		allowed, err := PermissionResolver(r.Context(), userID, string(permission), appID)
		if err != nil {
			log.Printf("authorization lookup failed for user %s app=%s permission %s: %v", userID, appID, permission, err)
			redirectForbidden(w, r, permission, appID)
			return
		}
		if !allowed {
			redirectForbidden(w, r, permission, appID)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func redirectForbidden(w http.ResponseWriter, r *http.Request, permission Permission, appID string) {
	userID := auth.UserIDFromRequest(r)
	_ = db.LogSecurityEvent(
		context.Background(),
		"authz_denied",
		"warn",
		userID,
		r.URL.Path,
		r.Method,
		r.RemoteAddr,
		r.UserAgent(),
		map[string]any{
			"required_permission": string(permission),
			"app_id":              appID,
		},
	)

	target := "/forbidden?required=" + url.QueryEscape(string(permission))
	if appID != "" {
		target += "&app_id=" + url.QueryEscape(appID)
	}
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}
