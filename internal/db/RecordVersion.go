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

// RecordVersionValue returns the value the UI treats as a record's version:
// the update counter when the record carries one, otherwise the exact update
// timestamp.
//
// Both the GET that renders a form and the save response that reports the
// committed version call this, so the two cannot drift: if they did, every
// save would look like someone else's change and the page would claim
// "updates are available" for a write the reader had just made.
func RecordVersionValue(snapshot map[string]any) string {
	if snapshot == nil {
		return ""
	}
	if count, ok := snapshot["_update_count"]; ok && count != nil {
		if version := strings.TrimSpace(fmt.Sprint(count)); version != "" && version != "<nil>" {
			return version
		}
	}
	return formatExactRecordTimestamp(snapshot["_updated_at"])
}

// formatExactRecordTimestamp renders a timestamp without truncation and
// without normalising its location, so the value round-trips through the
// client's comparison unchanged.
func formatExactRecordTimestamp(value any) string {
	switch stamp := value.(type) {
	case nil:
		return ""
	case time.Time:
		return stamp.Format(time.RFC3339Nano)
	case string:
		trimmed := strings.TrimSpace(stamp)
		if trimmed == "" {
			return ""
		}
		if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return parsed.Format(time.RFC3339Nano)
		}
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			return parsed.Format(time.RFC3339Nano)
		}
		return trimmed
	default:
		return strings.TrimSpace(fmt.Sprint(stamp))
	}
}
