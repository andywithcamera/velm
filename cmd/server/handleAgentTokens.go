package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"velm/internal/auth"
	"velm/internal/db"
)

// persistAgentToken is indirected for tests.
var persistAgentToken = func(r *http.Request, userID, tokenHash, label string) error {
	return db.NewAgentTokenStore().CreateAgentToken(r.Context(), userID, tokenHash, label)
}

// handleAgentTokensCreate mints an agent API token for the authenticated
// admin's own identity (issue #240 phase 1). The raw secret is returned once
// in the response; only its SHA-256 hash is persisted.
func handleAgentTokensCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil && err.Error() != "EOF" {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "no authenticated identity", http.StatusUnauthorized)
		return
	}
	mintAgentToken(w, r, userID, body.Label)
}

func mintAgentToken(w http.ResponseWriter, r *http.Request, userID, label string) {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "unnamed agent token"
	}
	if len(label) > 120 {
		label = label[:120]
	}

	raw, err := auth.NewAgentToken()
	if err != nil {
		log.Printf("agent token: generate: %v", err)
		http.Error(w, "token generation failed", http.StatusInternalServerError)
		return
	}
	if err := persistAgentToken(r, userID, auth.HashAgentToken(raw), label); err != nil {
		log.Printf("agent token: persist: %v", err)
		http.Error(w, "token persistence failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":      raw,
		"label":      label,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"note":       "store this now — the raw secret is not retrievable later",
	})
}
