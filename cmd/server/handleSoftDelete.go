package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/realtime"
	"velm/internal/utils"
)

func handleSoftDeleteRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}

	tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(r.URL.Path, "/api/delete/")))
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("_id"))
	if !utils.IsValidUUID(id) {
		http.Error(w, "Invalid record ID", http.StatusBadRequest)
		return
	}

	quotedTable, err := db.QuoteIdentifier(tableName)
	if err != nil {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	realtimeClientID := strings.TrimSpace(r.FormValue("realtime_client_id"))

	if !tableHasColumn(tableName, "_deleted_at") {
		http.Error(w, "Table does not support soft delete", http.StatusBadRequest)
		return
	}

	oldSnapshot := db.GetRecord(tableName, id)
	oldData, _ := oldSnapshot.Data.(map[string]any)
	securityEvaluator, err := db.LoadTableSecurityEvaluator(r.Context(), tableName, userID)
	if err != nil {
		http.Error(w, "Failed to evaluate security rules", http.StatusInternalServerError)
		return
	}
	if securityEvaluator != nil && !securityEvaluator.AllowsRecord("D", oldData) {
		http.Error(w, "You do not have permission to delete this record", http.StatusForbidden)
		return
	}

	query := fmt.Sprintf(`UPDATE %s SET _deleted_at = NOW(), _deleted_by = NULLIF($1, '')::uuid, _updated_at = NOW() WHERE _id = $2`, quotedTable)
	ct, err := db.Pool.Exec(context.Background(), query, userID, id)
	if err != nil {
		http.Error(w, "Failed to delete record", http.StatusInternalServerError)
		return
	}
	if ct.RowsAffected() == 0 {
		http.Error(w, "Record not found", http.StatusNotFound)
		return
	}

	newSnapshot := db.GetRecord(tableName, id)
	newData, _ := newSnapshot.Data.(map[string]any)
	if err := db.CaptureDataChange(context.Background(), userID, tableName, id, "delete", oldData, newData); err != nil {
		log.Printf("failed to capture delete audit table=%s id=%s err=%v", tableName, id, err)
	}
	realtime.PublishRecordChange(tableName, id, "delete", userID, realtimeClientID)

	http.Redirect(w, r, "/t/"+tableName, http.StatusSeeOther)
}

func handleRestoreRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}

	tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(r.URL.Path, "/api/restore/")))
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("_id"))
	formName := normalizeRuntimeFormName(r.FormValue("form_name"))
	if !utils.IsValidUUID(id) {
		http.Error(w, "Invalid record ID", http.StatusBadRequest)
		return
	}

	quotedTable, err := db.QuoteIdentifier(tableName)
	if err != nil {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	realtimeClientID := strings.TrimSpace(r.FormValue("realtime_client_id"))

	if !tableHasColumn(tableName, "_deleted_at") {
		http.Error(w, "Table does not support restore", http.StatusBadRequest)
		return
	}

	oldSnapshot := db.GetRecord(tableName, id)
	oldData, _ := oldSnapshot.Data.(map[string]any)
	securityEvaluator, err := db.LoadTableSecurityEvaluator(r.Context(), tableName, userID)
	if err != nil {
		http.Error(w, "Failed to evaluate security rules", http.StatusInternalServerError)
		return
	}
	if securityEvaluator != nil && !securityEvaluator.AllowsRecord("U", oldData) {
		http.Error(w, "You do not have permission to restore this record", http.StatusForbidden)
		return
	}

	query := fmt.Sprintf(`UPDATE %s SET _deleted_at = NULL, _deleted_by = NULL, _updated_at = NOW() WHERE _id = $1`, quotedTable)
	ct, err := db.Pool.Exec(context.Background(), query, id)
	if err != nil {
		http.Error(w, "Failed to restore record", http.StatusInternalServerError)
		return
	}
	if ct.RowsAffected() == 0 {
		http.Error(w, "Record not found", http.StatusNotFound)
		return
	}

	newSnapshot := db.GetRecord(tableName, id)
	newData, _ := newSnapshot.Data.(map[string]any)
	if err := db.CaptureDataChange(context.Background(), userID, tableName, id, "restore", oldData, newData); err != nil {
		log.Printf("failed to capture restore audit table=%s id=%s err=%v", tableName, id, err)
	}
	realtime.PublishRecordChange(tableName, id, "restore", userID, realtimeClientID)

	http.Redirect(w, r, recordFormHref(tableName, id, formName), http.StatusSeeOther)
}

func tableHasColumn(tableName, columnName string) bool {
	var exists bool
	err := db.Pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = $1
			  AND column_name = $2
		)
	`, strings.TrimSpace(strings.ToLower(tableName)), strings.TrimSpace(strings.ToLower(columnName))).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}
