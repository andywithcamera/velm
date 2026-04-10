package db

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func loadRecordSnapshotWithQuerier(ctx context.Context, querier scriptQuerier, tableName, recordID string) map[string]any {
	recordID = strings.TrimSpace(recordID)
	if recordID == "" || recordID == "new" {
		return map[string]any{}
	}
	record, err := getScriptRecordWithQuerier(ctx, querier, tableName, recordID)
	if err != nil || record == nil {
		return map[string]any{}
	}
	return record
}

func allocateRecordIDWithQuerier(ctx context.Context, querier scriptQuerier) (string, error) {
	if querier == nil {
		querier = Pool
	}
	var recordID string
	if err := querier.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&recordID); err != nil {
		return "", err
	}
	return recordID, nil
}

func buildRuntimeTriggerRecord(view View, baseSnapshot map[string]any, formData map[string]string, nullColumns map[string]bool) (map[string]any, error) {
	record := cloneValueMap(baseSnapshot)
	columns := scriptColumnsByName(view)
	for fieldName, value := range formData {
		column, ok := columns[fieldName]
		if !ok {
			continue
		}
		native, err := runtimeFieldValue(column, value, nullColumns[fieldName])
		if err != nil {
			return nil, err
		}
		record[fieldName] = native
	}
	for fieldName := range nullColumns {
		if _, ok := columns[fieldName]; !ok {
			continue
		}
		if _, ok := formData[fieldName]; ok {
			continue
		}
		record[fieldName] = nil
	}
	return record, nil
}

func applyRuntimeTriggerRecordToFormData(view View, currentRecord, previousRecord map[string]any, isCreate bool, formData map[string]string, nullColumns map[string]bool) error {
	columns := scriptColumnsByName(view)
	for _, column := range view.Columns {
		if strings.HasPrefix(column.NAME, "_") {
			continue
		}
		delete(formData, column.NAME)
		delete(nullColumns, column.NAME)
	}

	for fieldName, rawValue := range currentRecord {
		column, ok := columns[fieldName]
		if !ok || strings.HasPrefix(fieldName, "_") {
			continue
		}

		normalized, isNull, err := normalizeScriptColumnValue(column, rawValue)
		if err != nil {
			return err
		}
		currentComparable := normalized
		if isNull {
			currentComparable = ""
		}

		if !isCreate && currentComparable == comparableSnapshotValue(column, previousRecord[fieldName]) {
			continue
		}
		if isNull {
			nullColumns[fieldName] = true
			continue
		}
		formData[fieldName] = normalized
		delete(nullColumns, fieldName)
	}
	return nil
}

func runtimeFieldValue(column Column, normalized string, isNull bool) (any, error) {
	if isNull {
		return nil, nil
	}
	switch normalizeDataType(column.DATA_TYPE) {
	case "bool", "boolean":
		return strings.EqualFold(normalized, "true"), nil
	case "int", "integer", "serial":
		value, err := strconv.Atoi(normalized)
		if err != nil {
			return nil, err
		}
		return value, nil
	case "bigint", "bigserial":
		value, err := strconv.ParseInt(normalized, 10, 64)
		if err != nil {
			return nil, err
		}
		return value, nil
	case "float", "double", "decimal", "numeric":
		value, err := strconv.ParseFloat(normalized, 64)
		if err != nil {
			return nil, err
		}
		return value, nil
	case "json", "jsonb":
		if strings.TrimSpace(normalized) == "" {
			return nil, nil
		}
		var value any
		if err := json.Unmarshal([]byte(normalized), &value); err != nil {
			return nil, err
		}
		return value, nil
	default:
		return normalized, nil
	}
}

func filterSubmittedColumns(columns []Column, formData map[string]string, nullColumns map[string]bool) {
	allowed := make(map[string]bool, len(columns))
	for _, column := range columns {
		allowed[column.NAME] = true
	}
	for fieldName := range formData {
		if !allowed[fieldName] {
			delete(formData, fieldName)
		}
	}
	for fieldName := range nullColumns {
		if !allowed[fieldName] {
			delete(nullColumns, fieldName)
		}
	}
}

func buildInsertColumnsAndValues(columnsByName map[string]Column, formData map[string]string, nullColumns map[string]bool) ([]string, []any, []string, error) {
	keys := make([]string, 0, allocHintSum(len(formData), len(nullColumns)))
	seen := map[string]bool{}
	for key := range formData {
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	for key := range nullColumns {
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	sort.Strings(keys)

	quotedColumns := make([]string, 0, len(keys))
	values := make([]any, 0, len(keys))
	placeholders := make([]string, 0, len(keys))
	for index, key := range keys {
		quotedColumn, err := QuoteIdentifier(key)
		if err != nil {
			return nil, nil, nil, err
		}
		quotedColumns = append(quotedColumns, quotedColumn)
		if nullColumns[key] {
			values = append(values, nil)
		} else {
			values = append(values, formData[key])
		}
		cast := ""
		if column, ok := columnsByName[key]; ok {
			cast = scriptColumnCast(column.DATA_TYPE)
		}
		placeholders = append(placeholders, fmt.Sprintf("$%d%s", index+1, cast))
	}
	return quotedColumns, values, placeholders, nil
}
