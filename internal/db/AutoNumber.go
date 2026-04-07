package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const autoNumberTriggerName = "trg_velm_autnumber_before_write"

type autoNumberTriggerConfig struct {
	Column    string `json:"column"`
	Prefix    string `json:"prefix"`
	AppPrefix string `json:"app_prefix"`
}

func resolvedAutoNumberScope(appName, namespace string) string {
	scope := strings.TrimSpace(strings.ToLower(namespace))
	if scope != "" {
		return scope
	}
	scope = strings.TrimSpace(strings.ToLower(appName))
	if scope != "" {
		return scope
	}
	return "system"
}

func autoNumberAppPrefix(scope, prefix string) string {
	scope = strings.TrimSpace(strings.ToLower(scope))
	prefix = normalizeAutoNumberPrefix(prefix)
	if scope == "" {
		return prefix
	}
	return scope + "_" + prefix
}

func applyDerivedTaskAutoNumberPrefix(apps []RegisteredApp, ownerApp RegisteredApp, table AppDefinitionTable, columns []AppDefinitionColumn) []AppDefinitionColumn {
	tableName := strings.TrimSpace(strings.ToLower(table.Name))
	if tableName == "" || tableName == "base_task" {
		return columns
	}
	if !definitionTableExtendsTargetWithApps(apps, ownerApp, table, "base_task", map[string]bool{}) {
		return columns
	}

	prefix := derivedTaskAutoNumberPrefix(ownerApp, table)
	updated := append([]AppDefinitionColumn(nil), columns...)
	for i := range updated {
		if strings.TrimSpace(strings.ToLower(updated[i].Name)) != "number" {
			continue
		}
		if !IsAutoNumberDataType(updated[i].DataType) {
			continue
		}
		updated[i].Prefix = prefix
		break
	}
	return updated
}

func derivedTaskAutoNumberPrefix(ownerApp RegisteredApp, table AppDefinitionTable) string {
	candidates := []string{
		strings.TrimSpace(table.LabelSingular),
		scriptScopeAliasForTable(ownerApp, table.Name),
		table.Name,
	}
	for _, candidate := range candidates {
		prefix := compactAutoNumberPrefixCandidate(candidate)
		switch {
		case len(prefix) >= 4:
			return prefix[:4]
		case len(prefix) == 3:
			return prefix
		}
	}
	return "TASK"
}

func compactAutoNumberPrefixCandidate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(value))
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z':
			builder.WriteRune(ch - ('a' - 'A'))
		case ch >= 'A' && ch <= 'Z':
			builder.WriteRune(ch)
		}
	}
	return builder.String()
}

func buildAutoNumberTriggerConfigs(scope string, table AppDefinitionTable) ([]autoNumberTriggerConfig, error) {
	configs := make([]autoNumberTriggerConfig, 0, len(table.Columns))
	for _, column := range table.Columns {
		if !IsAutoNumberDataType(column.DataType) {
			continue
		}
		prefix := normalizeAutoNumberPrefix(column.Prefix)
		if err := validateAutoNumberPrefix(prefix); err != nil {
			return nil, fmt.Errorf("column %q: %w", column.Name, err)
		}
		configs = append(configs, autoNumberTriggerConfig{
			Column:    strings.TrimSpace(strings.ToLower(column.Name)),
			Prefix:    prefix,
			AppPrefix: autoNumberAppPrefix(scope, prefix),
		})
	}
	return configs, nil
}

func applyTableAutoNumberStateTx(ctx context.Context, tx pgx.Tx, scope string, table AppDefinitionTable) error {
	configs, err := buildAutoNumberTriggerConfigs(scope, table)
	if err != nil {
		return err
	}
	if err := ensureAutoNumberTriggerTx(ctx, tx, table.Name, configs); err != nil {
		return err
	}
	if len(configs) == 0 {
		return nil
	}
	return syncAutoNumberCountersTx(ctx, tx, table.Name, configs)
}

func ensureAutoNumberTriggerTx(ctx context.Context, tx pgx.Tx, tableName string, configs []autoNumberTriggerConfig) error {
	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name %q", tableName)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, autoNumberTriggerName, quotedTable)); err != nil {
		return fmt.Errorf("drop autnumber trigger on %q: %w", tableName, err)
	}
	if len(configs) == 0 {
		return nil
	}

	payload, err := json.Marshal(configs)
	if err != nil {
		return fmt.Errorf("marshal autnumber trigger config for %q: %w", tableName, err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION base_autnumber_before_write(%s)`,
		autoNumberTriggerName,
		quotedTable,
		quoteSQLLiteral(string(payload)),
	)); err != nil {
		return fmt.Errorf("create autnumber trigger on %q: %w", tableName, err)
	}
	return nil
}

func syncAutoNumberCountersTx(ctx context.Context, tx pgx.Tx, tableName string, configs []autoNumberTriggerConfig) error {
	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return fmt.Errorf("invalid table name %q", tableName)
	}
	for _, config := range configs {
		quotedColumn, err := QuoteIdentifier(config.Column)
		if err != nil {
			return fmt.Errorf("invalid column name %q on %q", config.Column, tableName)
		}

		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`UPDATE %s
			SET %s = %s || '-' || LPAD(substring(BTRIM(%s) FROM '([0-9]+)$'), 6, '0')
			WHERE %s IS NOT NULL
			  AND BTRIM(%s) <> ''
			  AND BTRIM(%s) ~ '^[A-Z]{3,4}-[0-9]+$'
			  AND BTRIM(%s) !~ %s`,
			quotedTable,
			quotedColumn,
			quoteSQLLiteral(config.Prefix),
			quotedColumn,
			quotedColumn,
			quotedColumn,
			quotedColumn,
			quotedColumn,
			quoteSQLLiteral("^"+config.Prefix+`-[0-9]{6,}$`),
		)); err != nil {
			return fmt.Errorf("normalize autnumber values for %s.%s: %w", tableName, config.Column, err)
		}

		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`SELECT base_sync_autnumber_counter(%s, %s, %s) FROM %s WHERE %s IS NOT NULL AND BTRIM(%s) <> ''`,
			quoteSQLLiteral(config.AppPrefix),
			quoteSQLLiteral(config.Prefix),
			quotedColumn,
			quotedTable,
			quotedColumn,
			quotedColumn,
		)); err != nil {
			return fmt.Errorf("sync autnumber counter for %s.%s: %w", tableName, config.Column, err)
		}

		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`UPDATE %s SET %s = base_next_autnumber(%s, %s) WHERE %s IS NULL OR BTRIM(%s) = ''`,
			quotedTable,
			quotedColumn,
			quoteSQLLiteral(config.AppPrefix),
			quoteSQLLiteral(config.Prefix),
			quotedColumn,
			quotedColumn,
		)); err != nil {
			return fmt.Errorf("backfill autnumber values for %s.%s: %w", tableName, config.Column, err)
		}
	}
	return nil
}

func quoteSQLLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
