package db

import (
	"context"
	"fmt"
)

func GetColumns(table_id string) ([]Column, error) {
	return GetColumnsContext(context.Background(), table_id)
}

func GetColumnsContext(ctx context.Context, table_id string) ([]Column, error) {
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		if columns, err, ok := cache.cachedColumns(table_id); ok {
			return columns, err
		}
	}

	var (
		columns []Column
		err     error
	)
	if app, table, ok, err := FindYAMLTableByID(ctx, table_id); err != nil {
		if cache := requestMetadataCacheFromContext(ctx); cache != nil {
			cache.storeColumns(table_id, nil, err)
		}
		return nil, err
	} else if ok {
		columns = BuildYAMLColumnsWithContext(ctx, app, table)
		if cache := requestMetadataCacheFromContext(ctx); cache != nil {
			cache.storeColumns(table_id, columns, nil)
		}
		return columns, nil
	}

	if tableName, ok := parsePhysicalTableID(table_id); ok {
		columns, err = GetPhysicalColumns(ctx, tableName)
		if cache := requestMetadataCacheFromContext(ctx); cache != nil {
			cache.storeColumns(table_id, columns, err)
		}
		return columns, err
	}

	err = fmt.Errorf("unknown table id %s", table_id)
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		cache.storeColumns(table_id, nil, err)
	}
	return nil, err
}
