package utils

import "github.com/jackc/pgx/v5/pgtype"

func NormalizeValue(val any) any {
	switch v := val.(type) {
	case nil:
		return nil
	case pgtype.UUID:
		if !v.Valid {
			return nil
		}
		return v.String()
	case *pgtype.UUID:
		if v == nil || !v.Valid {
			return nil
		}
		return v.String()
	case [16]byte:
		// Handle UUIDs
		return UuidToString(v[:])
	case []byte:
		// If the byte slice has length 16, it's likely a UUID
		if len(v) == 16 {
			return UuidToString(v)
		}
		// Otherwise, treat it as a string
		return string(v)
	case string:
		// If it's already a string, just return it
		return v
	case int, int32, int64:
		// If it's any integer type, return it as is
		return v
	case float64:
		// If it's a float64, return it
		return v
	case bool:
		// If it's a boolean, return it
		return v
	default:
		// For any unknown types, just return them as is
		return v
	}
}
