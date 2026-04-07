package main

import (
	"net/http"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/realtime"
)

func handleAddRecordComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	tableName := strings.TrimSpace(strings.ToLower(r.FormValue("table_name")))
	recordID := strings.TrimSpace(r.FormValue("record_id"))
	body := strings.TrimSpace(r.FormValue("body"))

	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if recordID == "" || recordID == "new" || !db.IsValidRecordID(tableName, recordID) {
		http.Error(w, "Invalid record ID", http.StatusBadRequest)
		return
	}
	if db.GetTable(tableName).ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	if body == "" {
		http.Error(w, "Comment body is required", http.StatusBadRequest)
		return
	}
	if len(body) > 4000 {
		http.Error(w, "Comment body exceeds 4000 characters", http.StatusBadRequest)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	record := db.GetRecord(tableName, recordID)
	if record.View == nil || record.Data == nil {
		http.Error(w, "Record not found", http.StatusNotFound)
		return
	}
	recordData, _ := record.Data.(map[string]any)
	securityEvaluator, err := db.LoadTableSecurityEvaluator(r.Context(), tableName, userID)
	if err != nil {
		http.Error(w, "Failed to evaluate security rules", http.StatusInternalServerError)
		return
	}
	if securityEvaluator != nil && !securityEvaluator.AllowsRecord("R", recordData) {
		http.Error(w, "You do not have permission to access this record", http.StatusForbidden)
		return
	}
	realtimeClientID := strings.TrimSpace(r.FormValue("realtime_client_id"))

	if err := db.AddRecordComment(r.Context(), tableName, recordID, userID, body); err != nil {
		http.Error(w, "Failed to save comment", http.StatusInternalServerError)
		return
	}
	realtime.PublishRecordChange(tableName, recordID, "comment", userID, realtimeClientID)

	http.Redirect(w, r, "/f/"+tableName+"/"+recordID+"#recordTimeline", http.StatusSeeOther)
}
