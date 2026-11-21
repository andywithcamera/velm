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

// unauthenticatedRootRoutePropertyKey is the landing target for a visitor with
// no session: "login" (the default), "page:<slug>" for a public page, or
// "table:<name>". It is what makes a page usable as the site homepage without
// giving the anonymous visitor a session.
const unauthenticatedRootRoutePropertyKey = "unauthenticated_root_route_target"

// legacyRootRoutePropertyKey is the original name of the unauthenticated
// property. It is read only as a fallback, so an install that set it before
// #372 keeps its landing behaviour without a migration.
const legacyRootRoutePropertyKey = "root_route_target"

func handleRootRoute(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	target := resolveUnauthenticatedRootRouteTarget(r.Context())
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

// resolveUnauthenticatedRootRouteTarget answers "where does a visitor with no
// session land?". Order: the explicit unauthenticated property, then the legacy
// property name, then the login page. An empty value at either level means
// "keep looking": clearing the new property restores the old behaviour instead
// of stranding the visitor on a blank page.
func resolveUnauthenticatedRootRouteTarget(ctx context.Context) string {
	for _, key := range []string{unauthenticatedRootRoutePropertyKey, legacyRootRoutePropertyKey} {
		if target := resolveRootRouteProperty(ctx, key); target != "" {
			return target
		}
	}
	return "login"
}

// setProperty writes one runtime property.
//
// It is an UPDATE followed by an INSERT rather than INSERT ... ON CONFLICT,
// because _property has no unique constraint on key: the ON CONFLICT form the
// landing-target endpoint used before #372 could never succeed, which is why
// saving a landing target always returned 500. A property is one row by
// convention, so updating every row with that key keeps the table consistent
// even if an install already holds a duplicate.
func setProperty(ctx context.Context, key, value string) error {
	tag, err := db.Pool.Exec(ctx, `UPDATE _property SET value = $2 WHERE key = $1`, key, value)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO _property (key, value) VALUES ($1, $2)`, key, value)
	return err
}

// resolveRootRouteProperty reads one landing-target property, preferring the
// live row over the boot-time snapshot. It returns "" when the property is
// unset or empty, and never an error: a landing target is not worth a 500.
func resolveRootRouteProperty(ctx context.Context, key string) string {
	// A nil pool is the boot-time case (and the unit-test case): fall through
	// to the snapshot instead of panicking on a query nothing has answered yet.
	if db.Pool != nil {
		var value string
		if err := db.Pool.QueryRow(ctx, `SELECT value FROM _property WHERE key = $1 LIMIT 1`, key).Scan(&value); err == nil {
			if target := normalizeUnauthenticatedRouteTarget(value); target != "" {
				return target
			}
		}
	}
	if fallback, ok := propertyItems[key]; ok {
		if s, ok := fallback.(string); ok {
			if target := normalizeUnauthenticatedRouteTarget(s); target != "" {
				return target
			}
		}
	}
	return ""
}

// normalizeUnauthenticatedRouteTarget lowercases and validates a landing target
// for an anonymous visitor. It accepts "login", page targets and table targets;
// anything else is refused, which is why a stray path cannot turn the root
// route into an open redirect.
func normalizeUnauthenticatedRouteTarget(value string) string {
	target := strings.TrimSpace(strings.ToLower(value))
	switch {
	case target == "":
		return ""
	case target == "login" || target == "/login" || target == "/login/":
		return "login"
	case strings.HasPrefix(target, "page:"), strings.HasPrefix(target, "/p/"):
		return target
	case strings.HasPrefix(target, "table:"), strings.HasPrefix(target, "/t/"):
		return target
	}
	return ""
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
