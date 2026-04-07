package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"velm/internal/utils"
)

func GetRecord(tableName string, id string) SingleRow {
	return GetRecordContext(context.Background(), tableName, id)
}

func GetRecordContext(ctx context.Context, tableName string, id string) SingleRow {
	view := GetViewContext(ctx, tableName)
	if view.Table.ID == "" {
		fmt.Printf("Table %s not found\n", tableName)
		return SingleRow{
			View: nil,
			Data: nil,
		}
	}

	if id == "" {
		fmt.Printf("ID cannot be empty for table %s\n", tableName)
		return SingleRow{
			View: &view,
			Data: nil,
		}
	}
	if strings.TrimSpace(id) == "new" {
		result := make(map[string]any, len(view.Columns))
		for _, col := range view.Columns {
			result[col.NAME] = nil
		}
		return SingleRow{
			View: &view,
			Data: result,
		}
	}

	// Prepare slices for scanning
	colNames := make([]string, len(view.Columns))
	quotedColNames := make([]string, len(view.Columns))
	vals := make([]any, len(view.Columns))
	valPtrs := make([]any, len(view.Columns))
	for i, col := range view.Columns {
		if !IsSafeIdentifier(col.NAME) {
			fmt.Printf("Unsafe column name on table %s: %s\n", tableName, col.NAME)
			return SingleRow{
				View: &view,
				Data: nil,
			}
		}
		colNames[i] = col.NAME
		quotedColNames[i] = `"` + col.NAME + `"`
		valPtrs[i] = &vals[i]
	}

	quotedTableName, err := QuoteIdentifier(view.Table.NAME)
	if err != nil {
		fmt.Printf("Unsafe table name %s: %v\n", view.Table.NAME, err)
		return SingleRow{
			View: &view,
			Data: nil,
		}
	}

	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE _id=$1",
		strings.Join(quotedColNames, ", "),
		quotedTableName,
	)

	recordIDValue, err := ParseRecordIDValue(tableName, id)
	if err != nil {
		fmt.Printf("Invalid record ID %s for table %s: %v\n", id, tableName, err)
		return SingleRow{
			View: &view,
			Data: nil,
		}
	}

	row := Pool.QueryRow(ctx, query, recordIDValue)

	err = row.Scan(valPtrs...)
	if err != nil {
		if err == sql.ErrNoRows {
			fmt.Printf("No data found for ID %s in table %s\n", id, tableName)
			return SingleRow{
				View: &view,
				Data: nil,
			}
		}
		fmt.Printf("Error scanning row: %v\n", err)
		return SingleRow{
			View: &view,
			Data: nil,
		}
	}

	// Build a map of column name to value
	result := make(map[string]any)
	for i, colName := range colNames {
		result[colName] = utils.NormalizeValue(vals[i])
	}

	return SingleRow{
		View: &view,
		Data: result,
	}
}
