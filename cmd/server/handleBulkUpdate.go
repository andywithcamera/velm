package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/realtime"
	"velm/internal/utils"
)

func handleBulkUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(r.URL.Path, "/api/bulk/update/")))
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}

	view := db.GetView(tableName)
	if view.Table == nil || view.Table.ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	var payload struct {
		IDs    []string       `json:"ids"`
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if len(payload.IDs) == 0 {
		http.Error(w, "ids are required", http.StatusBadRequest)
		return
	}
	if len(payload.IDs) > 500 {
		http.Error(w, "too many ids (max 500)", http.StatusBadRequest)
		return
	}
	if len(payload.Fields) == 0 {
		http.Error(w, "fields are required", http.StatusBadRequest)
		return
	}

	allowedColumns := map[string]bool{}
	editableColumns := map[string]bool{}
	columnType := map[string]string{}
	columnMeta := map[string]db.Column{}
	for _, col := range view.Columns {
		allowedColumns[col.NAME] = true
		columnType[col.NAME] = strings.ToLower(strings.TrimSpace(col.DATA_TYPE))
		columnMeta[col.NAME] = col
		if !strings.HasPrefix(col.NAME, "_") {
			editableColumns[col.NAME] = true
		}
	}

	formData := map[string]string{}
	nullColumns := map[string]bool{}
	for fieldName, raw := range payload.Fields {
		fieldName = strings.TrimSpace(fieldName)
		if !db.IsSafeIdentifier(fieldName) || !editableColumns[fieldName] {
			http.Error(w, "Invalid field in request", http.StatusBadRequest)
			return
		}

		stringValue, err := bulkFieldToString(raw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		normalized, isNull, err := db.NormalizeSubmittedColumnValue(columnMeta[fieldName], strings.TrimSpace(stringValue))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		formData[fieldName] = normalized
		if isNull {
			nullColumns[fieldName] = true
		}
	}
	if taskTypeValue := db.TaskTypeValueForTable(r.Context(), tableName); taskTypeValue != "" && allowedColumns["work_type"] {
		formData["work_type"] = taskTypeValue
		delete(nullColumns, "work_type")
	}

	if err := db.ValidateFormWrite(view.Columns, formData, false); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	securityEvaluator, err := db.LoadTableSecurityEvaluator(r.Context(), tableName, userID)
	if err != nil {
		http.Error(w, "Failed to evaluate security rules", http.StatusInternalServerError)
		return
	}

	if allowedColumns["_updated_by"] {
		formData["_updated_by"] = userID
	}
	if allowedColumns["_updated_at"] {
		formData["_updated_at"] = time.Now().Format(time.RFC3339)
	}

	columnNames := make([]string, 0, len(formData))
	for k := range formData {
		columnNames = append(columnNames, k)
	}
	sort.Strings(columnNames)

	setParts := make([]string, 0, len(columnNames))
	valuesTemplate := make([]any, 0, allocHintSum(len(columnNames), 1))
	for i, col := range columnNames {
		quotedCol, err := db.QuoteIdentifier(col)
		if err != nil {
			http.Error(w, "Invalid field in request", http.StatusBadRequest)
			return
		}
		setParts = append(setParts, fmt.Sprintf("%s = $%d", quotedCol, i+1))
		if nullColumns[col] {
			valuesTemplate = append(valuesTemplate, nil)
		} else {
			valuesTemplate = append(valuesTemplate, formData[col])
		}
	}

	quotedTable, err := db.QuoteIdentifier(tableName)
	if err != nil {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}

	query := fmt.Sprintf("UPDATE %s SET %s WHERE _id = $%d", quotedTable, strings.Join(setParts, ", "), len(valuesTemplate)+1)

	updated := 0
	skipped := 0
	denied := 0
	for _, id := range payload.IDs {
		id = strings.TrimSpace(id)
		if !utils.IsValidUUID(id) {
			skipped++
			continue
		}

		oldSnapshot := loadSnapshotSafe(tableName, id)
		if securityEvaluator != nil {
			proposedRecord := db.MergeRecordSnapshot(oldSnapshot, formData, nullColumns)
			changedFields := db.ChangedRecordFields(view.Columns, oldSnapshot, formData, nullColumns)
			if !securityEvaluator.AllowsFields("U", changedFields, proposedRecord) {
				denied++
				continue
			}
		}
		values := append([]any{}, valuesTemplate...)
		values = append(values, id)
		ct, err := db.Pool.Exec(context.Background(), query, values...)
		if err != nil {
			http.Error(w, "Failed to apply bulk update", http.StatusInternalServerError)
			return
		}
		if ct.RowsAffected() == 0 {
			skipped++
			continue
		}

		updated++
		newSnapshot := loadSnapshotSafe(tableName, id)
		_ = db.CaptureDataChange(context.Background(), userID, tableName, id, "bulk_update", oldSnapshot, newSnapshot)
		if err := db.EmitTaskNotifications(r.Context(), tableName, id, "bulk_update", userID, oldSnapshot, newSnapshot); err != nil {
			log.Printf("failed to emit task notifications table=%s id=%s err=%v", tableName, id, err)
		}
		realtime.PublishRecordChange(tableName, id, "bulk_update", userID, "")
	}

	if updated == 0 && denied > 0 {
		http.Error(w, "You do not have permission to update this record", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"requested": len(payload.IDs),
		"updated":   updated,
		"skipped":   skipped,
		"denied":    denied,
	})
}

func bulkFieldToString(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case float64, float32, int, int64, int32, uint, uint64, uint32, bool:
		return fmt.Sprint(v), nil
	case map[string]any, []any:
		raw, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("invalid field value")
		}
		return string(raw), nil
	default:
		return "", fmt.Errorf("unsupported field value type")
	}
}

func loadSnapshotSafe(tableName, id string) map[string]any {
	row := db.GetRecord(tableName, id)
	if row.Data == nil {
		return map[string]any{}
	}
	data, ok := row.Data.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return data
}
