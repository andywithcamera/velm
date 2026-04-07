package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"velm/internal/realtime"
	"velm/internal/utils"
)

func HandleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}

	tableName := strings.TrimPrefix(r.URL.Path, "/api/save/")
	if !IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if IsImmutableTableName(tableName) {
		http.Error(w, "Table is read-only", http.StatusForbidden)
		return
	}
	quotedTableName, err := QuoteIdentifier(tableName)
	if err != nil {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}

	view := GetView(tableName)
	if view.Table == nil || view.Table.ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	err = r.ParseForm()
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	allowedColumns := map[string]bool{}
	editableColumns := map[string]bool{}
	columnMeta := map[string]Column{}
	for _, col := range view.Columns {
		allowedColumns[col.NAME] = true
		columnMeta[col.NAME] = col
		if !strings.HasPrefix(col.NAME, "_") {
			editableColumns[col.NAME] = true
		}
	}

	formData := map[string]string{}
	nullColumns := map[string]bool{}
	var id string
	expectedVersion := strings.TrimSpace(r.FormValue("expected_version"))
	expectedUpdatedAt := strings.TrimSpace(r.FormValue("expected_updated_at"))
	formName := normalizeRuntimeFormName(r.FormValue("form_name"))
	if expectedVersion == "" {
		expectedVersion = expectedUpdatedAt
	}
	for k, v := range r.PostForm {
		if len(v) == 0 {
			continue
		}
		if k == "_id" {
			id = v[0]
			continue
		}
		if isSaveTransportField(k) {
			continue
		}
		if strings.HasPrefix(k, "_") {
			continue
		}
		if !IsSafeIdentifier(k) || !editableColumns[k] {
			http.Error(w, "Invalid field in request", http.StatusBadRequest)
			return
		}
		val := strings.TrimSpace(v[len(v)-1])
		normalized, isNull, err := NormalizeSubmittedColumnValue(columnMeta[k], val)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		formData[k] = normalized
		if isNull {
			nullColumns[k] = true
		}
	}
	if taskTypeValue := TaskTypeValueForTable(r.Context(), tableName); taskTypeValue != "" && allowedColumns["work_type"] {
		formData["work_type"] = taskTypeValue
		delete(nullColumns, "work_type")
	}

	if id != "" && id != "new" && !IsValidRecordID(tableName, id) {
		http.Error(w, "Invalid record ID", http.StatusBadRequest)
		return
	}
	oldSnapshot := map[string]any{}
	if id != "new" && id != "" {
		oldSnapshot = loadRecordSnapshot(tableName, id)
		if !submittedFormHasChanges(view.Columns, formData, nullColumns, oldSnapshot) {
			if r.Header.Get("HX-Request") != "" {
				w.Header().Set("X-Velm-Form-Message", "No changes to save")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Redirect(w, r, recordFormRedirectTarget(tableName, id, formName), http.StatusSeeOther)
			return
		}
	}
	if err := ValidateMetadataWrite(context.Background(), tableName, id, formData); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := ValidateFormWrite(view.Columns, formData, id == "" || id == "new"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Get the current user from the session or request headers
	updatedBy := utils.GetLoggedinUserid(r)
	if updatedBy == "" {
		http.Error(w, "Unable to identify the user making the update", http.StatusForbidden)
		return
	}
	realtimeClientID := strings.TrimSpace(r.FormValue("realtime_client_id"))
	if allowedColumns["_updated_by"] {
		formData["_updated_by"] = updatedBy
	}

	updatedAt := time.Now()
	if allowedColumns["_updated_at"] {
		formData["_updated_at"] = updatedAt.Format(time.RFC3339Nano)
	}

	if id == "new" || id == "" {
		if allowedColumns["_created_by"] {
			formData["_created_by"] = updatedBy
		}

		createdAt := time.Now()
		if allowedColumns["_created_at"] {
			formData["_created_at"] = createdAt.Format(time.RFC3339Nano)
		}
	}

	isCreate := id == "new" || id == ""
	tx, err := Pool.Begin(context.Background())
	if err != nil {
		http.Error(w, "Failed to save record", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if isCreate && allowedColumns["_id"] {
		allocatedID, err := allocateRecordIDWithQuerier(context.Background(), tx)
		if err != nil {
			http.Error(w, "Failed to save record", http.StatusInternalServerError)
			return
		}
		id = allocatedID
		formData["_id"] = allocatedID
		delete(nullColumns, "_id")
	}

	triggerRecord, err := buildRuntimeTriggerRecord(view, oldSnapshot, formData, nullColumns)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	triggerEvent := "record.update"
	if isCreate {
		triggerEvent = "record.insert"
	}
	triggerRecord, err = RunAppBeforeWriteTriggersWithQuerier(context.Background(), tx, tableName, triggerEvent, updatedBy, "", triggerRecord, oldSnapshot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := applyRuntimeTriggerRecordToFormData(view, triggerRecord, oldSnapshot, isCreate, formData, nullColumns); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filterSubmittedColumns(view.Columns, formData, nullColumns)
	if taskTypeValue := TaskTypeValueForTable(r.Context(), tableName); taskTypeValue != "" && allowedColumns["work_type"] {
		formData["work_type"] = taskTypeValue
		delete(nullColumns, "work_type")
	}
	validationRecordID := id
	if isCreate {
		validationRecordID = ""
	}
	if err := ValidateMetadataWrite(context.Background(), tableName, validationRecordID, formData); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := ValidateFormWrite(view.Columns, formData, isCreate); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	securityEvaluator, err := LoadTableSecurityEvaluator(r.Context(), tableName, updatedBy)
	if err != nil {
		http.Error(w, "Failed to evaluate security rules", http.StatusInternalServerError)
		return
	}
	if securityEvaluator != nil {
		securityOperation := "U"
		permissionMessage := "You do not have permission to update this record"
		if isCreate {
			securityOperation = "C"
			permissionMessage = "You do not have permission to create this record"
		}
		proposedRecord := MergeRecordSnapshot(oldSnapshot, formData, nullColumns)
		changedFields := ChangedRecordFields(view.Columns, oldSnapshot, formData, nullColumns)
		if !securityEvaluator.AllowsFields(securityOperation, changedFields, proposedRecord) {
			http.Error(w, permissionMessage, http.StatusForbidden)
			return
		}
	}

	var operation string
	if isCreate {
		operation = "insert"
		insertColumns, values, placeholders, err := buildInsertColumnsAndValues(columnMeta, formData, nullColumns)
		if err != nil {
			http.Error(w, "Invalid field in request", http.StatusBadRequest)
			return
		}
		query := fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s) RETURNING _id::text",
			quotedTableName,
			strings.Join(insertColumns, ", "),
			strings.Join(placeholders, ", "),
		)
		if err := tx.QueryRow(context.Background(), query, values...).Scan(&id); err != nil {
			log.Printf("failed to insert record table=%s err=%v", tableName, err)
			writeSaveDatabaseError(w, err)
			return
		}
	} else {
		operation = "update"
		columnNames := make([]string, 0, len(formData))
		for k := range formData {
			columnNames = append(columnNames, k)
		}
		sort.Strings(columnNames)

		columns := make([]string, 0, len(formData))
		values := make([]any, 0, len(formData))
		for _, columnName := range columnNames {
			quotedColumn, err := QuoteIdentifier(columnName)
			if err != nil {
				http.Error(w, "Invalid field in request", http.StatusBadRequest)
				return
			}
			columns = append(columns, quotedColumn)
			if nullColumns[columnName] {
				values = append(values, nil)
			} else {
				values = append(values, formData[columnName])
			}
		}

		recordIDValue, err := ParseRecordIDValue(tableName, id)
		if err != nil {
			http.Error(w, "Invalid record ID", http.StatusBadRequest)
			return
		}
		setParts := make([]string, len(columns))
		for j, col := range columns {
			if column, ok := columnMeta[columnNames[j]]; ok {
				setParts[j] = fmt.Sprintf("%s = $%d%s", col, j+1, scriptColumnCast(column.DATA_TYPE))
			} else {
				setParts[j] = fmt.Sprintf("%s = $%d", col, j+1)
			}
		}
		whereParts := []string{fmt.Sprintf("_id = $%d", len(values)+1)}
		values = append(values, recordIDValue)
		if expectedVersion != "" && allowedColumns["_update_count"] {
			expectedCount, err := strconv.ParseInt(expectedVersion, 10, 64)
			if err != nil || expectedCount < 0 {
				http.Error(w, "Invalid record version", http.StatusBadRequest)
				return
			}
			whereParts = append(whereParts, fmt.Sprintf("_update_count = $%d", len(values)+1))
			values = append(values, expectedCount)
		} else if expectedUpdatedAt != "" && allowedColumns["_updated_at"] {
			whereParts = append(whereParts, fmt.Sprintf("_updated_at = $%d", len(values)+1))
			values = append(values, expectedUpdatedAt)
		}
		query := fmt.Sprintf(
			"UPDATE %s SET %s WHERE %s",
			quotedTableName,
			strings.Join(setParts, ", "),
			strings.Join(whereParts, " AND "),
		)
		commandTag, err := tx.Exec(context.Background(), query, values...)
		if err != nil {
			log.Printf("failed to update record table=%s id=%s err=%v", tableName, id, err)
			writeSaveDatabaseError(w, err)
			return
		}
		if commandTag.RowsAffected() == 0 {
			if (expectedVersion != "" && allowedColumns["_update_count"]) || (expectedUpdatedAt != "" && allowedColumns["_updated_at"]) {
				http.Error(w, "Record changed by another user; refresh and try again", http.StatusConflict)
				return
			}
			http.Error(w, "Record not found", http.StatusNotFound)
			return
		}
	}

	newSnapshot := loadRecordSnapshotWithQuerier(context.Background(), tx, tableName, id)
	if err := tx.Commit(context.Background()); err != nil {
		http.Error(w, "Failed to save record", http.StatusInternalServerError)
		return
	}
	applyAuthzSaveSideEffects(context.Background(), tableName, oldSnapshot, newSnapshot)
	if err := CaptureDataChange(context.Background(), updatedBy, tableName, id, operation, oldSnapshot, newSnapshot); err != nil {
		log.Printf("failed to capture data change audit table=%s id=%s err=%v", tableName, id, err)
	}
	if err := EmitTaskNotifications(r.Context(), tableName, id, operation, updatedBy, oldSnapshot, newSnapshot); err != nil {
		log.Printf("failed to emit task notifications table=%s id=%s err=%v", tableName, id, err)
	}
	realtime.PublishRecordChange(tableName, id, operation, updatedBy, realtimeClientID)

	http.Redirect(w, r, recordFormRedirectTarget(tableName, id, formName), http.StatusSeeOther)
}

func writeSaveDatabaseError(w http.ResponseWriter, err error) {
	if message, status, ok := classifySaveDatabaseError(err); ok {
		http.Error(w, message, status)
		return
	}
	http.Error(w, "Failed to save record", http.StatusInternalServerError)
}

func classifySaveDatabaseError(err error) (string, int, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "", 0, false
	}

	message := strings.TrimSpace(pgErr.Message)
	if message == "" {
		message = "Invalid data"
	}

	switch pgErr.Code {
	case "P0001", "23502", "23503", "23505", "23514", "22P02":
		return message, http.StatusBadRequest, true
	default:
		return "", 0, false
	}
}

func isSaveTransportField(name string) bool {
	switch strings.TrimSpace(name) {
	case "expected_version", "expected_updated_at", "csrf_token", "realtime_client_id", "form_name":
		return true
	default:
		return false
	}
}

func normalizeRuntimeFormName(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func recordFormRedirectTarget(tableName, recordID, formName string) string {
	target := "/f/" + tableName + "/" + recordID
	formName = normalizeRuntimeFormName(formName)
	if formName == "" || formName == "default" {
		return target
	}
	return target + "?form=" + formName
}

func applyAuthzSaveSideEffects(ctx context.Context, tableName string, oldSnapshot, newSnapshot map[string]any) {
	switch strings.ToLower(strings.TrimSpace(tableName)) {
	case "_group":
		InvalidateAuthzCache()
	case "_permission":
		InvalidateAuthzCache()
	case "_group_membership", "_user_role":
		InvalidateAuthzCache()
		for _, userID := range []string{
			snapshotStringValue(oldSnapshot, "user_id"),
			snapshotStringValue(newSnapshot, "user_id"),
		} {
			if strings.TrimSpace(userID) == "" {
				continue
			}
			if err := BumpUserSessionVersion(ctx, userID); err != nil {
				log.Printf("failed to bump user session table=%s user_id=%s err=%v", tableName, userID, err)
			}
			InvalidateAuthzCacheForUser(userID)
		}
	case "_group_role":
		InvalidateAuthzCache()
		for _, roleID := range []string{
			snapshotStringValue(oldSnapshot, "role_id"),
			snapshotStringValue(newSnapshot, "role_id"),
		} {
			if strings.TrimSpace(roleID) == "" {
				continue
			}
			if err := BumpSessionVersionForRole(ctx, roleID); err != nil {
				log.Printf("failed to bump role sessions table=%s role_id=%s err=%v", tableName, roleID, err)
			}
		}
	case "_role", "_role_permission", "_role_inheritance":
		InvalidateAuthzCache()
		for _, roleID := range []string{
			snapshotStringValue(oldSnapshot, "_id"),
			snapshotStringValue(newSnapshot, "_id"),
			snapshotStringValue(oldSnapshot, "role_id"),
			snapshotStringValue(newSnapshot, "role_id"),
		} {
			if strings.TrimSpace(roleID) == "" {
				continue
			}
			if err := BumpSessionVersionForRole(ctx, roleID); err != nil {
				log.Printf("failed to bump role sessions table=%s role_id=%s err=%v", tableName, roleID, err)
			}
		}
	}
}

func snapshotStringValue(snapshot map[string]any, key string) string {
	if len(snapshot) == 0 {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(snapshot[key]))
}

func NormalizeSubmittedValue(dataType, value string) (string, error) {
	switch normalizeDataType(dataType) {
	case "bool", "boolean":
		switch strings.ToLower(value) {
		case "1", "true", "on", "yes":
			return "true", nil
		default:
			return "false", nil
		}
	case "json", "jsonb":
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "null", nil
		}
		if !json.Valid([]byte(trimmed)) {
			return "", fmt.Errorf("invalid JSON value")
		}
		return trimmed, nil
	default:
		return value, nil
	}
}

func NormalizeSubmittedColumnValue(column Column, value string) (string, bool, error) {
	trimmed := strings.TrimSpace(value)
	normalized, err := NormalizeSubmittedValue(column.DATA_TYPE, trimmed)
	if err != nil {
		return "", false, err
	}
	if shouldStoreNullForBlankColumn(column, trimmed, normalized) {
		return "", true, nil
	}
	return normalized, false, nil
}

func shouldStoreNullForBlankColumn(column Column, rawValue, normalized string) bool {
	if !column.IS_NULLABLE {
		return false
	}
	if strings.TrimSpace(rawValue) != "" {
		return false
	}
	switch normalizeDataType(column.DATA_TYPE) {
	case "uuid", "reference", "choice",
		"bool", "boolean",
		"int", "integer", "serial",
		"bigint", "bigserial",
		"float", "double", "decimal", "numeric",
		"date",
		"timestamp", "timestamptz", "datetime":
		return true
	case "json", "jsonb":
		return normalized == "null"
	default:
		return false
	}
}

func submittedFormHasChanges(columns []Column, formData map[string]string, nullColumns map[string]bool, currentSnapshot map[string]any) bool {
	currentValues := comparableSnapshotValues(columns, currentSnapshot)
	for fieldName, submittedValue := range formData {
		expectedValue := submittedValue
		if nullColumns[fieldName] {
			expectedValue = ""
		}
		if currentValues[fieldName] != expectedValue {
			return true
		}
	}
	return false
}

func comparableSnapshotValues(columns []Column, snapshot map[string]any) map[string]string {
	out := make(map[string]string, len(columns))
	for _, column := range columns {
		if strings.HasPrefix(column.NAME, "_") {
			continue
		}
		out[column.NAME] = comparableSnapshotValue(column, snapshot[column.NAME])
	}
	return out
}

func comparableSnapshotValue(column Column, value any) string {
	dataType := normalizeDataType(column.DATA_TYPE)
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		switch dataType {
		case "date":
			if len(v) >= 10 {
				return v[:10]
			}
		case "timestamp", "timestamptz", "datetime":
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return t.Format("2006-01-02T15:04")
			}
			if len(v) >= 16 {
				return v[:16]
			}
		}
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case time.Time:
		switch dataType {
		case "date":
			return v.Format("2006-01-02")
		case "timestamp", "timestamptz", "datetime":
			return v.Format("2006-01-02T15:04")
		default:
			return v.Format(time.RFC3339)
		}
	default:
		return fmt.Sprint(v)
	}
}
