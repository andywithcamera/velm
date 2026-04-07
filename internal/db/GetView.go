package db

import (
	"context"
	"fmt"
	"strings"
)

func GetView(tableName string) View {
	return GetViewContext(context.Background(), tableName)
}

func GetViewContext(ctx context.Context, tableName string) View {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		return View{}
	}
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		if view, ok := cache.cachedView(tableName); ok {
			return view
		}
	}

	table := GetTableContext(ctx, tableName)
	if table.ID == "" {
		fmt.Printf("Table %s not found\n", tableName)
		return View{}
	}

	columns, err := GetColumnsContext(ctx, table.ID)
	if err != nil {
		fmt.Printf("Error retrieving columns for table %s: %v\n", tableName, err)
		return View{}
	}

	view := View{
		Table:   &table,
		Columns: columns,
	}
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		cache.storeView(tableName, view)
	}
	return view
}
