package main

import (
	"net/http"
	"strings"
	"unicode"

	"velm/internal/auth"
	"velm/internal/security"
)

type Breadcrumb struct {
	Label string
	Href  string
}

func newViewData(w http.ResponseWriter, r *http.Request, uri, title, section string) map[string]any {
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		appID = r.Header.Get("X-App-ID")
	}

	return map[string]any{
		"Uri":         uri,
		"Title":       title,
		"PageTitle":   title,
		"Section":     section,
		"AppID":       appID,
		"Menu":        menuItems,
		"Properties":  propertyItems,
		"User":        userDataFromRequest(r),
		"Theme":       activeUserTheme(r.Context(), auth.UserIDFromRequest(r)),
		"CSRFToken":   ensureCSRFToken(w, r),
		"RequestID":   security.RequestIDFromContext(r.Context()),
		"CSPNonce":    security.NonceFromContext(r.Context()),
		"Breadcrumbs": buildBreadcrumbs(uri, title),
	}
}

func buildBreadcrumbs(uri, pageTitle string) []Breadcrumb {
	breadcrumbs := []Breadcrumb{{Label: "Home", Href: "/"}}
	trimmed := strings.Trim(uri, "/")
	if trimmed == "" {
		return breadcrumbs
	}

	parts := strings.Split(trimmed, "/")
	current := ""
	for i, part := range parts {
		current += "/" + part
		label := strings.ReplaceAll(part, "_", " ")
		label = titleizeBreadcrumbLabel(label)
		if i == len(parts)-1 && pageTitle != "" {
			label = pageTitle
		}
		breadcrumbs = append(breadcrumbs, Breadcrumb{
			Label: label,
			Href:  current,
		})
	}
	return breadcrumbs
}

// titleizeBreadcrumbLabel upper-cases the first letter of each word, leaving
// the rest of every word untouched.
//
// This replaces strings.Title, which is deprecated because it treats any
// non-letter as a word boundary: "what's_new" became "What'S New". A breadcrumb
// is a human-facing label built from a URL slug, so only the first rune of each
// space-separated word should change.
func titleizeBreadcrumbLabel(label string) string {
	words := strings.Fields(label)
	for i, word := range words {
		runes := []rune(word)
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}
