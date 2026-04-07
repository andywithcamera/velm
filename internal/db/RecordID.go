package db

import (
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
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return nil, fmt.Errorf("record id is required")
	}

	switch recordIDKind(tableName) {
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
	if Pool == nil {
		return "uuid"
	}

	view := GetView(tableName)
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
