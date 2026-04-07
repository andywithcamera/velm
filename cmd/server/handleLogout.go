package main

import (
	"net/http"
)

func handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session, _ := store.Get(r, "mysession")

	// Completely destroy the session
	session.Options.MaxAge = -1

	// Optional: wipe values if you're paranoid
	for k := range session.Values {
		delete(session.Values, k)
	}

	session.Save(r, w)

	// If HTMX, do an HX-Redirect
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", "/login")
	} else {
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}
