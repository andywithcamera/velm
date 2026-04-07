package main

import (
	"net/http"
	"strings"

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
		"CSRFToken":   ensureCSRFToken(w, r),
		"RequestID":   security.RequestIDFromContext(r.Context()),
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
		label = strings.Title(label)
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
