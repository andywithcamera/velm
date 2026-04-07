package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

const physicalTableIDPrefix = "physical:table:"

type builtinTableMeta struct {
	LabelSingular string
	LabelPlural   string
	Description   string
	DisplayField  string
}

var builtinTableMetadata = map[string]builtinTableMeta{
	"_audit_log": {
		LabelSingular: "System Log",
		LabelPlural:   "System Logs",
		Description:   "Platform request and audit log entries.",
		DisplayField:  "path",
	},
	"_audit_data_change": {
		LabelSingular: "Record Update",
		LabelPlural:   "Record Updates",
		Description:   "Field-level record change history.",
		DisplayField:  "field_name",
	},
	"_request_metric": {
		LabelSingular: "Request Metric",
		LabelPlural:   "Request Metrics",
		Description:   "End-to-end request timing with server, database, and client measurements.",
		DisplayField:  "path",
	},
	"_docs_article": {
		LabelSingular: "Doc",
		LabelPlural:   "Docs",
		Description:   "Markdown docs.",
		DisplayField:  "title",
	},
	"_docs_article_version": {
		LabelSingular: "Doc Version",
		LabelPlural:   "Doc Versions",
		Description:   "Version history for docs.",
		DisplayField:  "version_num",
	},
	"_docs_library": {
		LabelSingular: "Docs Library",
		LabelPlural:   "Docs Libraries",
		Description:   "Docs libraries.",
		DisplayField:  "name",
	},
	"_saved_view": {
		LabelSingular: "Saved View",
		LabelPlural:   "Saved Views",
		Description:   "Saved list and filter configurations.",
		DisplayField:  "name",
	},
	"_group": {
		LabelSingular: "Group",
		LabelPlural:   "Groups",
		Description:   "User groups used for role assignment.",
		DisplayField:  "name",
	},
	"_group_membership": {
		LabelSingular: "Group Membership",
		LabelPlural:   "Group Memberships",
		Description:   "User-to-group assignments.",
	},
	"_group_role": {
		LabelSingular: "Role Assignment",
		LabelPlural:   "Role Assignments",
		Description:   "Group-to-role assignments.",
	},
	"_permission": {
		LabelSingular: "Permission",
		LabelPlural:   "Permissions",
		Description:   "Authorization permissions.",
		DisplayField:  "description",
	},
	"_property": {
		LabelSingular: "Property",
		LabelPlural:   "Properties",
		Description:   "System runtime properties.",
		DisplayField:  "key",
	},
	"_role": {
		LabelSingular: "Role",
		LabelPlural:   "Roles",
		Description:   "Authorization roles.",
		DisplayField:  "name",
	},
	"_role_inheritance": {
		LabelSingular: "Role Inheritance",
		LabelPlural:   "Role Inheritance",
		Description:   "Role-to-role inheritance links.",
	},
	"_role_permission": {
		LabelSingular: "Role Permission",
		LabelPlural:   "Role Permissions",
		Description:   "Role-to-permission assignments.",
	},
	"_user": {
		LabelSingular: "User",
		LabelPlural:   "Users",
		Description:   "Platform user accounts.",
		DisplayField:  "name",
	},
	"_user_preference": {
		LabelSingular: "Preference",
		LabelPlural:   "Preferences",
		Description:   "Per-user interface preferences.",
		DisplayField:  "pref_key",
	},
	"_user_notification": {
		LabelSingular: "Notification",
		LabelPlural:   "Notifications",
		Description:   "Per-user in-app notifications written by scripts and platform flows.",
		DisplayField:  "title",
	},
	"_user_role": {
		LabelSingular: "User Role",
		LabelPlural:   "User Roles",
		Description:   "Direct user-to-role assignments.",
	},
}

func GetPhysicalTable(ctx context.Context, tableName string) (Table, bool, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" || !IsSafeIdentifier(tableName) {
		return Table{}, false, nil
	}

	var exists bool
	err := Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = current_schema()
			  AND table_type = 'BASE TABLE'
			  AND table_name = $1
		)
	`, tableName).Scan(&exists)
	if err != nil {
		return Table{}, false, fmt.Errorf("load physical table: %w", err)
	}
	if !exists {
		return Table{}, false, nil
	}

	meta := builtinTableMetadata[tableName]
	labelSingular := strings.TrimSpace(meta.LabelSingular)
	if labelSingular == "" {
		labelSingular = humanizeCatalogIdentifier(strings.TrimPrefix(tableName, "_"))
	}
	labelPlural := strings.TrimSpace(meta.LabelPlural)
	if labelPlural == "" {
		labelPlural = pluralizeCatalogLabel(labelSingular)
	}

	return Table{
		ID:             physicalTableIDPrefix + tableName,
		NAME:           tableName,
		CREATED_AT:     time.Time{},
		CREATED_BY:     "",
		UPDATED_AT:     time.Time{},
		UPDATED_BY:     "",
		LABEL_SINGULAR: labelSingular,
		LABEL_PLURAL:   labelPlural,
		DESCRIPTION:    strings.TrimSpace(meta.Description),
		DISPLAY_FIELD:  NormalizeDisplayFieldName(meta.DisplayField),
	}, true, nil
}

func parsePhysicalTableID(input string) (string, bool) {
	if !strings.HasPrefix(input, physicalTableIDPrefix) {
		return "", false
	}
	tableName := strings.TrimSpace(strings.TrimPrefix(input, physicalTableIDPrefix))
	if tableName == "" || !IsSafeIdentifier(tableName) {
		return "", false
	}
	return tableName, true
}

func GetPhysicalColumns(ctx context.Context, tableName string) ([]Column, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" || !IsSafeIdentifier(tableName) {
		return nil, fmt.Errorf("invalid table name")
	}

	rows, err := Pool.Query(ctx, `
		SELECT column_name, data_type, udt_name, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = $1
		ORDER BY ordinal_position ASC
	`, tableName)
	if err != nil {
		return nil, fmt.Errorf("load physical columns: %w", err)
	}
	defer rows.Close()

	columns := make([]Column, 0, 16)
	tableID := physicalTableIDPrefix + tableName
	for rows.Next() {
		var name string
		var dataType string
		var udtName string
		var isNullable string
		var defaultValue string
		if err := rows.Scan(&name, &dataType, &udtName, &isNullable, &defaultValue); err != nil {
			return nil, fmt.Errorf("scan physical column: %w", err)
		}
		column := Column{
			ID:               "physical:column:" + tableName + ":" + name,
			NAME:             name,
			CREATED_AT:       time.Time{},
			CREATED_BY:       "",
			UPDATED_AT:       time.Time{},
			UPDATED_BY:       "",
			LABEL:            humanizeCatalogIdentifier(strings.TrimPrefix(name, "_")),
			DATA_TYPE:        normalizeCatalogDataType(dataType, udtName),
			IS_NULLABLE:      strings.EqualFold(strings.TrimSpace(isNullable), "YES"),
			DEFAULT_VALUE:    nullableCatalogString(defaultValue),
			IS_HIDDEN:        strings.HasPrefix(name, "_"),
			IS_READONLY:      false,
			VALIDATION_REGEX: sql.NullString{},
			CONDITION_EXPR:   sql.NullString{},
			VALIDATION_MSG:   sql.NullString{},
			TABLE_ID:         tableID,
		}
		if refTable := builtinReferenceTable(name); refTable != "" {
			column.REFERENCE_TABLE = sql.NullString{String: refTable, Valid: true}
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate physical columns: %w", err)
	}
	return columns, nil
}

func builtinReferenceTable(columnName string) string {
	switch strings.ToLower(strings.TrimSpace(columnName)) {
	case "user_id":
		return "_user"
	case "group_id":
		return "_group"
	case "role_id", "inherits_role_id":
		return "_role"
	case "permission_id":
		return "_permission"
	default:
		return ""
	}
}

func ListPhysicalBaseTables(ctx context.Context) ([]string, error) {
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		if names, err, ok := cache.cachedPhysicalBaseTables(); ok {
			return names, err
		}
	}

	names, err := listPhysicalBaseTablesUncached(ctx)
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		cache.storePhysicalBaseTables(names, err)
	}
	return names, err
}

func listPhysicalBaseTablesUncached(ctx context.Context) ([]string, error) {
	rows, err := Pool.Query(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_type = 'BASE TABLE'
		ORDER BY table_name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list physical tables: %w", err)
	}
	defer rows.Close()

	names := make([]string, 0, 32)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan physical table name: %w", err)
		}
		names = append(names, strings.TrimSpace(strings.ToLower(name)))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate physical tables: %w", err)
	}
	sort.Strings(names)
	return names, nil
}

func buildPhysicalBuilderTable(ctx context.Context, tableName string) (BuilderTableSummary, error) {
	table, ok, err := GetPhysicalTable(ctx, tableName)
	if err != nil {
		return BuilderTableSummary{}, err
	}
	if !ok {
		return BuilderTableSummary{}, fmt.Errorf("table not found")
	}
	columns, err := GetPhysicalColumns(ctx, tableName)
	if err != nil {
		return BuilderTableSummary{}, err
	}
	return BuilderTableSummary{
		ID:            table.ID,
		Name:          table.NAME,
		LabelSingular: table.LABEL_SINGULAR,
		LabelPlural:   table.LABEL_PLURAL,
		Description:   table.DESCRIPTION,
		ColumnCount:   len(columns),
	}, nil
}

func buildPhysicalBuilderColumns(ctx context.Context, tableName string) ([]BuilderColumnSummary, error) {
	columns, err := GetPhysicalColumns(ctx, tableName)
	if err != nil {
		return nil, err
	}
	items := make([]BuilderColumnSummary, 0, len(columns))
	for _, column := range columns {
		items = append(items, BuilderColumnSummary{
			ID:             column.ID,
			Name:           column.NAME,
			Label:          column.LABEL,
			DataType:       column.DATA_TYPE,
			IsNullable:     column.IS_NULLABLE,
			DefaultValue:   strings.TrimSpace(column.DEFAULT_VALUE.String),
			ValidationRule: strings.TrimSpace(column.VALIDATION_REGEX.String),
		})
	}
	return items, nil
}

func humanizeCatalogIdentifier(input string) string {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return ""
	}
	parts := strings.Split(input, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func pluralizeCatalogLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	lower := strings.ToLower(label)
	switch {
	case strings.HasSuffix(lower, "s"):
		return label
	case strings.HasSuffix(lower, "y") && len(label) > 1:
		return label[:len(label)-1] + "ies"
	default:
		return label + "s"
	}
}

func nullableCatalogString(value string) sql.NullString {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func normalizeCatalogDataType(dataType, udtName string) string {
	switch strings.TrimSpace(strings.ToLower(dataType)) {
	case "boolean":
		return "boolean"
	case "date":
		return "date"
	case "double precision":
		return "double"
	case "integer", "smallint":
		return "integer"
	case "bigint":
		return "bigint"
	case "json":
		return "json"
	case "jsonb":
		return "jsonb"
	case "numeric":
		return "numeric"
	case "real":
		return "float"
	case "text":
		return "text"
	case "timestamp with time zone":
		return "timestamptz"
	case "timestamp without time zone":
		return "timestamp"
	case "uuid":
		return "uuid"
	case "character varying":
		return "text"
	case "user-defined":
		if strings.EqualFold(strings.TrimSpace(udtName), "jsonb") {
			return "jsonb"
		}
		return "text"
	default:
		return normalizeDataType(strings.TrimSpace(strings.ToLower(dataType)))
	}
}
