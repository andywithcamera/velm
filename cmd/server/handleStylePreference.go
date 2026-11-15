package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"velm/internal/auth"
	"velm/internal/db"
)

// handleStylePreference reads (GET) or writes (POST) the user's active style
// pack, persisted in _user_preference. Modelled on handleThemePreference; the
// two axes are stored independently so changing one never resets the other.
func handleStylePreference(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		payload, err := json.Marshal(map[string]string{"style": activeUserStyle(r.Context(), userID)})
		if err != nil {
			http.Error(w, "Failed to encode style", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	case http.MethodPost:
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
		value, err := packStylePreference(payload.Value)
		if err != nil {
			http.Error(w, "Failed to encode style", http.StatusInternalServerError)
			return
		}
		if err := db.UpsertUserPreference(context.Background(), userID, themePreferenceNamespace, stylePreferenceKey, value); err != nil {
			http.Error(w, "Failed to save style", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
