package db

import (
	"context"
	"fmt"
	"velm/internal/utils"
	"strings"
)

func GetRecords(tableName string, whereClause string, args ...any) MultiRow {
	view := GetView(tableName)
	if view.Table.ID == "" {
		fmt.Printf("Table %s not found\n", tableName)
		return MultiRow{
			View: nil,
			Data: nil,
		}
	}

	// Prepare slices for scanning
	colNames := make([]string, len(view.Columns))
	quotedColNames := make([]string, len(view.Columns))
	for i, col := range view.Columns {
		if !IsSafeIdentifier(col.NAME) {
			fmt.Printf("Unsafe column name on table %s: %s\n", tableName, col.NAME)
			return MultiRow{
				View: &view,
				Data: nil,
			}
		}
		colNames[i] = col.NAME
		quotedColNames[i] = `"` + col.NAME + `"`
	}

	quotedTableName, err := QuoteIdentifier(view.Table.NAME)
	if err != nil {
		fmt.Printf("Unsafe table name %s: %v\n", view.Table.NAME, err)
		return MultiRow{
			View: &view,
			Data: nil,
		}
	}

	ctx := context.Background()
	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s",
		strings.Join(quotedColNames, ", "),
		quotedTableName,
		whereClause,
	)

	rows, err := Pool.Query(ctx, query, args...)
	if err != nil {
		fmt.Printf("Error executing query: %v\n", err)
		return MultiRow{
			View: &view,
			Data: nil,
		}
	}
	defer rows.Close()

	var results []any
	for rows.Next() {
		vals := make([]any, len(view.Columns))
		valPtrs := make([]any, len(view.Columns))
		for i := range vals {
			valPtrs[i] = &vals[i]
		}

		if err := rows.Scan(valPtrs...); err != nil {
			fmt.Printf("Error scanning row: %v\n", err)
			continue
		}

		rowData := make(map[string]any)
		for i, col := range view.Columns {
			rowData[col.NAME] = utils.NormalizeValue(vals[i])
		}
		results = append(results, rowData)
	}

	return MultiRow{
		View: &view,
		Data: results,
	}
}
