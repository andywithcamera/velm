package main

import (
	"net/http"
	"net/url"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

func requireAdminTableAccess(w http.ResponseWriter, r *http.Request, tableName string) bool {
	if !db.IsAdminOnlyTableName(tableName) {
		return true
	}

	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		redirectForbiddenTableAccess(w, r)
		return false
	}

	allowed, err := db.UserHasGlobalPermission(r.Context(), userID, "admin")
	if err != nil || !allowed {
		redirectForbiddenTableAccess(w, r)
		return false
	}

	return true
}

func redirectForbiddenTableAccess(w http.ResponseWriter, r *http.Request) {
	target := "/forbidden?required=" + url.QueryEscape("admin")
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}
