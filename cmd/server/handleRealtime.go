package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/realtime"
)

func handleRealtimeStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tableName := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table")))
	recordID := strings.TrimSpace(r.URL.Query().Get("record_id"))
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if !requireAdminTableAccess(w, r, tableName) {
		return
	}
	if db.GetTable(tableName).ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	if recordID != "" && !db.IsValidRecordID(tableName, recordID) {
		http.Error(w, "Invalid record ID", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	events, unsubscribe := realtime.Subscribe(tableName, recordID)
	defer unsubscribe()

	if recordID != "" {
		if err := writeRealtimeEvent(w, realtime.Event{
			Type:     realtime.EventTypePresenceUpdate,
			Table:    tableName,
			RecordID: recordID,
			At:       time.Now().UTC().Format(time.RFC3339Nano),
			Presence: realtime.PresenceSnapshot(tableName, recordID),
		}); err != nil {
			return
		}
		flusher.Flush()
	}

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeRealtimeEvent(w, event); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func handleRealtimePresenceHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	tableName := strings.TrimSpace(strings.ToLower(r.FormValue("table_name")))
	recordID := strings.TrimSpace(r.FormValue("record_id"))
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	status := strings.TrimSpace(r.FormValue("status"))
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if !requireAdminTableAccess(w, r, tableName) {
		return
	}
	if db.GetTable(tableName).ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	if recordID == "" || recordID == "new" || !db.IsValidRecordID(tableName, recordID) {
		http.Error(w, "Invalid record ID", http.StatusBadRequest)
		return
	}
	if clientID == "" {
		http.Error(w, "Missing client ID", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	realtime.UpsertPresence(
		tableName,
		recordID,
		clientID,
		userID,
		strings.TrimSpace(auth.UserNameFromRequest(r)),
		status,
	)
	w.WriteHeader(http.StatusNoContent)
}

func handleRealtimeRecordVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tableName := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table")))
	recordID := strings.TrimSpace(r.URL.Query().Get("record_id"))
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if !requireAdminTableAccess(w, r, tableName) {
		return
	}
	if db.GetTable(tableName).ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	if recordID == "" || recordID == "new" || !db.IsValidRecordID(tableName, recordID) {
		http.Error(w, "Invalid record ID", http.StatusBadRequest)
		return
	}

	version, found, err := db.GetRecordVersion(r.Context(), tableName, recordID)
	if err != nil {
		http.Error(w, "Failed to load record version", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "Record not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"version":    version,
		"updated_at": version,
	})
}

func writeRealtimeEvent(w http.ResponseWriter, event realtime.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", strings.TrimSpace(event.Type)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	return nil
}
