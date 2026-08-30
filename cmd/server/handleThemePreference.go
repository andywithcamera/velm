package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"velm/internal/auth"
	"velm/internal/db"
)

// handleThemePreference reads (GET) or writes (POST) the user's active theme,
// persisted in the _user_preference table (R4). Modeled on the existing
// list-view preference endpoint.
func handleThemePreference(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleGetThemePreference(w, r, userID)
	case http.MethodPost:
		handleSaveThemePreference(w, r, userID)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleGetThemePreference(w http.ResponseWriter, r *http.Request, userID string) {
	theme := activeUserTheme(r.Context(), userID)
	payload, err := json.Marshal(map[string]string{"theme": theme})
	if err != nil {
		http.Error(w, "Failed to encode theme", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

func handleSaveThemePreference(w http.ResponseWriter, r *http.Request, userID string) {
	var payload struct {
		Value string `json:"value"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Normalize (validates against the registry; unknown falls back to default).
	theme := normalizeTheme(payload.Value)
	value, err := packThemePreference(theme)
	if err != nil {
		http.Error(w, "Failed to encode theme", http.StatusInternalServerError)
		return
	}
	if err := db.UpsertUserPreference(context.Background(), userID, themePreferenceNamespace, themePreferenceKey, value); err != nil {
		http.Error(w, "Failed to save theme", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
