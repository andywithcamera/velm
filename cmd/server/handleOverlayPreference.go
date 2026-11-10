package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"velm/internal/auth"
	"velm/internal/db"
)

// overlayPreferenceKey stores the ambient overlay selection ("none" or an
// effect id) alongside the colour theme in _user_preference.
const overlayPreferenceKey = "overlay"

// validOverlays is the set of overlay effect identifiers. "none" disables the
// layer; unknown values fall back to it.
var validOverlays = map[string]bool{
	"none":      true,
	"matrix":    true,
	"nature":    true,
	"rainfall":  true,
	"fireflies": true,
	"aurora":    true,
}

func normalizeOverlay(overlay string) string {
	if validOverlays[overlay] {
		return overlay
	}
	return "none"
}

// activeUserOverlay resolves the persisted overlay for a user, defaulting to
// "none" so overlays stay strictly opt-in.
func activeUserOverlay(ctx context.Context, userID string) string {
	if userID == "" {
		return "none"
	}
	raw, err := db.GetUserPreference(ctx, userID, themePreferenceNamespace, overlayPreferenceKey)
	if err != nil || raw == nil {
		return "none"
	}
	trimmed := string(raw)
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	return normalizeOverlay(trimmed)
}

// handleOverlayPreference reads (GET) or writes (POST) the user's ambient
// overlay preference. Modeled on the theme preference endpoint.
func handleOverlayPreference(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		payload, err := json.Marshal(map[string]string{"overlay": activeUserOverlay(r.Context(), userID)})
		if err != nil {
			http.Error(w, "Failed to encode overlay", http.StatusInternalServerError)
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
		value, err := json.Marshal(normalizeOverlay(payload.Value))
		if err != nil {
			http.Error(w, "Failed to encode overlay", http.StatusInternalServerError)
			return
		}
		if err := db.UpsertUserPreference(context.Background(), userID, themePreferenceNamespace, overlayPreferenceKey, value); err != nil {
			http.Error(w, "Failed to save overlay", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
