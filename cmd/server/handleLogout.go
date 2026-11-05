package main

import (
	"log"
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

	// The cookie is already being cleared locally and the response redirects to
	// /login either way, so a failed save is not worth failing the request over.
	// It is worth a line in the log: it means the cookie on the client is stale.
	if err := session.Save(r, w); err != nil {
		log.Printf("logout: save cleared session: %v", err)
	}

	// If HTMX, do an HX-Redirect
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", "/login")
	} else {
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}
