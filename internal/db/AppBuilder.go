package db

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

type BuilderTableSummary struct {
	ID            string
	Name          string
	LabelSingular string
	LabelPlural   string
	Description   string
	ColumnCount   int
}

type BuilderColumnSummary struct {
	ID                string
	Name              string
	Label             string
	DataType          string
	IsNullable        bool
	DefaultValue      string
	ValidationRule    string
	ValidationExpr    string
	ConditionExpr     string
	ValidationMessage string
	ReferenceTable    string
	Prefix            string
	Choices           []ChoiceOption
}

func GetBuilderTable(ctx context.Context, tableName string) (BuilderTableSummary, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if !IsSafeIdentifier(tableName) {
		return BuilderTableSummary{}, fmt.Errorf("invalid table name")
	}
	if isProtectedTableName(tableName) {
		return BuilderTableSummary{}, fmt.Errorf("table is protected")
	}

	if app, table, ok, err := FindYAMLTableByName(ctx, tableName); err != nil {
		return BuilderTableSummary{}, err
	} else if ok {
		return BuildYAMLBuilderTableWithContext(ctx, app, table), nil
	}
	return buildPhysicalBuilderTable(ctx, tableName)
}

func GetBuilderTableRecord(ctx context.Context, tableName string) (BuilderTableSummary, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if !IsSafeIdentifier(tableName) {
		return BuilderTableSummary{}, fmt.Errorf("invalid table name")
	}
	if app, table, ok, err := FindYAMLTableByName(ctx, tableName); err != nil {
		return BuilderTableSummary{}, err
	} else if ok {
		return BuildYAMLBuilderTableWithContext(ctx, app, table), nil
	}
	return buildPhysicalBuilderTable(ctx, tableName)
}

func ListBuilderTables(ctx context.Context) ([]BuilderTableSummary, error) {
	yamlApps, err := ListActiveApps(ctx)
	if err != nil {
		return nil, err
	}

	yamlTables := make(map[string]BuilderTableSummary)
	for _, app := range yamlApps {
		if app.Definition == nil {
			continue
		}
		for _, table := range app.Definition.Tables {
			if isProtectedTableName(table.Name) {
				continue
			}
			yamlTables[table.Name] = BuildYAMLBuilderTableWithContext(ctx, app, table)
		}
	}

	tables := make([]BuilderTableSummary, 0, 32)
	physicalTables, err := ListPhysicalBaseTables(ctx)
	if err != nil {
		return nil, err
	}
	for _, tableName := range physicalTables {
		if isProtectedTableName(tableName) {
			continue
		}
		if _, shadowed := yamlTables[tableName]; shadowed {
			continue
		}
		item, err := buildPhysicalBuilderTable(ctx, tableName)
		if err != nil {
			return nil, err
		}
		tables = append(tables, item)
	}

	for _, table := range yamlTables {
		tables = append(tables, table)
	}

	sort.Slice(tables, func(i, j int) bool {
		return tables[i].Name < tables[j].Name
	})
	return tables, nil
}

func ListBuilderColumns(ctx context.Context, tableName string) ([]BuilderColumnSummary, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if !IsSafeIdentifier(tableName) {
		return nil, fmt.Errorf("invalid table name")
	}
	if isProtectedTableName(tableName) {
		return nil, fmt.Errorf("table is protected")
	}

	if app, table, ok, err := FindYAMLTableByName(ctx, tableName); err != nil {
		return nil, err
	} else if ok {
		return BuildYAMLBuilderColumnsWithContext(ctx, app, table), nil
	}
	return buildPhysicalBuilderColumns(ctx, tableName)
}

func ListBuilderColumnRecords(ctx context.Context, tableName string) ([]BuilderColumnSummary, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if !IsSafeIdentifier(tableName) {
		return nil, fmt.Errorf("invalid table name")
	}
	if app, table, ok, err := FindYAMLTableByName(ctx, tableName); err != nil {
		return nil, err
	} else if ok {
		return BuildYAMLBuilderColumnsWithContext(ctx, app, table), nil
	}
	return buildPhysicalBuilderColumns(ctx, tableName)
}

func CreateAppTable(ctx context.Context, appName, name, labelSingular, labelPlural, description, userID string) error {
	appName = strings.TrimSpace(strings.ToLower(appName))
	name = strings.TrimSpace(strings.ToLower(name))
	if err := validateBuilderTableName(name); err != nil {
		return err
	}
	if isProtectedTableName(name) {
		return fmt.Errorf("table name %q is reserved", name)
	}

	if labelSingular = strings.TrimSpace(labelSingular); labelSingular == "" {
		labelSingular = humanizeIdentifier(name)
	}
	if labelPlural = strings.TrimSpace(labelPlural); labelPlural == "" {
		labelPlural = labelSingular + "s"
	}
	description = strings.TrimSpace(description)

	app, err := GetActiveAppByName(ctx, appName)
	if err != nil {
		return err
	}
	if err := validateAppDefinitionTableName(app.Name, app.Namespace, name); err != nil {
		return err
	}
	definition := app.Definition
	if definition == nil {
		definition = &AppDefinition{
			Name:        app.Name,
			Namespace:   app.Namespace,
			Label:       app.Label,
			Description: app.Description,
		}
	}
	for _, table := range definition.Tables {
		if table.Name == name {
			return fmt.Errorf("table %q already exists", name)
		}
	}

	quotedTable, err := QuoteIdentifier(name)
	if err != nil {
		return fmt.Errorf("invalid table name")
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin create table: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
		return fmt.Errorf("check existing table: %w", err)
	}
	if exists {
		return fmt.Errorf("table %q already exists", name)
	}

	if _, err := tx.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto`); err != nil {
		return fmt.Errorf("ensure pgcrypto: %w", err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			_update_count BIGINT NOT NULL DEFAULT 0,
				_deleted_at TIMESTAMPTZ,
				_created_by UUID,
				_updated_by UUID,
				_deleted_by UUID
			)
		`, quotedTable)); err != nil {
		return fmt.Errorf("create physical table: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT _ensure_record_version_trigger($1)`, name); err != nil {
		return fmt.Errorf("ensure record version trigger: %w", err)
	}

	definition.Tables = append(definition.Tables, AppDefinitionTable{
		Name:          name,
		LabelSingular: labelSingular,
		LabelPlural:   labelPlural,
		Description:   description,
		Columns:       []AppDefinitionColumn{},
	})
	if err := saveAppDefinitionTx(ctx, tx, app.Name, definition); err != nil {
		return err
	}
	if err := recordBuilderSchemaChange(ctx, tx, "table_create", name, "", nil, map[string]any{
		"app_name":       app.Name,
		"name":           name,
		"label_singular": labelSingular,
		"label_plural":   labelPlural,
		"description":    description,
	}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit create table: %w", err)
	}

	return nil
}

func CreateAppColumn(ctx context.Context, tableName, name, label, dataType, defaultValue, prefix, validationRegex, validationExpr, conditionExpr, validationMessage, referenceTable string, choices []ChoiceOption, userID string, isNullable bool) error {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	name = strings.TrimSpace(strings.ToLower(name))
	label = strings.TrimSpace(label)
	dataType = normalizeDataType(dataType)
	defaultValue = strings.TrimSpace(defaultValue)
	prefix = normalizeAutoNumberPrefix(prefix)
	validationRegex = strings.TrimSpace(validationRegex)
	validationExpr = strings.TrimSpace(validationExpr)
	conditionExpr = strings.TrimSpace(conditionExpr)
	validationMessage = strings.TrimSpace(validationMessage)
	referenceTable = strings.TrimSpace(strings.ToLower(referenceTable))

	if !IsSafeIdentifier(tableName) {
		return fmt.Errorf("invalid table name")
	}
	if isProtectedTableName(tableName) {
		return fmt.Errorf("table %q is protected", tableName)
	}
	if err := validateBuilderColumnName(name); err != nil {
		return err
	}
	if err := validateBuilderColumnDataType(dataType); err != nil {
		return err
	}
	if label == "" {
		label = humanizeIdentifier(name)
	}
	isAutoNumber := IsAutoNumberDataType(dataType)

	sqlType, err := builderDataTypeToSQL(dataType)
	if err != nil {
		return err
	}
	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name")
	}
	quotedColumn, err := QuoteIdentifier(name)
	if err != nil {
		return fmt.Errorf("invalid column name")
	}

	app, definition, tableIndex, err := loadOwnedDefinitionTable(ctx, tableName)
	if err != nil {
		return err
	}
	for _, column := range definition.Tables[tableIndex].Columns {
		if column.Name == name {
			return fmt.Errorf("column %q already exists on %q", name, tableName)
		}
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin create column: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if !isNullable && !isAutoNumber {
		var rowCount int
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(1) FROM %s`, quotedTable)).Scan(&rowCount); err != nil {
			return fmt.Errorf("check table row count: %w", err)
		}
		if rowCount > 0 {
			return fmt.Errorf("cannot add NOT NULL column to non-empty table without a default")
		}
	}

	nullClause := ""
	if !isNullable && !isAutoNumber {
		nullClause = " NOT NULL"
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s%s`, quotedTable, quotedColumn, sqlType, nullClause)); err != nil {
		return fmt.Errorf("add physical column: %w", err)
	}

	definition.Tables[tableIndex].Columns = append(definition.Tables[tableIndex].Columns, AppDefinitionColumn{
		Name:              name,
		Label:             label,
		DataType:          dataType,
		IsNullable:        isNullable,
		Prefix:            prefix,
		ReferenceTable:    referenceTable,
		Choices:           append([]ChoiceOption(nil), choices...),
		DefaultValue:      defaultValue,
		ValidationRegex:   validationRegex,
		ValidationExpr:    validationExpr,
		ConditionExpr:     conditionExpr,
		ValidationMessage: validationMessage,
	})
	if err := saveAppDefinitionTx(ctx, tx, app.Name, definition); err != nil {
		return err
	}
	resolvedTable, err := resolveDraftTableForAutoNumber(ctx, app, definition, definition.Tables[tableIndex])
	if err != nil {
		return err
	}
	if err := applyTableAutoNumberStateTx(ctx, tx, resolvedAutoNumberScope(app.Name, app.Namespace), resolvedTable); err != nil {
		return err
	}
	if !isNullable && isAutoNumber {
		var nullCount int
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(1) FROM %s WHERE %s IS NULL`, quotedTable, quotedColumn)).Scan(&nullCount); err != nil {
			return fmt.Errorf("check autnumber NULL values: %w", err)
		}
		if nullCount > 0 {
			return fmt.Errorf("cannot set NOT NULL: existing rows contain NULL values")
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s SET NOT NULL`, quotedTable, quotedColumn)); err != nil {
			return fmt.Errorf("set autnumber NOT NULL: %w", err)
		}
	}
	if err := recordBuilderSchemaChange(ctx, tx, "column_create", tableName, name, nil, map[string]any{
		"app_name":         app.Name,
		"name":             name,
		"label":            label,
		"data_type":        dataType,
		"is_nullable":      isNullable,
		"prefix":           prefix,
		"reference_table":  referenceTable,
		"choices":          choices,
		"default_value":    defaultValue,
		"validation_regex": validationRegex,
		"validation_expr":  validationExpr,
		"condition_expr":   conditionExpr,
		"validation_msg":   validationMessage,
	}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit create column: %w", err)
	}
	return nil
}

func UpdateAppTable(ctx context.Context, tableName, labelSingular, labelPlural, description, userID string) error {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if !IsSafeIdentifier(tableName) {
		return fmt.Errorf("invalid table name")
	}
	if isProtectedTableName(tableName) {
		return fmt.Errorf("table %q is protected", tableName)
	}

	labelSingular = strings.TrimSpace(labelSingular)
	labelPlural = strings.TrimSpace(labelPlural)
	description = strings.TrimSpace(description)
	if labelSingular == "" {
		labelSingular = humanizeIdentifier(tableName)
	}
	if labelPlural == "" {
		labelPlural = labelSingular + "s"
	}

	before, err := GetBuilderTable(ctx, tableName)
	if err != nil {
		return err
	}

	app, definition, tableIndex, err := loadOwnedDefinitionTable(ctx, tableName)
	if err != nil {
		return err
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin update table metadata: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	definition.Tables[tableIndex].LabelSingular = labelSingular
	definition.Tables[tableIndex].LabelPlural = labelPlural
	definition.Tables[tableIndex].Description = description
	if err := saveAppDefinitionTx(ctx, tx, app.Name, definition); err != nil {
		return err
	}
	if err := recordBuilderSchemaChange(ctx, tx, "table_update", tableName, "", map[string]any{
		"app_name":       app.Name,
		"name":           before.Name,
		"label_singular": before.LabelSingular,
		"label_plural":   before.LabelPlural,
		"description":    before.Description,
	}, map[string]any{
		"app_name":       app.Name,
		"name":           tableName,
		"label_singular": labelSingular,
		"label_plural":   labelPlural,
		"description":    description,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit update table metadata: %w", err)
	}
	return nil
}

func UpdateAppColumn(ctx context.Context, tableName, columnName, label, defaultValue, prefix, validationRegex, validationExpr, conditionExpr, validationMessage, referenceTable string, choices []ChoiceOption, userID string, isNullable bool) error {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	columnName = strings.TrimSpace(strings.ToLower(columnName))
	label = strings.TrimSpace(label)
	defaultValue = strings.TrimSpace(defaultValue)
	prefix = normalizeAutoNumberPrefix(prefix)
	validationRegex = strings.TrimSpace(validationRegex)
	validationExpr = strings.TrimSpace(validationExpr)
	conditionExpr = strings.TrimSpace(conditionExpr)
	validationMessage = strings.TrimSpace(validationMessage)
	referenceTable = strings.TrimSpace(strings.ToLower(referenceTable))

	if !IsSafeIdentifier(tableName) || !IsSafeIdentifier(columnName) {
		return fmt.Errorf("invalid table or column name")
	}
	if strings.HasPrefix(columnName, "_") {
		return fmt.Errorf("system columns cannot be edited here")
	}
	if isProtectedTableName(tableName) {
		return fmt.Errorf("table %q is protected", tableName)
	}
	if label == "" {
		label = humanizeIdentifier(columnName)
	}

	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name")
	}
	quotedColumn, err := QuoteIdentifier(columnName)
	if err != nil {
		return fmt.Errorf("invalid column name")
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin update column: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	app, definition, tableIndex, columnIndex, err := loadOwnedDefinitionColumn(ctx, tableName, columnName)
	if err != nil {
		return err
	}
	current := definition.Tables[tableIndex].Columns[columnIndex]
	currentNullable := current.IsNullable
	currentLabel := current.Label
	currentDefault := current.DefaultValue
	currentValidation := current.ValidationRegex
	currentValidationExpr := current.ValidationExpr
	currentConditionExpr := current.ConditionExpr
	currentValidationMessage := current.ValidationMessage
	currentDataType := current.DataType
	currentPrefix := current.Prefix
	currentReferenceTable := current.ReferenceTable
	currentChoices := append([]ChoiceOption(nil), current.Choices...)
	deferSetNotNull := false
	if BaseDataType(currentDataType) == "reference" && referenceTable == "" && currentReferenceTable != "" {
		referenceTable = currentReferenceTable
	}
	if BaseDataType(currentDataType) == "choice" && len(choices) == 0 && len(currentChoices) > 0 {
		choices = append([]ChoiceOption(nil), currentChoices...)
	}
	if IsAutoNumberDataType(currentDataType) && prefix == "" && currentPrefix != "" {
		prefix = currentPrefix
	}

	if currentNullable != isNullable {
		if isNullable {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL`, quotedTable, quotedColumn)); err != nil {
				return fmt.Errorf("update nullability: %w", err)
			}
		} else {
			if IsAutoNumberDataType(currentDataType) {
				deferSetNotNull = true
			} else {
				var nullCount int
				if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(1) FROM %s WHERE %s IS NULL`, quotedTable, quotedColumn)).Scan(&nullCount); err != nil {
					return fmt.Errorf("check null values: %w", err)
				}
				if nullCount > 0 {
					return fmt.Errorf("cannot set NOT NULL: existing rows contain NULL values")
				}
				if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s SET NOT NULL`, quotedTable, quotedColumn)); err != nil {
					return fmt.Errorf("update nullability: %w", err)
				}
			}
		}
	}

	definition.Tables[tableIndex].Columns[columnIndex].Label = label
	definition.Tables[tableIndex].Columns[columnIndex].IsNullable = isNullable
	definition.Tables[tableIndex].Columns[columnIndex].DefaultValue = defaultValue
	definition.Tables[tableIndex].Columns[columnIndex].Prefix = prefix
	definition.Tables[tableIndex].Columns[columnIndex].ValidationRegex = validationRegex
	definition.Tables[tableIndex].Columns[columnIndex].ValidationExpr = validationExpr
	definition.Tables[tableIndex].Columns[columnIndex].ConditionExpr = conditionExpr
	definition.Tables[tableIndex].Columns[columnIndex].ValidationMessage = validationMessage
	definition.Tables[tableIndex].Columns[columnIndex].ReferenceTable = referenceTable
	definition.Tables[tableIndex].Columns[columnIndex].Choices = append([]ChoiceOption(nil), choices...)
	if err := saveAppDefinitionTx(ctx, tx, app.Name, definition); err != nil {
		return err
	}
	resolvedTable, err := resolveDraftTableForAutoNumber(ctx, app, definition, definition.Tables[tableIndex])
	if err != nil {
		return err
	}
	if err := applyTableAutoNumberStateTx(ctx, tx, resolvedAutoNumberScope(app.Name, app.Namespace), resolvedTable); err != nil {
		return err
	}
	if deferSetNotNull {
		var nullCount int
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(1) FROM %s WHERE %s IS NULL`, quotedTable, quotedColumn)).Scan(&nullCount); err != nil {
			return fmt.Errorf("check null values: %w", err)
		}
		if nullCount > 0 {
			return fmt.Errorf("cannot set NOT NULL: existing rows contain NULL values")
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s SET NOT NULL`, quotedTable, quotedColumn)); err != nil {
			return fmt.Errorf("update nullability: %w", err)
		}
	}
	if err := recordBuilderSchemaChange(ctx, tx, "column_update", tableName, columnName, map[string]any{
		"app_name":         app.Name,
		"name":             columnName,
		"label":            currentLabel,
		"data_type":        currentDataType,
		"is_nullable":      currentNullable,
		"default_value":    currentDefault,
		"prefix":           currentPrefix,
		"validation_regex": currentValidation,
		"validation_expr":  currentValidationExpr,
		"condition_expr":   currentConditionExpr,
		"validation_msg":   currentValidationMessage,
		"reference_table":  currentReferenceTable,
		"choices":          currentChoices,
	}, map[string]any{
		"app_name":         app.Name,
		"name":             columnName,
		"label":            label,
		"data_type":        currentDataType,
		"is_nullable":      isNullable,
		"default_value":    defaultValue,
		"prefix":           prefix,
		"validation_regex": validationRegex,
		"validation_expr":  validationExpr,
		"condition_expr":   conditionExpr,
		"validation_msg":   validationMessage,
		"reference_table":  referenceTable,
		"choices":          choices,
	}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit update column: %w", err)
	}
	return nil
}

func DeleteAppTable(ctx context.Context, tableName string) error {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if !IsSafeIdentifier(tableName) {
		return fmt.Errorf("invalid table name")
	}
	if isProtectedTableName(tableName) {
		return fmt.Errorf("table %q is protected", tableName)
	}

	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name")
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete table: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	deps, err := collectTableDeleteDependencies(ctx, tx, tableName)
	if err != nil {
		return err
	}
	if len(deps) > 0 {
		return fmt.Errorf("cannot delete table %q: %s", tableName, strings.Join(deps, "; "))
	}

	tableMeta, err := GetBuilderTable(ctx, tableName)
	if err != nil {
		return err
	}
	app, definition, tableIndex, err := loadOwnedDefinitionTable(ctx, tableName)
	if err != nil {
		return err
	}

	var rowCount int
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(1) FROM %s`, quotedTable)).Scan(&rowCount); err != nil {
		return fmt.Errorf("check table row count: %w", err)
	}
	if rowCount > 0 {
		return fmt.Errorf("cannot delete non-empty table")
	}

	if _, err := tx.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, quotedTable)); err != nil {
		return fmt.Errorf("drop physical table: %w", err)
	}
	definition.Tables = append(definition.Tables[:tableIndex], definition.Tables[tableIndex+1:]...)
	if err := saveAppDefinitionTx(ctx, tx, app.Name, definition); err != nil {
		return err
	}
	if err := recordBuilderSchemaChange(ctx, tx, "table_delete", tableName, "", map[string]any{
		"app_name":       app.Name,
		"name":           tableMeta.Name,
		"label_singular": tableMeta.LabelSingular,
		"label_plural":   tableMeta.LabelPlural,
		"description":    tableMeta.Description,
		"column_count":   tableMeta.ColumnCount,
	}, nil); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete table: %w", err)
	}
	return nil
}

func DeleteAppColumn(ctx context.Context, tableName, columnName string) error {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	columnName = strings.TrimSpace(strings.ToLower(columnName))

	if !IsSafeIdentifier(tableName) || !IsSafeIdentifier(columnName) {
		return fmt.Errorf("invalid table or column name")
	}
	if strings.HasPrefix(columnName, "_") {
		return fmt.Errorf("system columns cannot be removed")
	}
	if isProtectedTableName(tableName) {
		return fmt.Errorf("table %q is protected", tableName)
	}

	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name")
	}
	quotedColumn, err := QuoteIdentifier(columnName)
	if err != nil {
		return fmt.Errorf("invalid column name")
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete column: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	deps, err := collectColumnDeleteDependencies(ctx, tx, tableName, columnName)
	if err != nil {
		return err
	}
	if len(deps) > 0 {
		return fmt.Errorf("cannot delete column %q on %q: %s", columnName, tableName, strings.Join(deps, "; "))
	}

	app, definition, tableIndex, columnIndex, err := loadOwnedDefinitionColumn(ctx, tableName, columnName)
	if err != nil {
		return err
	}
	current := definition.Tables[tableIndex].Columns[columnIndex]
	oldLabel := current.Label
	oldType := current.DataType
	oldNullable := current.IsNullable
	oldDefault := current.DefaultValue
	oldValidation := current.ValidationRegex

	var rowCount int
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(1) FROM %s`, quotedTable)).Scan(&rowCount); err != nil {
		return fmt.Errorf("check table row count: %w", err)
	}
	if rowCount > 0 {
		return fmt.Errorf("cannot drop column from non-empty table")
	}

	if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s DROP COLUMN IF EXISTS %s`, quotedTable, quotedColumn)); err != nil {
		return fmt.Errorf("drop physical column: %w", err)
	}
	definition.Tables[tableIndex].Columns = append(definition.Tables[tableIndex].Columns[:columnIndex], definition.Tables[tableIndex].Columns[columnIndex+1:]...)
	for formIndex := range definition.Tables[tableIndex].Forms {
		fields := definition.Tables[tableIndex].Forms[formIndex].Fields[:0]
		for _, field := range definition.Tables[tableIndex].Forms[formIndex].Fields {
			if field != columnName {
				fields = append(fields, field)
			}
		}
		definition.Tables[tableIndex].Forms[formIndex].Fields = fields
	}
	if err := saveAppDefinitionTx(ctx, tx, app.Name, definition); err != nil {
		return err
	}
	resolvedTable, err := resolveDraftTableForAutoNumber(ctx, app, definition, definition.Tables[tableIndex])
	if err != nil {
		return err
	}
	if err := applyTableAutoNumberStateTx(ctx, tx, resolvedAutoNumberScope(app.Name, app.Namespace), resolvedTable); err != nil {
		return err
	}
	if err := recordBuilderSchemaChange(ctx, tx, "column_delete", tableName, columnName, map[string]any{
		"app_name":         app.Name,
		"name":             columnName,
		"label":            oldLabel,
		"data_type":        oldType,
		"is_nullable":      oldNullable,
		"default_value":    oldDefault,
		"validation_regex": oldValidation,
	}, nil); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete column: %w", err)
	}
	return nil
}

func builderDataTypeToSQL(dataType string) (string, error) {
	dataType = normalizeDataType(dataType)
	switch BaseDataType(dataType) {
	case "text":
		return "TEXT", nil
	case "long_text", "richtext", "markdown", "email", "url", "phone", "choice", "code", "autnumber":
		return "TEXT", nil
	case "int", "integer":
		return "INTEGER", nil
	case "bigint", "bigserial", "serial":
		return "BIGINT", nil
	case "float", "double":
		return "DOUBLE PRECISION", nil
	case "decimal", "numeric":
		return "NUMERIC", nil
	case "bool", "boolean":
		return "BOOLEAN", nil
	case "date":
		return "DATE", nil
	case "timestamp", "timestamptz", "datetime":
		return "TIMESTAMPTZ", nil
	case "uuid", "reference":
		return "UUID", nil
	case "json", "jsonb":
		return "JSONB", nil
	}
	if varcharTypeRegex.MatchString(dataType) {
		return strings.ToUpper(dataType), nil
	}
	if enumTypeRegex.MatchString(dataType) {
		return "TEXT", nil
	}
	return "", fmt.Errorf("unsupported data_type %q", dataType)
}

func humanizeIdentifier(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return ""
	}
	parts := strings.Split(v, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func resolveDraftTableForAutoNumber(ctx context.Context, app RegisteredApp, definition *AppDefinition, table AppDefinitionTable) (AppDefinitionTable, error) {
	ownerApp := app
	ownerApp.DraftDefinition = definition

	apps, err := ListActiveApps(ctx)
	if err != nil {
		return AppDefinitionTable{}, err
	}
	apps = mergeRegisteredApps(apps, ownerApp)

	resolvedColumns, err := resolveDefinitionColumnsWithApps(apps, ownerApp, table, map[string]bool{})
	if err != nil {
		return AppDefinitionTable{}, err
	}

	resolvedTable := table
	resolvedTable.Columns = resolvedColumns
	return resolvedTable, nil
}

func collectTableDeleteDependencies(ctx context.Context, tx pgx.Tx, tableName string) ([]string, error) {
	deps := []string{}

	scriptDeps, err := CountYAMLTableScriptDependencies(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("check yaml script dependencies: %w", err)
	}
	if scriptDeps > 0 {
		deps = append(deps, fmt.Sprintf("%d yaml script(s)", scriptDeps))
	}

	if exists, err := relationExists(ctx, tx, "_saved_view"); err != nil {
		return nil, err
	} else if exists {
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(1) FROM _saved_view WHERE table_name = $1`, tableName).Scan(&n); err != nil {
			return nil, fmt.Errorf("check saved views: %w", err)
		}
		if n > 0 {
			deps = append(deps, fmt.Sprintf("%d saved view(s)", n))
		}
	}

	if exists, err := relationExists(ctx, tx, "_user_preference"); err != nil {
		return nil, err
	} else if exists {
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(1)
			FROM _user_preference
			WHERE namespace = 'list_view'
			  AND split_part(pref_key, ':', 2) = $1
		`, tableName).Scan(&n); err != nil {
			return nil, fmt.Errorf("check user preferences: %w", err)
		}
		if n > 0 {
			deps = append(deps, fmt.Sprintf("%d list-view preference(s)", n))
		}
	}

	var fkRefs int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(1)
		FROM pg_constraint c
		WHERE c.contype = 'f'
		  AND c.confrelid = to_regclass($1)
	`, tableName).Scan(&fkRefs); err != nil {
		return nil, fmt.Errorf("check foreign key references: %w", err)
	}
	if fkRefs > 0 {
		deps = append(deps, fmt.Sprintf("%d foreign-key reference(s)", fkRefs))
	}

	return deps, nil
}

func collectColumnDeleteDependencies(ctx context.Context, tx pgx.Tx, tableName, columnName string) ([]string, error) {
	deps := []string{}

	if exists, err := relationExists(ctx, tx, "_saved_view"); err != nil {
		return nil, err
	} else if exists {
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(1)
			FROM _saved_view
			WHERE table_name = $1
			  AND state::text ILIKE $2
		`, tableName, "%\""+columnName+"\"%").Scan(&n); err != nil {
			return nil, fmt.Errorf("check saved view column usage: %w", err)
		}
		if n > 0 {
			deps = append(deps, fmt.Sprintf("%d saved view(s) reference this column", n))
		}
	}

	if exists, err := relationExists(ctx, tx, "_user_preference"); err != nil {
		return nil, err
	} else if exists {
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(1)
			FROM _user_preference
			WHERE namespace = 'list_view'
			  AND split_part(pref_key, ':', 2) = $1
			  AND pref_value::text ILIKE $2
		`, tableName, "%\""+columnName+"\"%").Scan(&n); err != nil {
			return nil, fmt.Errorf("check list-view column usage: %w", err)
		}
		if n > 0 {
			deps = append(deps, fmt.Sprintf("%d list-view preference(s) reference this column", n))
		}
	}

	scriptDeps, err := CountYAMLColumnConditionDependencies(ctx, tableName, columnName)
	if err != nil {
		return nil, fmt.Errorf("check yaml script conditions: %w", err)
	}
	if scriptDeps > 0 {
		deps = append(deps, fmt.Sprintf("%d yaml script condition(s) mention this column", scriptDeps))
	}

	var fkRefs int
	if err := tx.QueryRow(ctx, `
		WITH target AS (
			SELECT a.attnum
			FROM pg_attribute a
			WHERE a.attrelid = to_regclass($1)
			  AND a.attname = $2
			  AND a.attisdropped = false
			  AND a.attnum > 0
		)
		SELECT COUNT(1)
		FROM pg_constraint c
		WHERE c.contype = 'f'
		  AND c.confrelid = to_regclass($1)
		  AND EXISTS (
				SELECT 1
				FROM target t
				WHERE t.attnum = ANY(c.confkey)
		  )
	`, tableName, columnName).Scan(&fkRefs); err != nil {
		return nil, fmt.Errorf("check foreign key column references: %w", err)
	}
	if fkRefs > 0 {
		deps = append(deps, fmt.Sprintf("%d foreign-key reference(s)", fkRefs))
	}

	return deps, nil
}

func relationExists(ctx context.Context, tx pgx.Tx, relName string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, relName).Scan(&exists); err != nil {
		return false, fmt.Errorf("check relation existence for %s: %w", relName, err)
	}
	return exists, nil
}

func saveAppDefinitionTx(ctx context.Context, tx pgx.Tx, appName string, definition *AppDefinition) error {
	if definition == nil {
		return fmt.Errorf("definition is required")
	}
	app, err := GetActiveAppByName(ctx, appName)
	if err != nil {
		return err
	}
	if err := prepareDefinitionForApp(app, definition); err != nil {
		return err
	}
	if err := validateAppDefinitionForApp(ctx, app, definition); err != nil {
		return err
	}

	content, err := yaml.Marshal(definition)
	if err != nil {
		return fmt.Errorf("marshal app definition yaml: %w", err)
	}

	commandTag, err := tx.Exec(ctx, `
		UPDATE _app
		SET definition_yaml = $2,
			published_definition_yaml = $2,
			definition_version = GREATEST(definition_version, published_version) + 1,
			published_version = GREATEST(definition_version, published_version) + 1,
			_updated_at = NOW()
		WHERE name = $1 OR namespace = $1
	`, app.Name, string(content))
	if err != nil {
		return fmt.Errorf("save app definition yaml: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}
	return nil
}

func loadOwnedDefinitionTable(ctx context.Context, tableName string) (RegisteredApp, *AppDefinition, int, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	app, table, ok, err := FindYAMLTableByName(ctx, tableName)
	if err != nil {
		return RegisteredApp{}, nil, -1, err
	}
	if !ok || app.Definition == nil {
		return RegisteredApp{}, nil, -1, fmt.Errorf("table %q is not owned by an app yaml definition", tableName)
	}
	for i := range app.Definition.Tables {
		if app.Definition.Tables[i].Name == table.Name {
			return app, app.Definition, i, nil
		}
	}
	return RegisteredApp{}, nil, -1, fmt.Errorf("table %q is not owned by an app yaml definition", tableName)
}

func loadOwnedDefinitionColumn(ctx context.Context, tableName, columnName string) (RegisteredApp, *AppDefinition, int, int, error) {
	app, definition, tableIndex, err := loadOwnedDefinitionTable(ctx, tableName)
	if err != nil {
		return RegisteredApp{}, nil, -1, -1, err
	}
	columnName = strings.TrimSpace(strings.ToLower(columnName))
	for i := range definition.Tables[tableIndex].Columns {
		if definition.Tables[tableIndex].Columns[i].Name == columnName {
			return app, definition, tableIndex, i, nil
		}
	}
	return RegisteredApp{}, nil, -1, -1, fmt.Errorf("column %q not found on %q", columnName, tableName)
}

func recordBuilderSchemaChange(ctx context.Context, tx pgx.Tx, changeType, tableName, columnName string, beforeState, afterState map[string]any) error {
	if exists, err := relationExists(ctx, tx, "_builder_schema_change"); err != nil {
		return err
	} else if !exists {
		return nil
	}

	var beforeJSON []byte
	var afterJSON []byte
	var beforeValue any
	var afterValue any
	var err error
	if beforeState != nil {
		beforeJSON, err = json.Marshal(beforeState)
		if err != nil {
			return fmt.Errorf("marshal before state: %w", err)
		}
		beforeValue = string(beforeJSON)
	}
	if afterState != nil {
		afterJSON, err = json.Marshal(afterState)
		if err != nil {
			return fmt.Errorf("marshal after state: %w", err)
		}
		afterValue = string(afterJSON)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO _builder_schema_change (
			change_type,
			table_name,
			column_name,
			before_state,
			after_state
		) VALUES (
			$1,
			$2,
			NULLIF($3, ''),
			$4::jsonb,
			$5::jsonb
		)
	`, changeType, tableName, columnName, beforeValue, afterValue); err != nil {
		return fmt.Errorf("record schema change: %w", err)
	}
	return nil
}
