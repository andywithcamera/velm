package db

import (
	"database/sql"
	"strings"
	"time"
)

var recordSystemColumnTemplates = []AppDefinitionColumn{
	{
		Name:       "_id",
		Label:      "ID",
		DataType:   "uuid",
		IsNullable: false,
	},
	{
		Name:       "_created_at",
		Label:      "Created At",
		DataType:   "timestamptz",
		IsNullable: false,
	},
	{
		Name:       "_updated_at",
		Label:      "Updated At",
		DataType:   "timestamptz",
		IsNullable: false,
	},
	{
		Name:       "_update_count",
		Label:      "Update Count",
		DataType:   "bigint",
		IsNullable: false,
	},
	{
		Name:       "_deleted_at",
		Label:      "Deleted At",
		DataType:   "timestamptz",
		IsNullable: true,
	},
	{
		Name:           "_created_by",
		Label:          "Created By",
		DataType:       "reference",
		IsNullable:     true,
		ReferenceTable: "_user",
	},
	{
		Name:           "_updated_by",
		Label:          "Updated By",
		DataType:       "reference",
		IsNullable:     true,
		ReferenceTable: "_user",
	},
	{
		Name:           "_deleted_by",
		Label:          "Deleted By",
		DataType:       "reference",
		IsNullable:     true,
		ReferenceTable: "_user",
	},
}

func RecordSystemColumnDefinitions() []AppDefinitionColumn {
	columns := make([]AppDefinitionColumn, 0, len(recordSystemColumnTemplates))
	for _, column := range recordSystemColumnTemplates {
		copy := column
		copy.Choices = append([]ChoiceOption(nil), column.Choices...)
		columns = append(columns, copy)
	}
	return columns
}

func IsSystemColumnName(name string) bool {
	for _, column := range recordSystemColumnTemplates {
		if column.Name == normalizeIdentifier(name) {
			return true
		}
	}
	return false
}

func recordSystemColumnNameSet() map[string]bool {
	names := make(map[string]bool, len(recordSystemColumnTemplates))
	for _, column := range recordSystemColumnTemplates {
		names[column.Name] = true
	}
	return names
}

func definitionHasExplicitSystemColumns(table AppDefinitionTable) bool {
	for _, column := range table.Columns {
		if IsSystemColumnName(column.Name) {
			return true
		}
	}
	return false
}

func definitionUsesImplicitSystemColumns(appName, namespace string, table AppDefinitionTable) bool {
	namespace = normalizeIdentifier(namespace)
	if namespace == "" {
		if !isOOTBBaseAppName(appName, namespace) {
			return false
		}
		if strings.HasPrefix(normalizeIdentifier(table.Name), "_") {
			return false
		}
		return !definitionHasExplicitSystemColumns(table)
	}
	return !definitionHasExplicitSystemColumns(table)
}

func buildSystemYAMLColumns(appName, tableName string) []Column {
	tableID := yamlTableID(appName, tableName)
	columns := make([]Column, 0, len(recordSystemColumnTemplates))
	for _, template := range recordSystemColumnTemplates {
		columns = append(columns, Column{
			ID:               yamlColumnID(appName, tableName, template.Name),
			NAME:             template.Name,
			CREATED_AT:       time.Time{},
			CREATED_BY:       "",
			UPDATED_AT:       time.Time{},
			UPDATED_BY:       "",
			LABEL:            template.Label,
			DATA_TYPE:        template.DataType,
			IS_NULLABLE:      template.IsNullable,
			DEFAULT_VALUE:    sql.NullString{},
			IS_HIDDEN:        true,
			IS_READONLY:      true,
			VALIDATION_REGEX: sql.NullString{},
			CONDITION_EXPR:   sql.NullString{},
			VALIDATION_MSG:   sql.NullString{},
			REFERENCE_TABLE:  nullableString(template.ReferenceTable),
			CHOICES:          append([]ChoiceOption(nil), template.Choices...),
			TABLE_ID:         tableID,
		})
	}
	return columns
}

func buildSystemBuilderColumns(appName, tableName string) []BuilderColumnSummary {
	columns := make([]BuilderColumnSummary, 0, len(recordSystemColumnTemplates))
	for _, template := range recordSystemColumnTemplates {
		columns = append(columns, BuilderColumnSummary{
			ID:             yamlColumnID(appName, tableName, template.Name),
			Name:           template.Name,
			Label:          template.Label,
			DataType:       template.DataType,
			IsNullable:     template.IsNullable,
			DefaultValue:   "",
			ValidationRule: "",
		})
	}
	return columns
}

func normalizeIdentifier(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
