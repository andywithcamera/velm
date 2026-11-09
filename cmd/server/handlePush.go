package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/push"
)

// GET /api/push/config — public VAPID key so the client can subscribe.
func handlePushConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"enabled":   push.Enabled(),
		"publicKey": push.PublicKey(),
	}); err != nil {
		http.Error(w, "Encode failed", http.StatusInternalServerError)
	}
}

// POST /api/push/subscribe — store the browser's push subscription.
func handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Endpoint) == "" ||
		strings.TrimSpace(in.Keys.P256dh) == "" || strings.TrimSpace(in.Keys.Auth) == "" {
		http.Error(w, "Invalid subscription", http.StatusBadRequest)
		return
	}
	if err := db.UpsertPushSubscription(r.Context(), db.PushSubscriptionUpsert{
		UserID:    userID,
		Endpoint:  strings.TrimSpace(in.Endpoint),
		P256DH:    strings.TrimSpace(in.Keys.P256dh),
		Auth:      strings.TrimSpace(in.Keys.Auth),
		UserAgent: r.UserAgent(),
	}); err != nil {
		http.Error(w, "Failed to store subscription", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// POST /api/push/unsubscribe — remove an endpoint for the current user.
func handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if err := db.DeletePushSubscription(r.Context(), userID, strings.TrimSpace(in.Endpoint)); err != nil {
		http.Error(w, "Failed to remove subscription", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
