package main

import (
	"net/http"
	"net/url"
	"strings"
)

func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	data := newViewData(w, r, "/login", "Login", "Auth")
	data["View"] = "login"
	data["LoginNext"] = sanitizeLoginNext(strings.TrimSpace(r.URL.Query().Get("next")))

	// Otherwise, render the full page with the layout
	var err = templates.ExecuteTemplate(w, "layout.html", data) // Render the layout with page content
	if err != nil {
		http.Error(w, "Error rendering page", http.StatusInternalServerError)
	}
}

func sanitizeLoginNext(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}

	// Normalize backslashes because some clients treat "\" as "/".
	raw = strings.ReplaceAll(raw, "\\", "/")

	u, err := url.Parse(raw)
	if err != nil {
		return "/"
	}

	// Allow only local, relative redirects.
	if u.Hostname() != "" || u.Scheme != "" {
		return "/"
	}
	if !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}

	safe := u.String()
	if safe == "/login" || strings.HasPrefix(safe, "/login?") {
		return "/"
	}
	return safe
}
