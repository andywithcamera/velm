package main

import (
	"net/http"

	"velm/internal/security"
)

// handleMenu assembles the menu object and renders it.
func handleMenu(w http.ResponseWriter, r *http.Request) {
	// This handler returns the rendered partial for the menu
	data := map[string]any{
		"Menu":     menuItems,
		"CSPNonce": security.NonceFromContext(r.Context()),
	}

	err := templates.ExecuteTemplate(w, "menu.html", data)
	if err != nil {
		http.Error(w, "Failed to load menu", http.StatusInternalServerError)
	}
}
