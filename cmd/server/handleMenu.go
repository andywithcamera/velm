package main

import (
	"net/http"
)

// handleMenu assembles the menu object and renders it.
func handleMenu(w http.ResponseWriter, r *http.Request) {
	// This handler returns the rendered partial for the menu
	data := map[string]any{
		"Menu": menuItems,
	}

	err := templates.ExecuteTemplate(w, "menu.html", data)
	if err != nil {
		http.Error(w, "Failed to load menu", http.StatusInternalServerError)
	}
}
