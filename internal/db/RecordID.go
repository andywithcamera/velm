package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"velm/internal/utils"
)

func IsValidRecordID(tableName, id string) bool {
	if strings.TrimSpace(id) == "new" {
		return true
	}
	_, err := ParseRecordIDValue(tableName, id)
	return err == nil
}

func ParseRecordIDValue(tableName, id string) (any, error) {
	return ParseRecordIDValueContext(context.Background(), tableName, id)
}

// ParseRecordIDValueContext keeps the request metadata cache in play. Record id
// kind is derived from the table view, so resolving it context-free reloads
// every app definition on each call.
func ParseRecordIDValueContext(ctx context.Context, tableName, id string) (any, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return nil, fmt.Errorf("record id is required")
	}

	switch recordIDKindContext(ctx, tableName) {
	case "int":
		value, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("invalid integer record id")
		}
		return value, nil
	default:
		if !utils.IsValidUUID(trimmed) {
			return nil, fmt.Errorf("invalid uuid record id")
		}
		return trimmed, nil
	}
}

func recordIDKind(tableName string) string {
	return recordIDKindContext(context.Background(), tableName)
}

func recordIDKindContext(ctx context.Context, tableName string) string {
	if Pool == nil {
		return "uuid"
	}

	view := GetViewContext(ctx, tableName)
	for _, col := range view.Columns {
		if col.NAME != "_id" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(col.DATA_TYPE)) {
		case "int", "integer", "bigint", "bigserial", "serial":
			return "int"
		default:
			return "uuid"
		}
	}
	return "uuid"
}
