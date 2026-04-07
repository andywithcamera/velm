package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func GetRecordVersion(ctx context.Context, tableName, id string) (string, bool, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	id = strings.TrimSpace(id)
	if tableName == "" || !IsSafeIdentifier(tableName) {
		return "", false, fmt.Errorf("invalid table name")
	}
	if id == "" || !IsValidRecordID(tableName, id) {
		return "", false, fmt.Errorf("invalid record id")
	}

	view := GetView(tableName)
	if view.Table == nil || view.Table.ID == "" {
		return "", false, nil
	}

	hasUpdateCount := false
	hasUpdatedAt := false
	for _, column := range view.Columns {
		if strings.EqualFold(strings.TrimSpace(column.NAME), "_update_count") {
			hasUpdateCount = true
		}
		if strings.EqualFold(strings.TrimSpace(column.NAME), "_updated_at") {
			hasUpdatedAt = true
		}
	}
	if hasUpdateCount {
		quotedTable, err := QuoteIdentifier(view.Table.NAME)
		if err != nil {
			return "", false, err
		}
		recordIDValue, err := ParseRecordIDValue(tableName, id)
		if err != nil {
			return "", false, err
		}

		var version string
		if err := Pool.QueryRow(ctx, fmt.Sprintf(`SELECT COALESCE("_update_count", 0)::text FROM %s WHERE "_id" = $1`, quotedTable), recordIDValue).Scan(&version); err != nil {
			if err == sql.ErrNoRows {
				return "", false, nil
			}
			return "", false, err
		}
		return strings.TrimSpace(version), true, nil
	}
	if !hasUpdatedAt {
		return "", true, nil
	}

	quotedTable, err := QuoteIdentifier(view.Table.NAME)
	if err != nil {
		return "", false, err
	}
	recordIDValue, err := ParseRecordIDValue(tableName, id)
	if err != nil {
		return "", false, err
	}

	var updatedAt time.Time
	if err := Pool.QueryRow(ctx, fmt.Sprintf(`SELECT "_updated_at" FROM %s WHERE "_id" = $1`, quotedTable), recordIDValue).Scan(&updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}

	return updatedAt.UTC().Format(time.RFC3339Nano), true, nil
}
