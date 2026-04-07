package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"velm/internal/auth"
	"velm/internal/db"
	"strconv"
	"strings"
)

func handleListViewSavedViews(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleListSavedViews(w, r, userID)
	case http.MethodPost:
		handleSaveSavedView(w, r, userID)
	case http.MethodDelete:
		handleDeleteSavedView(w, r, userID)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleListSavedViews(w http.ResponseWriter, r *http.Request, userID string) {
	tableName := strings.TrimSpace(r.URL.Query().Get("table"))
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}

	appID := strings.TrimSpace(auth.AppIDFromRequest(r))
	views, err := db.ListSavedViews(context.Background(), userID, appID, tableName)
	if err != nil {
		http.Error(w, "Failed to load saved views", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"views": views})
}

func handleSaveSavedView(w http.ResponseWriter, r *http.Request, userID string) {
	var payload struct {
		Table      string          `json:"table"`
		Name       string          `json:"name"`
		Visibility string          `json:"visibility"`
		State      json.RawMessage `json:"state"`
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

	tableName := strings.TrimSpace(payload.Table)
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(payload.Name)
	if name == "" || len(name) > 80 {
		http.Error(w, "Invalid view name", http.StatusBadRequest)
		return
	}

	visibility := strings.TrimSpace(payload.Visibility)
	if visibility != "private" && visibility != "app" {
		visibility = "private"
	}
	if len(payload.State) == 0 || !json.Valid(payload.State) {
		http.Error(w, "Invalid state payload", http.StatusBadRequest)
		return
	}

	appID := strings.TrimSpace(auth.AppIDFromRequest(r))
	if err := db.UpsertSavedView(context.Background(), appID, tableName, name, visibility, userID, payload.State); err != nil {
		http.Error(w, "Failed to save view", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleDeleteSavedView(w http.ResponseWriter, r *http.Request, userID string) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid saved view id", http.StatusBadRequest)
		return
	}

	if err := db.DeleteSavedView(context.Background(), id, userID); err != nil {
		http.Error(w, "Failed to delete view", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
