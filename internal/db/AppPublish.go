package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func applyDefinitionSchemaTx(ctx context.Context, tx pgx.Tx, definition *AppDefinition) error {
	if definition == nil {
		return fmt.Errorf("definition is required")
	}
	if !definitionOwnsPhysicalSchema(definition) {
		return nil
	}
	if _, err := tx.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto`); err != nil {
		return fmt.Errorf("ensure pgcrypto: %w", err)
	}

	apps, err := ListActiveApps(ctx)
	if err != nil {
		return fmt.Errorf("list active apps: %w", err)
	}
	currentApp := RegisteredApp{
		Name:        strings.TrimSpace(strings.ToLower(definition.Name)),
		Namespace:   strings.TrimSpace(strings.ToLower(definition.Namespace)),
		Label:       strings.TrimSpace(definition.Label),
		Description: strings.TrimSpace(definition.Description),
		Definition:  definition,
	}
	apps = mergeRegisteredApps(apps, currentApp)

	scope := resolvedAutoNumberScope(definition.Name, definition.Namespace)
	for _, table := range definition.Tables {
		resolvedTable := table
		resolvedColumns, err := resolveDefinitionColumnsWithApps(apps, currentApp, table, map[string]bool{})
		if err != nil {
			return fmt.Errorf("resolve inherited columns for %q: %w", table.Name, err)
		}
		resolvedTable.Columns = resolvedColumns
		if err := ensureDefinitionTableSchemaTx(ctx, tx, scope, currentApp, apps, resolvedTable); err != nil {
			return err
		}
	}
	return nil
}

func reconcileDefinitionSchemaTx(ctx context.Context, tx pgx.Tx, _ RegisteredApp, publishedDefinition, nextDefinition *AppDefinition) error {
	if publishedDefinition == nil || !definitionOwnsPhysicalSchema(publishedDefinition) {
		return nil
	}

	nextTables := map[string]AppDefinitionTable{}
	if nextDefinition != nil {
		for _, table := range nextDefinition.Tables {
			nextTables[strings.TrimSpace(strings.ToLower(table.Name))] = table
		}
	}

	for _, publishedTable := range publishedDefinition.Tables {
		tableName := strings.TrimSpace(strings.ToLower(publishedTable.Name))
		if tableName == "" {
			continue
		}

		nextTable, exists := nextTables[tableName]
		if !exists {
			if err := dropDefinitionTableTx(ctx, tx, tableName); err != nil {
				return err
			}
			continue
		}

		if err := reconcileDefinitionColumnsTx(ctx, tx, tableName, publishedTable, nextTable); err != nil {
			return err
		}
	}

	return nil
}

func ensureDefinitionTableSchemaTx(ctx context.Context, tx pgx.Tx, scope string, ownerApp RegisteredApp, apps []RegisteredApp, table AppDefinitionTable) error {
	tableName := strings.TrimSpace(strings.ToLower(table.Name))
	if tableName == "" {
		return fmt.Errorf("table name is required")
	}
	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name %q", tableName)
	}

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tableName).Scan(&exists); err != nil {
		return fmt.Errorf("check physical table %q: %w", tableName, err)
	}
	if !exists {
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
			return fmt.Errorf("create physical table %q: %w", tableName, err)
		}
	}
	if err := ensureRecordVersionTriggerIfAvailableTx(ctx, tx, tableName); err != nil {
		return fmt.Errorf("ensure record version trigger for %q: %w", tableName, err)
	}

	physicalColumns, err := loadPhysicalColumnsTx(ctx, tx, tableName)
	if err != nil {
		return err
	}

	rowCount := -1
	for _, column := range table.Columns {
		columnName := strings.TrimSpace(strings.ToLower(column.Name))
		isAutoNumber := IsAutoNumberDataType(column.DataType)
		sqlType, err := builderDataTypeToSQL(normalizeDataType(column.DataType))
		if err != nil {
			return fmt.Errorf("table %q column %q: %w", tableName, columnName, err)
		}
		quotedColumn, err := QuoteIdentifier(columnName)
		if err != nil {
			return fmt.Errorf("invalid column name %q on %q", columnName, tableName)
		}

		physical, exists := physicalColumns[columnName]
		if !exists {
			if !column.IsNullable && !isAutoNumber {
				if rowCount < 0 {
					if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(1) FROM %s`, quotedTable)).Scan(&rowCount); err != nil {
						return fmt.Errorf("check table row count for %q: %w", tableName, err)
					}
				}
				if rowCount > 0 {
					return fmt.Errorf("cannot publish NOT NULL column %q on non-empty table %q", columnName, tableName)
				}
			}

			nullClause := ""
			if !column.IsNullable && !isAutoNumber {
				nullClause = " NOT NULL"
			}
			if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s%s`, quotedTable, quotedColumn, sqlType, nullClause)); err != nil {
				return fmt.Errorf("add physical column %q on %q: %w", columnName, tableName, err)
			}
			continue
		}

		expectedType := normalizeSQLType(sqlType)
		currentType := normalizeSQLType(physical.SQLType)
		if expectedType != currentType {
			return fmt.Errorf("type drift on %s.%s: expected %s, found %s", tableName, columnName, expectedType, currentType)
		}
	}

	if err := applyTableAutoNumberStateTx(ctx, tx, scope, table); err != nil {
		return fmt.Errorf("apply autnumber state on %q: %w", tableName, err)
	}
	if err := applyInheritedBaseTriggersTx(ctx, tx, ownerApp, apps, table); err != nil {
		return fmt.Errorf("apply inherited base triggers on %q: %w", tableName, err)
	}
	if err := applyDefinitionColumnDefaultsTx(ctx, tx, ownerApp, apps, table); err != nil {
		return fmt.Errorf("apply column defaults on %q: %w", tableName, err)
	}

	physicalColumns, err = loadPhysicalColumnsTx(ctx, tx, tableName)
	if err != nil {
		return err
	}
	for _, column := range table.Columns {
		columnName := strings.TrimSpace(strings.ToLower(column.Name))
		quotedColumn, err := QuoteIdentifier(columnName)
		if err != nil {
			return fmt.Errorf("invalid column name %q on %q", columnName, tableName)
		}
		physical, exists := physicalColumns[columnName]
		if !exists || column.IsNullable == physical.IsNullable {
			continue
		}
		if column.IsNullable {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL`, quotedTable, quotedColumn)); err != nil {
				return fmt.Errorf("drop NOT NULL on %s.%s: %w", tableName, columnName, err)
			}
			continue
		}

		var nullCount int
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(1) FROM %s WHERE %s IS NULL`, quotedTable, quotedColumn)).Scan(&nullCount); err != nil {
			return fmt.Errorf("check NULL values on %s.%s: %w", tableName, columnName, err)
		}
		if nullCount > 0 {
			return fmt.Errorf("cannot set NOT NULL on %s.%s: existing rows contain NULL values", tableName, columnName)
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s SET NOT NULL`, quotedTable, quotedColumn)); err != nil {
			return fmt.Errorf("set NOT NULL on %s.%s: %w", tableName, columnName, err)
		}
	}
	return nil
}

func ensureRecordVersionTriggerIfAvailableTx(ctx context.Context, tx pgx.Tx, tableName string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT to_regprocedure('_ensure_record_version_trigger(text)') IS NOT NULL`).Scan(&exists); err != nil {
		return fmt.Errorf("check record version trigger helper: %w", err)
	}
	if !exists {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT _ensure_record_version_trigger($1)`, tableName); err != nil {
		return err
	}
	return nil
}

func applyDefinitionColumnDefaultsTx(ctx context.Context, tx pgx.Tx, ownerApp RegisteredApp, apps []RegisteredApp, table AppDefinitionTable) error {
	tableName := strings.TrimSpace(strings.ToLower(table.Name))
	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name %q", tableName)
	}
	extendsBaseTask := definitionTableExtendsTargetWithApps(apps, ownerApp, table, "base_task", map[string]bool{})

	for _, column := range table.Columns {
		columnName := strings.TrimSpace(strings.ToLower(column.Name))
		quotedColumn, err := QuoteIdentifier(columnName)
		if err != nil {
			return fmt.Errorf("invalid column name %q on %q", columnName, tableName)
		}

		defaultSQL, hasDefault, err := definitionColumnDefaultSQL(tableName, extendsBaseTask, column)
		if err != nil {
			return err
		}
		if !hasDefault {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT`, quotedTable, quotedColumn)); err != nil {
				return fmt.Errorf("drop default on %s.%s: %w", tableName, columnName, err)
			}
			continue
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s`, quotedTable, quotedColumn, defaultSQL)); err != nil {
			return fmt.Errorf("set default on %s.%s: %w", tableName, columnName, err)
		}
	}
	return nil
}

func applyInheritedBaseTriggersTx(ctx context.Context, tx pgx.Tx, ownerApp RegisteredApp, apps []RegisteredApp, table AppDefinitionTable) error {
	if !definitionTableExtendsTargetWithApps(apps, ownerApp, table, "base_task", map[string]bool{}) {
		return nil
	}
	if definitionTableExtendsTargetWithApps(apps, ownerApp, table, "base_task", map[string]bool{}) {
		if err := ensureInheritedBeforeWriteTriggerTx(ctx, tx, table.Name, "trg_base_task_before_write", "base_task_before_write()"); err != nil {
			return err
		}
	}
	return nil
}

func ensureInheritedBeforeWriteTriggerTx(ctx context.Context, tx pgx.Tx, tableName, triggerName, functionCall string) error {
	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name %q", tableName)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, triggerName, quotedTable)); err != nil {
		return fmt.Errorf("drop trigger %q on %q: %w", triggerName, tableName, err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION %s`, triggerName, quotedTable, functionCall)); err != nil {
		return fmt.Errorf("create trigger %q on %q: %w", triggerName, tableName, err)
	}
	return nil
}

func definitionColumnDefaultSQL(tableName string, extendsBaseTask bool, column AppDefinitionColumn) (string, bool, error) {
	if IsAutoNumberDataType(column.DataType) {
		return "", false, nil
	}
	if extendsBaseTask && tableName != "base_task" && strings.TrimSpace(strings.ToLower(column.Name)) == "work_type" {
		return quoteSQLLiteral(derivedTaskTypeFromTableName(tableName)), true, nil
	}
	if defaultSQL, ok := systemColumnDefaultSQL(column.Name); ok && strings.TrimSpace(column.DefaultValue) == "" {
		return defaultSQL, true, nil
	}
	if strings.TrimSpace(column.DefaultValue) == "" {
		return "", false, nil
	}
	return quoteSQLLiteral(strings.TrimSpace(column.DefaultValue)) + scriptColumnCast(column.DataType), true, nil
}

func systemColumnDefaultSQL(columnName string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(columnName)) {
	case "_id":
		return "gen_random_uuid()", true
	case "_created_at", "_updated_at":
		return "NOW()", true
	case "_update_count":
		return "0", true
	default:
		return "", false
	}
}

func derivedTaskTypeFromTableName(tableName string) string {
	name := strings.TrimSpace(strings.ToLower(tableName))
	if name == "" {
		return "TASK"
	}
	if idx := strings.Index(name, "_"); idx >= 0 && idx < len(name)-1 {
		name = name[idx+1:]
	}
	name = strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(name)), " ")
	name = strings.TrimSpace(name)
	if name == "" {
		return "TASK"
	}
	return strings.ToUpper(name)
}

func definitionTableExtendsTargetWithApps(apps []RegisteredApp, ownerApp RegisteredApp, table AppDefinitionTable, target string, visited map[string]bool) bool {
	tableName := strings.TrimSpace(strings.ToLower(table.Name))
	target = strings.TrimSpace(strings.ToLower(target))
	if tableName == target {
		return true
	}
	if tableName == "" {
		return false
	}
	key := strings.TrimSpace(strings.ToLower(ownerApp.Name)) + ":" + tableName
	if visited[key] {
		return false
	}
	visited[key] = true
	defer delete(visited, key)

	if strings.TrimSpace(table.Extends) == "" || ownerApp.Definition == nil {
		return false
	}

	dependencyApps := dependencyAppsForRegisteredApp(apps, ownerApp)
	parentApp, parent, ok := resolveValidationTable(ownerApp, ownerApp.Definition, dependencyApps, table.Extends)
	if !ok {
		return false
	}
	return definitionTableExtendsTargetWithApps(apps, parentApp, parent, target, visited)
}

func reconcileDefinitionColumnsTx(ctx context.Context, tx pgx.Tx, tableName string, publishedTable, nextTable AppDefinitionTable) error {
	nextColumns := map[string]bool{}
	for _, column := range nextTable.Columns {
		nextColumns[strings.TrimSpace(strings.ToLower(column.Name))] = true
	}

	for _, column := range publishedTable.Columns {
		columnName := strings.TrimSpace(strings.ToLower(column.Name))
		if columnName == "" || IsSystemColumnName(columnName) || nextColumns[columnName] {
			continue
		}
		if err := dropDefinitionColumnTx(ctx, tx, tableName, columnName); err != nil {
			return err
		}
	}

	return nil
}

func dropDefinitionTableTx(ctx context.Context, tx pgx.Tx, tableName string) error {
	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name %q", tableName)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, quotedTable)); err != nil {
		return fmt.Errorf("drop physical table %q: %w", tableName, err)
	}
	return nil
}

func dropDefinitionColumnTx(ctx context.Context, tx pgx.Tx, tableName, columnName string) error {
	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name %q", tableName)
	}
	quotedColumn, err := QuoteIdentifier(columnName)
	if err != nil {
		return fmt.Errorf("invalid column name %q on %q", columnName, tableName)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s DROP COLUMN IF EXISTS %s`, quotedTable, quotedColumn)); err != nil {
		return fmt.Errorf("drop physical column %q on %q: %w", columnName, tableName, err)
	}
	return nil
}

func loadPhysicalColumnsTx(ctx context.Context, tx pgx.Tx, tableName string) (map[string]physicalColumn, error) {
	rows, err := tx.Query(ctx, `
		SELECT column_name, data_type, udt_name, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = $1
	`, tableName)
	if err != nil {
		return nil, fmt.Errorf("load physical columns: %w", err)
	}
	defer rows.Close()

	columns := map[string]physicalColumn{}
	for rows.Next() {
		var name, dataType, udtName, isNullable string
		if err := rows.Scan(&name, &dataType, &udtName, &isNullable); err != nil {
			return nil, fmt.Errorf("scan physical column: %w", err)
		}
		sqlType := strings.ToLower(strings.TrimSpace(dataType))
		if sqlType == "user-defined" {
			sqlType = strings.ToLower(strings.TrimSpace(udtName))
		}
		columns[strings.ToLower(strings.TrimSpace(name))] = physicalColumn{
			SQLType:    sqlType,
			IsNullable: strings.EqualFold(strings.TrimSpace(isNullable), "yes"),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate physical columns: %w", err)
	}
	return columns, nil
}
