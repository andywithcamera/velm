package main

import (
	"net/http"
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
	if raw == "" {
		return "/"
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return "/"
	}
	if raw == "/login" || strings.HasPrefix(raw, "/login?") {
		return "/"
	}
	return raw
}
