package db

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"velm/internal/utils"
)

var (
	varcharTypeRegex   = regexp.MustCompile(`^varchar\(([1-9][0-9]{0,4})\)$`)
	enumTypeRegex      = regexp.MustCompile(`^enum:[a-z0-9_\-]+(?:\|[a-z0-9_\-]+)+$`)
	referenceTypeRegex = regexp.MustCompile(`^reference:[a-z_][a-z0-9_]*$`)
	codeTypeRegex      = regexp.MustCompile(`^code(?::[a-z0-9][a-z0-9_\+\.\-]*)?$`)
	autoNumberRegex    = regexp.MustCompile(`^[A-Z]{3,4}$`)
)

var protectedTableNames = map[string]bool{
	"_user":                 true,
	"_table":                true,
	"_column":               true,
	"_menu":                 true,
	"_property":             true,
	"_role":                 true,
	"_permission":           true,
	"_role_permission":      true,
	"_user_role":            true,
	"_audit_log":            true,
	"_audit_data_change":    true,
	"_request_metric":       true,
	"_record_comment":       true,
	"_security_log":         true,
	"_user_preference":      true,
	"_user_auth_factor":     true,
	"_user_recovery_code":   true,
	"_seed_pack_release":    true,
	"_schema_migration":     true,
	"_demo_seed":            true,
	"_app":                  true,
	"_item":                 true,
	"_docs_library":         true,
	"_docs_article":         true,
	"_docs_article_version": true,
	"base_counters":         true,
}

var adminOnlyTableNames = map[string]bool{
	"_audit_log":        true,
	"_request_metric":   true,
	"_group":            true,
	"_group_membership": true,
	"_group_role":       true,
	"_permission":       true,
	"_property":         true,
	"_role":             true,
	"_role_inheritance": true,
	"_role_permission":  true,
	"_user_role":        true,
}

var immutableTableNames = map[string]bool{
	"_audit_log":         true,
	"_audit_data_change": true,
	"_request_metric":    true,
	"_security_log":      true,
}

func ValidateMetadataWrite(ctx context.Context, tableName, recordID string, formData map[string]string) error {
	_ = recordID
	switch strings.TrimSpace(strings.ToLower(tableName)) {
	case "base_task":
		return validateBaseTaskMetadataWrite(ctx, formData)
	default:
		return nil
	}
}

func validateBaseTaskMetadataWrite(ctx context.Context, formData map[string]string) error {
	groupID := strings.TrimSpace(formData["assignment_group_id"])
	userID := strings.TrimSpace(formData["assigned_user_id"])
	if userID == "" {
		return nil
	}
	if groupID == "" {
		return fmt.Errorf("assigned_user_id requires assignment_group_id")
	}
	if Pool == nil || !utils.IsValidUUID(groupID) || !utils.IsValidUUID(userID) {
		return nil
	}

	var exists bool
	err := Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM _group_membership gm
			WHERE gm.group_id = $1
			  AND gm.user_id = $2
		)
	`, groupID, userID).Scan(&exists)
	if err != nil {
		return nil
	}
	if !exists {
		return fmt.Errorf("assigned_user_id must be a member of assignment_group_id")
	}
	return nil
}

func IsAdminOnlyTableName(name string) bool {
	n := strings.TrimSpace(strings.ToLower(name))
	return adminOnlyTableNames[n]
}

func IsImmutableTableName(name string) bool {
	n := strings.TrimSpace(strings.ToLower(name))
	return immutableTableNames[n]
}

func validateBuilderTableName(name string) error {
	if !IsSafeIdentifier(name) {
		return fmt.Errorf("invalid table name")
	}
	if strings.HasPrefix(name, "_") {
		return fmt.Errorf("table names starting with '_' are reserved")
	}
	if len(name) > 63 {
		return fmt.Errorf("table name exceeds 63 characters")
	}
	return nil
}

func validateBuilderColumnName(name string) error {
	if !IsSafeIdentifier(name) {
		return fmt.Errorf("invalid column name")
	}
	if strings.HasPrefix(name, "_") {
		return fmt.Errorf("column names starting with '_' are reserved")
	}
	if len(name) > 63 {
		return fmt.Errorf("column name exceeds 63 characters")
	}
	return nil
}

func validateBuilderColumnDataType(dataType string) error {
	dt := normalizeDataType(dataType)
	if dt == "" {
		return fmt.Errorf("data_type is required")
	}

	switch dt {
	case "text", "int", "integer", "bigint", "bigserial", "serial", "float", "double", "decimal", "numeric",
		"bool", "boolean", "date", "timestamp", "timestamptz", "datetime",
		"uuid", "json", "jsonb", "reference", "choice", "long_text",
		"richtext", "markdown", "email", "url", "phone", "code", "autnumber":
		return nil
	}

	if match := varcharTypeRegex.FindStringSubmatch(dt); len(match) == 2 {
		size, _ := strconv.Atoi(match[1])
		if size < 1 || size > 65535 {
			return fmt.Errorf("varchar size must be between 1 and 65535")
		}
		return nil
	}
	if enumTypeRegex.MatchString(dt) {
		return nil
	}
	if referenceTypeRegex.MatchString(dt) {
		return nil
	}
	if codeTypeRegex.MatchString(dt) {
		return nil
	}

	return fmt.Errorf("unsupported data_type %q", dataType)
}

func normalizeDataType(dataType string) string {
	dt := strings.ToLower(strings.TrimSpace(dataType))
	switch dt {
	case "longtext", "long-text":
		return "long_text"
	case "rich_text", "rich-text":
		return "richtext"
	case "autonumber", "auto_number", "auto-number":
		return "autnumber"
	}
	if strings.HasPrefix(dt, "reference:") {
		refTable := normalizeIdentifier(strings.TrimSpace(strings.TrimPrefix(dt, "reference:")))
		if refTable == "" {
			return "reference"
		}
		return "reference:" + refTable
	}
	if strings.HasPrefix(dt, "code:") {
		lang := strings.TrimSpace(strings.TrimPrefix(dt, "code:"))
		if lang == "" {
			return "code"
		}
		return "code:" + lang
	}
	return dt
}

func CanonicalDataType(dataType string) string {
	return normalizeDataType(dataType)
}

func BaseDataType(dataType string) string {
	dt := normalizeDataType(dataType)
	switch {
	case strings.HasPrefix(dt, "reference:"):
		return "reference"
	case strings.HasPrefix(dt, "code:"):
		return "code"
	default:
		return dt
	}
}

func IsReferenceDataType(dataType string) bool {
	return BaseDataType(dataType) == "reference"
}

func DataTypeReferenceTable(dataType string) string {
	dt := normalizeDataType(dataType)
	if !strings.HasPrefix(dt, "reference:") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(dt, "reference:"))
}

func ResolveReferenceTable(dataType, explicit string) (string, error) {
	typed := DataTypeReferenceTable(dataType)
	explicit = normalizeIdentifier(explicit)
	switch {
	case typed != "" && explicit != "" && typed != explicit:
		return "", fmt.Errorf("reference_table %q does not match data_type %q", explicit, normalizeDataType(dataType))
	case typed != "":
		return typed, nil
	default:
		return explicit, nil
	}
}

func IsCodeDataType(dataType string) bool {
	return BaseDataType(dataType) == "code"
}

func DataTypeCodeLanguage(dataType string) string {
	dt := normalizeDataType(dataType)
	if !strings.HasPrefix(dt, "code:") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(dt, "code:"))
}

func IsRichTextDataType(dataType string) bool {
	return BaseDataType(dataType) == "richtext"
}

func IsAutoNumberDataType(dataType string) bool {
	return BaseDataType(dataType) == "autnumber"
}

func normalizeAutoNumberPrefix(prefix string) string {
	return strings.ToUpper(strings.TrimSpace(prefix))
}

func validateAutoNumberPrefix(prefix string) error {
	prefix = normalizeAutoNumberPrefix(prefix)
	if !autoNumberRegex.MatchString(prefix) {
		return fmt.Errorf("prefix must be three or four uppercase letters")
	}
	return nil
}

func isProtectedTableName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	if strings.HasPrefix(n, "_") {
		return true
	}
	return protectedTableNames[n]
}
