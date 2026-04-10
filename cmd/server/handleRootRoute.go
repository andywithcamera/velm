package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"

	"velm/internal/db"
)

const authenticatedRootRoutePropertyKey = "authenticated_root_route_target"
const defaultAuthenticatedRootRouteTarget = "/task"

func handleRootRoute(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	target := resolveRootRouteTarget(r.Context())
	switch {
	case target == "login":
		if requestIsAuthenticated(r) {
			http.Redirect(w, r, defaultAuthenticatedRoute(r.Context()), http.StatusFound)
			return
		}
		handleLoginPage(w, r)
		return
	case strings.HasPrefix(target, "page:"):
		slug := normalizePageRouteSlug(strings.TrimSpace(strings.TrimPrefix(target, "page:")))
		if slug == "" {
			handleLoginPage(w, r)
			return
		}
		renderPageBySlug(w, r, slug, "Public")
		return
	case strings.HasPrefix(target, "/p/"):
		slug := normalizePageRouteSlug(strings.TrimSpace(strings.TrimPrefix(target, "/p/")))
		if slug == "" {
			handleLoginPage(w, r)
			return
		}
		renderPageBySlug(w, r, slug, "Public")
		return
	case strings.HasPrefix(target, "table:"):
		table := strings.TrimSpace(strings.TrimPrefix(target, "table:"))
		if db.IsSafeIdentifier(table) {
			http.Redirect(w, r, "/t/"+table, http.StatusFound)
			return
		}
	case strings.HasPrefix(target, "/t/"):
		table := strings.TrimSpace(strings.TrimPrefix(target, "/t/"))
		if db.IsSafeIdentifier(table) {
			http.Redirect(w, r, "/t/"+table, http.StatusFound)
			return
		}
	}

	handleLoginPage(w, r)
}

func requestIsAuthenticated(r *http.Request) bool {
	if store == nil {
		return false
	}
	session, err := store.Get(r, "mysession")
	if err != nil {
		return false
	}
	authenticated, _ := session.Values["authenticated"].(bool)
	return authenticated
}

func defaultAuthenticatedRoute(ctx context.Context) string {
	target := resolveAuthenticatedRouteTarget(ctx)
	switch {
	case strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") && !strings.HasPrefix(target, "/\\"):
		if path := normalizeAuthenticatedRoutePath(target); path != "" {
			return path
		}
	case strings.HasPrefix(target, "page:"):
		slug := normalizePageRouteSlug(strings.TrimSpace(strings.TrimPrefix(target, "page:")))
		if slug != "" {
			return "/p/" + slug
		}
	case strings.HasPrefix(target, "table:"):
		table := strings.TrimSpace(strings.TrimPrefix(target, "table:"))
		if db.IsSafeIdentifier(table) {
			return "/t/" + table
		}
	}
	return defaultAuthenticatedRootRouteTarget
}

func resolveRootRouteTarget(ctx context.Context) string {
	var value string
	err := db.Pool.QueryRow(ctx, `SELECT value FROM _property WHERE key = 'root_route_target' LIMIT 1`).Scan(&value)
	if err == nil {
		v := strings.TrimSpace(strings.ToLower(value))
		if v != "" {
			return v
		}
	}
	if err != nil && err != sql.ErrNoRows {
		return "login"
	}
	if fallback, ok := propertyItems["root_route_target"]; ok {
		if s, ok := fallback.(string); ok {
			s = strings.TrimSpace(strings.ToLower(s))
			if s != "" {
				return s
			}
		}
	}
	return "login"
}

func resolveAuthenticatedRouteTarget(ctx context.Context) string {
	var value string
	err := db.Pool.QueryRow(ctx, `SELECT value FROM _property WHERE key = $1 LIMIT 1`, authenticatedRootRoutePropertyKey).Scan(&value)
	if err == nil {
		if target := normalizeAuthenticatedRouteTarget(value); target != "" {
			return target
		}
	}
	if err != nil && err != sql.ErrNoRows {
		return defaultAuthenticatedRootRouteTarget
	}
	if fallback, ok := propertyItems[authenticatedRootRoutePropertyKey]; ok {
		if s, ok := fallback.(string); ok {
			if target := normalizeAuthenticatedRouteTarget(s); target != "" {
				return target
			}
		}
	}
	return defaultAuthenticatedRootRouteTarget
}

func normalizeAuthenticatedRouteTarget(input string) string {
	target := strings.TrimSpace(input)
	lowerTarget := strings.ToLower(target)
	switch {
	case strings.HasPrefix(lowerTarget, "page:"):
		slug := normalizePageRouteSlug(strings.TrimSpace(target[len("page:"):]))
		if slug != "" {
			return "page:" + slug
		}
	case strings.HasPrefix(lowerTarget, "/p/"):
		slug := normalizePageRouteSlug(strings.TrimSpace(target[len("/p/"):]))
		if slug != "" {
			return "page:" + slug
		}
	case strings.HasPrefix(lowerTarget, "table:"):
		tableName := strings.TrimSpace(strings.ToLower(target[len("table:"):]))
		if db.IsSafeIdentifier(tableName) {
			return "table:" + tableName
		}
	case strings.HasPrefix(lowerTarget, "/t/"):
		tableName := strings.TrimSpace(strings.ToLower(target[len("/t/"):]))
		if db.IsSafeIdentifier(tableName) {
			return "table:" + tableName
		}
	}
	if path := normalizeAuthenticatedRoutePath(target); path != "" {
		return path
	}
	return ""
}

func normalizeAuthenticatedRoutePath(input string) string {
	target := strings.TrimSpace(input)
	if target == "" || strings.HasPrefix(target, "//") || strings.HasPrefix(target, "/\\") {
		return ""
	}

	parsed, err := url.ParseRequestURI(target)
	if err != nil || parsed == nil || parsed.Host != "" || parsed.Scheme != "" {
		return ""
	}

	path := parsed.EscapedPath()
	if path == "" || path == "/" {
		return ""
	}

	lowerPath := strings.ToLower(path)
	if lowerPath == "/login" {
		return ""
	}

	normalized := path
	if parsed.RawQuery != "" {
		normalized += "?" + parsed.RawQuery
	}
	return normalized
}
