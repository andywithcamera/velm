package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func CaptureDataChange(ctx context.Context, userID, tableName, recordID, operation string, oldValues, newValues map[string]any) error {
	if userID == "" || tableName == "" || recordID == "" {
		return nil
	}

	oldMap := stringifyValueMap(oldValues)
	newMap := stringifyValueMap(newValues)
	diff := makeFieldDiff(oldMap, newMap)

	diffJSON, err := json.Marshal(diff)
	if err != nil {
		return err
	}
	oldJSON, err := json.Marshal(oldMap)
	if err != nil {
		return err
	}
	newJSON, err := json.Marshal(newMap)
	if err != nil {
		return err
	}

	_, err = Pool.Exec(ctx, `
		INSERT INTO _audit_data_change (user_id, table_name, record_id, operation, field_diff, old_values, new_values)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb)
	`,
		strings.TrimSpace(userID),
		strings.TrimSpace(tableName),
		strings.TrimSpace(recordID),
		strings.TrimSpace(operation),
		string(diffJSON),
		string(oldJSON),
		string(newJSON),
	)
	if err != nil {
		return err
	}
	emitDerivedDataChangeObservability(userID, tableName, recordID, operation, oldValues, newValues)
	return nil
}

func makeFieldDiff(oldMap, newMap map[string]string) map[string]map[string]string {
	diff := map[string]map[string]string{}
	seen := map[string]bool{}

	for key, oldVal := range oldMap {
		newVal, ok := newMap[key]
		if !ok {
			newVal = ""
		}
		if oldVal != newVal {
			diff[key] = map[string]string{"old": oldVal, "new": newVal}
		}
		seen[key] = true
	}
	for key, newVal := range newMap {
		if seen[key] {
			continue
		}
		diff[key] = map[string]string{"old": "", "new": newVal}
	}
	return diff
}

func stringifyValueMap(values map[string]any) map[string]string {
	out := map[string]string{}
	for key, val := range values {
		if strings.HasPrefix(key, "_") {
			continue
		}
		switch v := val.(type) {
		case nil:
			out[key] = ""
		case string:
			out[key] = v
		default:
			out[key] = fmt.Sprint(v)
		}
	}
	return out
}

func loadRecordSnapshot(tableName, recordID string) map[string]any {
	row := GetRecord(tableName, recordID)
	if row.Data == nil {
		return map[string]any{}
	}
	data, ok := row.Data.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return data
}
