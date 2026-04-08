package db

import (
	"context"
	"crypto/sha1"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

const provisionedSeedGroupDescription = "Provisioned automatically from app seed data."

type appSeedState struct {
	App     RegisteredApp
	Aliases map[string]string
}

func EnsurePublishedAppSeeds(ctx context.Context, appName string) error {
	app, err := GetActiveAppByName(ctx, appName)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "app not found") {
			return nil
		}
		return err
	}

	definition := runtimeDefinitionForApp(app)
	if definition == nil || len(definition.Seeds) == 0 {
		return nil
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin published app seed sync for %q: %w", app.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := applyDefinitionSeedsTx(ctx, tx, app, definition); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit published app seed sync for %q: %w", app.Name, err)
	}
	return nil
}

func applyDefinitionSeedsTx(ctx context.Context, tx pgx.Tx, app RegisteredApp, definition *AppDefinition) error {
	if definition == nil || len(definition.Seeds) == 0 {
		return nil
	}

	state, err := buildAppSeedState(app, definition)
	if err != nil {
		return err
	}

	for _, seed := range definition.Seeds {
		tableName := strings.TrimSpace(strings.ToLower(seed.Table))
		if tableName == "" {
			return fmt.Errorf("seed table is required")
		}
		for rowIndex, row := range seed.Rows {
			if err := upsertDefinitionSeedRowTx(ctx, tx, state, tableName, rowIndex, row); err != nil {
				return fmt.Errorf("seed %q row %d: %w", tableName, rowIndex+1, err)
			}
		}
	}
	return nil
}

func buildAppSeedState(app RegisteredApp, definition *AppDefinition) (appSeedState, error) {
	state := appSeedState{
		App:     app,
		Aliases: map[string]string{},
	}

	for _, seed := range definition.Seeds {
		tableName := strings.TrimSpace(strings.ToLower(seed.Table))
		for rowIndex, row := range seed.Rows {
			alias := seedRowAlias(row)
			if alias == "" {
				continue
			}
			recordID := plannedSeedRecordID(app, tableName, rowIndex, row)
			if existing, ok := state.Aliases[alias]; ok && existing != recordID {
				return appSeedState{}, fmt.Errorf("duplicate seed alias %q", alias)
			}
			state.Aliases[alias] = recordID
		}
	}

	return state, nil
}

func upsertDefinitionSeedRowTx(ctx context.Context, tx pgx.Tx, state appSeedState, tableName string, rowIndex int, rawRow map[string]any) error {
	view, err := scriptViewForTable(tableName)
	if err != nil {
		return err
	}

	columns := scriptColumnsByName(view)
	rowValues := make(map[string]any, allocHintSum(len(rawRow), 1))
	if _, ok := columns["_id"]; ok {
		rowValues["_id"] = plannedSeedRecordID(state.App, tableName, rowIndex, rawRow)
	}

	for fieldName, rawValue := range rawRow {
		fieldName = strings.TrimSpace(strings.ToLower(fieldName))
		if fieldName == "" || fieldName == "_seed_key" {
			continue
		}
		column, ok := columns[fieldName]
		if !ok {
			return fmt.Errorf("unknown seed column %q on table %q", fieldName, tableName)
		}

		if raw, ok := rawValue.(string); ok && strings.TrimSpace(raw) == "@first-user" && column.IS_NULLABLE {
			userID, found, err := maybeFirstSeedUserIDTx(ctx, tx)
			if err != nil {
				return fmt.Errorf("resolve %s.%s: %w", tableName, fieldName, err)
			}
			if found {
				rowValues[fieldName] = userID
			} else {
				rowValues[fieldName] = nil
			}
			continue
		}

		resolved, err := resolveDefinitionSeedValueTx(ctx, tx, state, rawValue)
		if err != nil {
			return fmt.Errorf("resolve %s.%s: %w", tableName, fieldName, err)
		}
		rowValues[fieldName] = resolved
	}

	if taskTypeValue := TaskTypeValueForTable(ctx, tableName); taskTypeValue != "" {
		if _, ok := columns["work_type"]; ok {
			if _, exists := rowValues["work_type"]; !exists {
				rowValues["work_type"] = taskTypeValue
			}
		}
	}

	return upsertSeedRecordTx(ctx, tx, view, rowValues)
}

func resolveDefinitionSeedValueTx(ctx context.Context, tx pgx.Tx, state appSeedState, rawValue any) (any, error) {
	switch value := rawValue.(type) {
	case string:
		trimmed := strings.TrimSpace(value)
		switch {
		case trimmed == "@first-user":
			return firstSeedUserIDTx(ctx, tx)
		case strings.HasPrefix(trimmed, "@seed:"):
			alias := strings.TrimSpace(strings.TrimPrefix(trimmed, "@seed:"))
			recordID, ok := state.Aliases[alias]
			if !ok || strings.TrimSpace(recordID) == "" {
				return nil, fmt.Errorf("unknown seed alias %q", alias)
			}
			return recordID, nil
		case strings.HasPrefix(trimmed, "@group:"):
			groupName := strings.TrimSpace(strings.TrimPrefix(trimmed, "@group:"))
			if groupName == "" {
				return nil, fmt.Errorf("group name is required")
			}
			return ensureSeedGroupIDTx(ctx, tx, groupName)
		default:
			return value, nil
		}
	case []any:
		items := make([]any, 0, len(value))
		for _, item := range value {
			resolved, err := resolveDefinitionSeedValueTx(ctx, tx, state, item)
			if err != nil {
				return nil, err
			}
			items = append(items, resolved)
		}
		return items, nil
	case map[string]any:
		items := make(map[string]any, len(value))
		for key, item := range value {
			resolved, err := resolveDefinitionSeedValueTx(ctx, tx, state, item)
			if err != nil {
				return nil, err
			}
			items[key] = resolved
		}
		return items, nil
	default:
		return rawValue, nil
	}
}

func ensureSeedGroupIDTx(ctx context.Context, tx pgx.Tx, groupName string) (string, error) {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return "", fmt.Errorf("group name is required")
	}

	var groupID string
	err := tx.QueryRow(ctx, `
		SELECT _id::text
		FROM _group
		WHERE LOWER(name) = LOWER($1)
		LIMIT 1
	`, groupName).Scan(&groupID)
	if err == nil && strings.TrimSpace(groupID) != "" {
		return groupID, nil
	}
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "no rows") {
		return "", err
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO _group (name, description, is_system)
		VALUES ($1, $2, FALSE)
		ON CONFLICT (name) DO UPDATE
		SET _updated_at = NOW()
		RETURNING _id::text
	`, groupName, provisionedSeedGroupDescription).Scan(&groupID); err != nil {
		return "", err
	}
	return groupID, nil
}

func firstSeedUserIDTx(ctx context.Context, tx pgx.Tx) (string, error) {
	userID, found, err := maybeFirstSeedUserIDTx(ctx, tx)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("no users available for seed lookup")
	}
	return userID, nil
}

func maybeFirstSeedUserIDTx(ctx context.Context, tx pgx.Tx) (string, bool, error) {
	var userID string
	if err := tx.QueryRow(ctx, `
		SELECT _id::text
		FROM _user
		ORDER BY LOWER(COALESCE(name, '')) ASC, LOWER(COALESCE(email, '')) ASC, _id ASC
		LIMIT 1
	`).Scan(&userID); err != nil {
		if err == pgx.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	return userID, true, nil
}

func upsertSeedRecordTx(ctx context.Context, tx pgx.Tx, view View, rowValues map[string]any) error {
	if view.Table == nil || view.Table.NAME == "" {
		return fmt.Errorf("table is required")
	}
	if len(rowValues) == 0 {
		return nil
	}

	columns := scriptColumnsByName(view)
	keys := make([]string, 0, len(rowValues))
	for key := range rowValues {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	quotedTable, err := QuoteIdentifier(view.Table.NAME)
	if err != nil {
		return err
	}

	insertColumns := make([]string, 0, len(keys))
	placeholders := make([]string, 0, len(keys))
	values := make([]any, 0, len(keys))
	updateSet := make([]string, 0, len(keys))
	for index, key := range keys {
		column, ok := columns[key]
		if !ok {
			return fmt.Errorf("unknown column %q on table %q", key, view.Table.NAME)
		}

		normalized, isNull, err := normalizeScriptColumnValue(column, rowValues[key])
		if err != nil {
			return err
		}

		quotedColumn, err := QuoteIdentifier(key)
		if err != nil {
			return err
		}
		insertColumns = append(insertColumns, quotedColumn)
		placeholders = append(placeholders, fmt.Sprintf("$%d%s", index+1, scriptColumnCast(column.DATA_TYPE)))
		if isNull {
			values = append(values, nil)
		} else {
			values = append(values, normalized)
		}

		if seedUpdateExcludedColumn(key) {
			continue
		}
		updateSet = append(updateSet, fmt.Sprintf("%s = EXCLUDED.%s", quotedColumn, quotedColumn))
	}

	query := fmt.Sprintf(
		`INSERT INTO %s (%s) VALUES (%s)`,
		quotedTable,
		strings.Join(insertColumns, ", "),
		strings.Join(placeholders, ", "),
	)
	if len(updateSet) == 0 {
		query += ` ON CONFLICT (_id) DO NOTHING`
	} else {
		query += ` ON CONFLICT (_id) DO UPDATE SET ` + strings.Join(updateSet, ", ")
	}

	if _, err := tx.Exec(ctx, query, values...); err != nil {
		return err
	}
	return nil
}

func seedUpdateExcludedColumn(columnName string) bool {
	columnName = strings.TrimSpace(strings.ToLower(columnName))
	switch columnName {
	case "_id", "_created_at", "_created_by", "_deleted_at", "_deleted_by", "_update_count":
		return true
	default:
		return false
	}
}

func seedRowAlias(row map[string]any) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(fmt.Sprint(row["_seed_key"])))
}

func plannedSeedRecordID(app RegisteredApp, tableName string, rowIndex int, row map[string]any) string {
	if row != nil {
		if rawID := strings.TrimSpace(fmt.Sprint(row["_id"])); rawID != "" && rawID != "<nil>" {
			return rawID
		}
	}
	seedKey := seedRowAlias(row)
	source := firstNonEmpty(app.Namespace, app.Name)
	source += "|" + strings.TrimSpace(strings.ToLower(tableName)) + "|"
	if seedKey != "" {
		source += seedKey
	} else {
		source += strconv.Itoa(rowIndex)
	}
	return deterministicSeedUUID(source)
}

func deterministicSeedUUID(source string) string {
	sum := sha1.Sum([]byte(source))
	buf := [16]byte{}
	copy(buf[:], sum[:16])
	buf[6] = (buf[6] & 0x0f) | 0x50
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%04x%08x",
		uint32(buf[0])<<24|uint32(buf[1])<<16|uint32(buf[2])<<8|uint32(buf[3]),
		uint16(buf[4])<<8|uint16(buf[5]),
		uint16(buf[6])<<8|uint16(buf[7]),
		uint16(buf[8])<<8|uint16(buf[9]),
		uint16(buf[10])<<8|uint16(buf[11]),
		uint32(buf[12])<<24|uint32(buf[13])<<16|uint32(buf[14])<<8|uint32(buf[15]),
	)
}
