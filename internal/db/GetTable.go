package db

import (
	"context"
	"log"
	"strings"
)

func GetTable(tableName string) Table {
	return GetTableContext(context.Background(), tableName)
}

func GetTableContext(ctx context.Context, tableName string) Table {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" || !IsSafeIdentifier(tableName) {
		return Table{}
	}
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		if table, ok := cache.cachedTable(tableName); ok {
			return table
		}
	}

	result := Table{}

	if app, table, ok, err := FindYAMLTableByName(ctx, tableName); err == nil && ok {
		result = BuildYAMLTable(app, table)
	} else if err != nil {
		log.Printf("get table yaml lookup failed table=%s err=%v", tableName, err)
	} else if table, ok, err := GetPhysicalTable(ctx, tableName); err == nil && ok {
		result = table
	} else if err != nil {
		log.Printf("get table physical lookup failed table=%s err=%v", tableName, err)
	}

	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		cache.storeTable(tableName, result)
	}
	return result
}

func TableExists(ctx context.Context, tableName string) bool {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" || !IsSafeIdentifier(tableName) {
		return false
	}

	if _, _, ok, err := FindYAMLTableByName(ctx, tableName); err == nil && ok {
		return true
	}

	if _, ok, err := GetPhysicalTable(ctx, tableName); err == nil && ok {
		return true
	}

	return false
}
