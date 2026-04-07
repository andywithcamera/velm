package main

import (
	"log"
	"net/http"
	"velm/internal/auth"
)

func ensureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	token, err := auth.EnsureCSRFToken(w, r, store)
	if err != nil {
		log.Printf("failed to ensure CSRF token: %v", err)
		return ""
	}
	return token
}
