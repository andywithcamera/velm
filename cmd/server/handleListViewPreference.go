package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"velm/internal/auth"
	"velm/internal/db"
	"strings"
)

const listViewPreferenceNamespace = "list_view"

func handleListViewPreference(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleGetListViewPreference(w, r, userID)
	case http.MethodPost:
		handleSaveListViewPreference(w, r, userID)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleGetListViewPreference(w http.ResponseWriter, r *http.Request, userID string) {
	tableName := strings.TrimSpace(r.URL.Query().Get("table"))
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}

	key := listViewPreferenceKey(r, tableName)
	raw, err := db.GetUserPreference(context.Background(), userID, listViewPreferenceNamespace, key)
	if err != nil {
		http.Error(w, "Failed to load preferences", http.StatusInternalServerError)
		return
	}
	if raw == nil {
		raw = []byte("{}")
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func handleSaveListViewPreference(w http.ResponseWriter, r *http.Request, userID string) {
	var payload struct {
		Table string          `json:"table"`
		State json.RawMessage `json:"state"`
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
	if len(payload.State) == 0 || !json.Valid(payload.State) {
		http.Error(w, "Invalid state payload", http.StatusBadRequest)
		return
	}

	key := listViewPreferenceKey(r, tableName)
	if err := db.UpsertUserPreference(context.Background(), userID, listViewPreferenceNamespace, key, payload.State); err != nil {
		http.Error(w, "Failed to save preferences", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func listViewPreferenceKey(r *http.Request, tableName string) string {
	appID := strings.TrimSpace(auth.AppIDFromRequest(r))
	if appID == "" {
		appID = "global"
	}
	return appID + ":" + tableName
}
