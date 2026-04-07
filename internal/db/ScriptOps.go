package db

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"velm/internal/utils"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ScriptRecordQuery is the native substrate for generated model helpers such as:
// Ticket.listIDs(query), Ticket.updateWhere(query, patch),
// Ticket.bulkPatch(ids, patch), and Ticket.deleteWhere(query).
type ScriptRecordFilter struct {
	Column   string
	Operator string
	Value    any
}

type ScriptRecordOrder struct {
	Column    string
	Direction string
}

type ScriptRecordFilterGroup struct {
	Mode    string
	Filters []ScriptRecordFilter
}

type ScriptRecordQuery struct {
	IDs            []string
	Equals         map[string]any
	Filters        []ScriptRecordFilter
	Groups         []ScriptRecordFilterGroup
	OrderBy        []ScriptRecordOrder
	Limit          int
	IncludeDeleted bool
}

type ScriptMutationResult struct {
	IDs      []string
	Affected int
}

type scriptQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func GetScriptRecord(ctx context.Context, tableName, id string) (map[string]any, error) {
	return getScriptRecordWithQuerier(ctx, Pool, tableName, id)
}

func getScriptRecordWithQuerier(ctx context.Context, querier scriptQuerier, tableName, id string) (map[string]any, error) {
	items, err := listScriptRecordsWithQuerier(ctx, querier, tableName, ScriptRecordQuery{
		IDs:   []string{id},
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("record %q not found in %q", id, tableName)
	}
	return items[0], nil
}

func ListScriptRecords(ctx context.Context, tableName string, query ScriptRecordQuery) ([]map[string]any, error) {
	return listScriptRecordsWithQuerier(ctx, Pool, tableName, query)
}

func listScriptRecordsWithQuerier(ctx context.Context, querier scriptQuerier, tableName string, query ScriptRecordQuery) ([]map[string]any, error) {
	view, err := scriptViewForTable(tableName)
	if err != nil {
		return nil, err
	}

	whereClause, args, err := buildScriptWhereClause(view, query, 1)
	if err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	args = append(args, limit)

	quotedTable, err := QuoteIdentifier(view.Table.NAME)
	if err != nil {
		return nil, err
	}

	columnNames := make([]string, 0, len(view.Columns))
	for _, column := range view.Columns {
		quotedColumn, err := QuoteIdentifier(column.NAME)
		if err != nil {
			return nil, err
		}
		columnNames = append(columnNames, quotedColumn)
	}

	querySQL := fmt.Sprintf(`SELECT %s FROM %s`, strings.Join(columnNames, ", "), quotedTable)
	if whereClause != "" {
		querySQL += ` WHERE ` + whereClause
	}
	orderClause, err := buildScriptOrderClause(view, query)
	if err != nil {
		return nil, err
	}
	querySQL += ` ORDER BY ` + orderClause
	querySQL += fmt.Sprintf(` LIMIT $%d`, len(args))

	rows, err := querier.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		values := make([]any, len(view.Columns))
		ptrs := make([]any, len(view.Columns))
		for i := range values {
			ptrs[i] = &values[i]
		}

		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		item := make(map[string]any, len(view.Columns))
		for i, column := range view.Columns {
			item[column.NAME] = utils.NormalizeValue(values[i])
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func CreateScriptRecord(ctx context.Context, tableName string, values map[string]any, userID string) (map[string]any, error) {
	return createScriptRecordWithQuerier(ctx, Pool, tableName, values, userID)
}

func createScriptRecordWithQuerier(ctx context.Context, querier scriptQuerier, tableName string, values map[string]any, userID string) (map[string]any, error) {
	view, err := scriptViewForTable(tableName)
	if err != nil {
		return nil, err
	}

	formData, nullColumns, err := buildScriptWriteData(view, values, strings.TrimSpace(userID), time.Now().Format(time.RFC3339), true)
	if err != nil {
		return nil, err
	}
	if len(formData)+len(nullColumns) == 0 {
		return nil, fmt.Errorf("values are required")
	}

	if scriptHasColumn(view, "_id") {
		recordID, err := allocateRecordIDWithQuerier(ctx, querier)
		if err != nil {
			return nil, err
		}
		formData["_id"] = recordID
		delete(nullColumns, "_id")
	}

	triggerRecord, err := buildRuntimeTriggerRecord(view, nil, formData, nullColumns)
	if err != nil {
		return nil, err
	}
	triggerRecord, err = RunAppBeforeWriteTriggersWithQuerier(ctx, querier, tableName, "record.insert", strings.TrimSpace(userID), "", triggerRecord, nil)
	if err != nil {
		return nil, err
	}
	if err := applyRuntimeTriggerRecordToFormData(view, triggerRecord, nil, true, formData, nullColumns); err != nil {
		return nil, err
	}
	filterSubmittedColumns(view.Columns, formData, nullColumns)
	if taskTypeValue := TaskTypeValueForTable(ctx, tableName); taskTypeValue != "" && scriptColumnsByName(view)["work_type"].NAME != "" {
		formData["work_type"] = taskTypeValue
		delete(nullColumns, "work_type")
	}
	if err := ValidateMetadataWrite(ctx, tableName, "", formData); err != nil {
		return nil, err
	}
	if err := ValidateFormWrite(view.Columns, formData, true); err != nil {
		return nil, err
	}

	quotedTable, err := QuoteIdentifier(view.Table.NAME)
	if err != nil {
		return nil, err
	}
	insertColumns, args, valueParts, err := buildInsertColumnsAndValues(scriptColumnsByName(view), formData, nullColumns)
	if err != nil {
		return nil, err
	}

	var recordID string
	querySQL := fmt.Sprintf(
		`INSERT INTO %s (%s) VALUES (%s) RETURNING _id::text`,
		quotedTable,
		strings.Join(insertColumns, ", "),
		strings.Join(valueParts, ", "),
	)
	if err := querier.QueryRow(ctx, querySQL, args...).Scan(&recordID); err != nil {
		return nil, err
	}

	return getScriptRecordWithQuerier(ctx, querier, tableName, recordID)
}

func UpdateScriptRecord(ctx context.Context, tableName, id string, patch map[string]any, userID string) (map[string]any, error) {
	return updateScriptRecordWithQuerier(ctx, Pool, tableName, id, patch, userID)
}

func updateScriptRecordWithQuerier(ctx context.Context, querier scriptQuerier, tableName, id string, patch map[string]any, userID string) (map[string]any, error) {
	view, err := scriptViewForTable(tableName)
	if err != nil {
		return nil, err
	}

	oldSnapshot := loadRecordSnapshotWithQuerier(ctx, querier, tableName, id)
	if len(oldSnapshot) == 0 {
		return nil, fmt.Errorf("record %q not found in %q", id, tableName)
	}

	formData, nullColumns, err := buildScriptWriteData(view, patch, strings.TrimSpace(userID), time.Now().Format(time.RFC3339), false)
	if err != nil {
		return nil, err
	}
	triggerRecord, err := buildRuntimeTriggerRecord(view, oldSnapshot, formData, nullColumns)
	if err != nil {
		return nil, err
	}
	triggerRecord, err = RunAppBeforeWriteTriggersWithQuerier(ctx, querier, tableName, "record.update", strings.TrimSpace(userID), "", triggerRecord, oldSnapshot)
	if err != nil {
		return nil, err
	}
	if err := applyRuntimeTriggerRecordToFormData(view, triggerRecord, oldSnapshot, false, formData, nullColumns); err != nil {
		return nil, err
	}
	filterSubmittedColumns(view.Columns, formData, nullColumns)
	if taskTypeValue := TaskTypeValueForTable(ctx, tableName); taskTypeValue != "" && scriptColumnsByName(view)["work_type"].NAME != "" {
		formData["work_type"] = taskTypeValue
		delete(nullColumns, "work_type")
	}
	if err := ValidateMetadataWrite(ctx, tableName, id, formData); err != nil {
		return nil, err
	}
	if err := ValidateFormWrite(view.Columns, formData, false); err != nil {
		return nil, err
	}
	if !submittedFormHasChanges(view.Columns, formData, nullColumns, oldSnapshot) {
		return oldSnapshot, nil
	}

	columnNames := make([]string, 0, len(formData))
	for key := range formData {
		columnNames = append(columnNames, key)
	}
	sort.Strings(columnNames)

	values := make([]any, 0, len(columnNames)+1)
	setParts := make([]string, 0, len(columnNames))
	columns := scriptColumnsByName(view)
	for index, columnName := range columnNames {
		quotedColumn, err := QuoteIdentifier(columnName)
		if err != nil {
			return nil, err
		}
		if nullColumns[columnName] {
			setParts = append(setParts, fmt.Sprintf(`%s = NULL`, quotedColumn))
			continue
		}
		values = append(values, formData[columnName])
		setParts = append(setParts, fmt.Sprintf(`%s = $%d%s`, quotedColumn, len(values), scriptColumnCast(columns[columnName].DATA_TYPE)))
		_ = index
	}
	if len(setParts) == 0 {
		return oldSnapshot, nil
	}

	recordIDValue, err := ParseRecordIDValue(tableName, id)
	if err != nil {
		return nil, err
	}
	values = append(values, recordIDValue)

	quotedTable, err := QuoteIdentifier(view.Table.NAME)
	if err != nil {
		return nil, err
	}
	querySQL := fmt.Sprintf(`UPDATE %s SET %s WHERE _id = $%d`, quotedTable, strings.Join(setParts, ", "), len(values))
	commandTag, err := querier.Exec(ctx, querySQL, values...)
	if err != nil {
		return nil, err
	}
	if commandTag.RowsAffected() == 0 {
		return nil, fmt.Errorf("record %q not found in %q", id, tableName)
	}
	return getScriptRecordWithQuerier(ctx, querier, tableName, id)
}

func ListScriptRecordIDs(ctx context.Context, tableName string, query ScriptRecordQuery) ([]string, error) {
	return listScriptRecordIDsWithQuerier(ctx, Pool, tableName, query)
}

func listScriptRecordIDsWithQuerier(ctx context.Context, querier scriptQuerier, tableName string, query ScriptRecordQuery) ([]string, error) {
	view, err := scriptViewForTable(tableName)
	if err != nil {
		return nil, err
	}

	whereClause, args, err := buildScriptWhereClause(view, query, 1)
	if err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	args = append(args, limit)

	quotedTable, err := QuoteIdentifier(view.Table.NAME)
	if err != nil {
		return nil, err
	}

	querySQL := fmt.Sprintf(`SELECT _id::text FROM %s`, quotedTable)
	if whereClause != "" {
		querySQL += ` WHERE ` + whereClause
	}
	orderClause, err := buildScriptOrderClause(view, query)
	if err != nil {
		return nil, err
	}
	querySQL += ` ORDER BY ` + orderClause
	querySQL += fmt.Sprintf(` LIMIT $%d`, len(args))

	rows, err := querier.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func BulkPatchScriptRecords(ctx context.Context, tableName string, ids []string, patch map[string]any, userID string) (ScriptMutationResult, error) {
	return UpdateScriptRecordsWhere(ctx, tableName, ScriptRecordQuery{IDs: ids}, patch, userID)
}

func UpdateScriptRecordsWhere(ctx context.Context, tableName string, query ScriptRecordQuery, patch map[string]any, userID string) (ScriptMutationResult, error) {
	return updateScriptRecordsWhereWithQuerier(ctx, Pool, tableName, query, patch, userID)
}

func updateScriptRecordsWhereWithQuerier(ctx context.Context, querier scriptQuerier, tableName string, query ScriptRecordQuery, patch map[string]any, userID string) (ScriptMutationResult, error) {
	if !scriptQueryHasSelectors(query) {
		return ScriptMutationResult{}, fmt.Errorf("script mutation requires ids or filters")
	}

	ids, err := listScriptRecordIDsWithQuerier(ctx, querier, tableName, query)
	if err != nil {
		return ScriptMutationResult{}, err
	}

	updatedIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		record, err := updateScriptRecordWithQuerier(ctx, querier, tableName, id, patch, userID)
		if err != nil {
			return ScriptMutationResult{}, err
		}
		if record == nil {
			continue
		}
		recordID := strings.TrimSpace(fmt.Sprint(record["_id"]))
		if recordID == "" {
			recordID = id
		}
		updatedIDs = append(updatedIDs, recordID)
	}

	return ScriptMutationResult{
		IDs:      updatedIDs,
		Affected: len(updatedIDs),
	}, nil
}

func DeleteScriptRecordsWhere(ctx context.Context, tableName string, query ScriptRecordQuery, userID string) (ScriptMutationResult, error) {
	return deleteScriptRecordsWhereWithQuerier(ctx, Pool, tableName, query, userID)
}

func deleteScriptRecordsWhereWithQuerier(ctx context.Context, querier scriptQuerier, tableName string, query ScriptRecordQuery, userID string) (ScriptMutationResult, error) {
	if !scriptQueryHasSelectors(query) {
		return ScriptMutationResult{}, fmt.Errorf("script delete requires ids or filters")
	}

	view, err := scriptViewForTable(tableName)
	if err != nil {
		return ScriptMutationResult{}, err
	}
	if !scriptHasColumn(view, "_deleted_at") {
		return ScriptMutationResult{}, fmt.Errorf("table %q does not support soft delete", tableName)
	}

	whereClause, whereArgs, err := buildScriptWhereClause(view, query, 1)
	if err != nil {
		return ScriptMutationResult{}, err
	}
	orderClause, err := buildScriptOrderClause(view, query)
	if err != nil {
		return ScriptMutationResult{}, err
	}

	setParts := []string{`"_deleted_at" = NOW()`}
	args := append([]any{}, whereArgs...)
	if scriptHasColumn(view, "_updated_at") {
		setParts = append(setParts, `"_updated_at" = NOW()`)
	}
	if strings.TrimSpace(userID) != "" && scriptHasColumn(view, "_deleted_by") {
		placeholder := fmt.Sprintf("$%d", len(args)+1)
		args = append(args, strings.TrimSpace(userID))
		setParts = append(setParts, fmt.Sprintf(`"_deleted_by" = %s::uuid`, placeholder))
	}
	if strings.TrimSpace(userID) != "" && scriptHasColumn(view, "_updated_by") {
		placeholder := fmt.Sprintf("$%d", len(args)+1)
		args = append(args, strings.TrimSpace(userID))
		setParts = append(setParts, fmt.Sprintf(`"_updated_by" = %s::uuid`, placeholder))
	}

	quotedTable, err := QuoteIdentifier(view.Table.NAME)
	if err != nil {
		return ScriptMutationResult{}, err
	}

	limitClause := ""
	if query.Limit > 0 {
		limit := query.Limit
		if limit > 5000 {
			limit = 5000
		}
		args = append(args, limit)
		limitClause = fmt.Sprintf(" LIMIT $%d", len(args))
	}

	sqlQuery := fmt.Sprintf(
		`WITH target AS (
			SELECT _id
			FROM %s
			WHERE %s
			ORDER BY %s%s
		)
		UPDATE %s AS t
		SET %s
		FROM target
		WHERE t._id = target._id
		RETURNING t._id::text`,
		quotedTable,
		whereClause,
		orderClause,
		limitClause,
		quotedTable,
		strings.Join(setParts, ", "),
	)

	rows, err := querier.Query(ctx, sqlQuery, args...)
	if err != nil {
		return ScriptMutationResult{}, err
	}
	defer rows.Close()

	ids := make([]string, 0, 32)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return ScriptMutationResult{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return ScriptMutationResult{}, err
	}

	return ScriptMutationResult{
		IDs:      ids,
		Affected: len(ids),
	}, nil
}

func buildScriptWhereClause(view View, query ScriptRecordQuery, startAt int) (string, []any, error) {
	if view.Table == nil || view.Table.NAME == "" {
		return "", nil, fmt.Errorf("table is required")
	}

	columns := scriptColumnsByName(view)
	args := make([]any, 0, len(query.IDs)+len(query.Equals)+len(query.Filters)+len(query.Groups))
	clauses := make([]string, 0, len(query.IDs)+len(query.Equals)+len(query.Filters)+len(query.Groups)+1)
	nextIndex := startAt

	if scriptHasColumn(view, "_deleted_at") && !query.IncludeDeleted {
		clauses = append(clauses, `"_deleted_at" IS NULL`)
	}

	if len(query.IDs) > 0 {
		idClauses := make([]string, 0, len(query.IDs))
		for _, rawID := range query.IDs {
			recordIDValue, err := ParseRecordIDValue(view.Table.NAME, rawID)
			if err != nil {
				return "", nil, err
			}
			idClauses = append(idClauses, fmt.Sprintf("$%d", nextIndex))
			args = append(args, recordIDValue)
			nextIndex++
		}
		clauses = append(clauses, fmt.Sprintf(`"_id" IN (%s)`, strings.Join(idClauses, ", ")))
	}

	keys := make([]string, 0, len(query.Equals))
	for key := range query.Equals {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		column, ok := columns[key]
		if !ok {
			return "", nil, fmt.Errorf("unknown script query column %q", key)
		}
		quotedColumn, err := QuoteIdentifier(column.NAME)
		if err != nil {
			return "", nil, err
		}

		normalized, isNull, err := normalizeScriptColumnValue(column, query.Equals[key])
		if err != nil {
			return "", nil, err
		}
		if isNull {
			clauses = append(clauses, fmt.Sprintf(`%s IS NULL`, quotedColumn))
			continue
		}

		cast := scriptColumnCast(column.DATA_TYPE)
		clauses = append(clauses, fmt.Sprintf(`%s = $%d%s`, quotedColumn, nextIndex, cast))
		args = append(args, normalized)
		nextIndex++
	}

	for _, filter := range query.Filters {
		columnName := strings.TrimSpace(strings.ToLower(filter.Column))
		if columnName == "" {
			return "", nil, fmt.Errorf("script query filter column is required")
		}

		column, ok := columns[columnName]
		if !ok {
			return "", nil, fmt.Errorf("unknown script query column %q", columnName)
		}

		quotedColumn, err := QuoteIdentifier(column.NAME)
		if err != nil {
			return "", nil, err
		}

		clause, filterArgs, consumed, err := buildScriptFilterClause(column, quotedColumn, filter, nextIndex)
		if err != nil {
			return "", nil, err
		}
		if clause != "" {
			clauses = append(clauses, clause)
		}
		if len(filterArgs) > 0 {
			args = append(args, filterArgs...)
		}
		nextIndex += consumed
	}

	for _, group := range query.Groups {
		groupClause, groupArgs, consumed, err := buildScriptFilterGroupClause(view, columns, group, nextIndex)
		if err != nil {
			return "", nil, err
		}
		if groupClause != "" {
			clauses = append(clauses, groupClause)
		}
		if len(groupArgs) > 0 {
			args = append(args, groupArgs...)
		}
		nextIndex += consumed
	}

	return strings.Join(clauses, " AND "), args, nil
}

func buildScriptOrderClause(view View, query ScriptRecordQuery) (string, error) {
	columns := scriptColumnsByName(view)
	if len(query.OrderBy) == 0 {
		quotedID, err := QuoteIdentifier("_id")
		if err != nil {
			return "", err
		}
		return quotedID, nil
	}

	parts := make([]string, 0, len(query.OrderBy))
	for _, order := range query.OrderBy {
		columnName := strings.TrimSpace(strings.ToLower(order.Column))
		if columnName == "" {
			return "", fmt.Errorf("script query order column is required")
		}

		column, ok := columns[columnName]
		if !ok {
			return "", fmt.Errorf("unknown script query order column %q", columnName)
		}

		quotedColumn, err := QuoteIdentifier(column.NAME)
		if err != nil {
			return "", err
		}

		direction, err := normalizeScriptOrderDirection(order.Direction)
		if err != nil {
			return "", err
		}
		parts = append(parts, quotedColumn+` `+direction)
	}

	return strings.Join(parts, ", "), nil
}

func buildScriptSetClauses(view View, patch map[string]any, userID, updatedAt string, startAt int) ([]string, []any, error) {
	if view.Table == nil || view.Table.NAME == "" {
		return nil, nil, fmt.Errorf("table is required")
	}
	if len(patch) == 0 {
		return nil, nil, fmt.Errorf("patch is required")
	}

	columns := scriptColumnsByName(view)
	formData, nullColumns, err := buildScriptWriteData(view, patch, userID, updatedAt, false)
	if err != nil {
		return nil, nil, err
	}

	keys := make([]string, 0, len(formData)+len(nullColumns))
	for key := range formData {
		keys = append(keys, key)
	}
	for key := range nullColumns {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	args := make([]any, 0, len(formData))
	setParts := make([]string, 0, len(keys))
	nextIndex := startAt
	for _, key := range keys {
		column := columns[key]
		quotedColumn, err := QuoteIdentifier(key)
		if err != nil {
			return nil, nil, err
		}
		if nullColumns[key] {
			setParts = append(setParts, fmt.Sprintf(`%s = NULL`, quotedColumn))
			continue
		}

		cast := scriptColumnCast(column.DATA_TYPE)
		setParts = append(setParts, fmt.Sprintf(`%s = $%d%s`, quotedColumn, nextIndex, cast))
		args = append(args, formData[key])
		nextIndex++
	}

	if len(setParts) == 0 {
		return nil, nil, fmt.Errorf("patch is required")
	}
	return setParts, args, nil
}

func buildScriptWriteData(view View, values map[string]any, userID, timestamp string, isCreate bool) (map[string]string, map[string]bool, error) {
	if view.Table == nil || view.Table.NAME == "" {
		return nil, nil, fmt.Errorf("table is required")
	}

	columns := scriptColumnsByName(view)
	formData := map[string]string{}
	nullColumns := map[string]bool{}

	for fieldName, rawValue := range values {
		fieldName = strings.TrimSpace(fieldName)
		column, ok := columns[fieldName]
		if !ok || strings.HasPrefix(fieldName, "_") {
			return nil, nil, fmt.Errorf("invalid script patch field %q", fieldName)
		}

		normalized, isNull, err := normalizeScriptColumnValue(column, rawValue)
		if err != nil {
			return nil, nil, err
		}
		if isNull {
			nullColumns[fieldName] = true
			continue
		}
		formData[fieldName] = normalized
	}

	if err := ValidateFormWrite(view.Columns, formData, isCreate); err != nil {
		return nil, nil, err
	}

	if timestamp != "" && scriptHasColumn(view, "_updated_at") {
		formData["_updated_at"] = timestamp
	}
	if userID != "" && scriptHasColumn(view, "_updated_by") {
		formData["_updated_by"] = userID
	}
	if isCreate {
		if timestamp != "" && scriptHasColumn(view, "_created_at") {
			formData["_created_at"] = timestamp
		}
		if userID != "" && scriptHasColumn(view, "_created_by") {
			formData["_created_by"] = userID
		}
	}

	return formData, nullColumns, nil
}

func scriptViewForTable(tableName string) (View, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if !IsSafeIdentifier(tableName) {
		return View{}, fmt.Errorf("invalid table name")
	}

	view := GetView(tableName)
	if view.Table == nil || view.Table.ID == "" {
		return View{}, fmt.Errorf("table %q not found", tableName)
	}
	return view, nil
}

func scriptColumnsByName(view View) map[string]Column {
	columns := make(map[string]Column, len(view.Columns))
	for _, column := range view.Columns {
		columns[column.NAME] = column
	}
	return columns
}

func scriptHasColumn(view View, name string) bool {
	for _, column := range view.Columns {
		if column.NAME == name {
			return true
		}
	}
	return false
}

func scriptQueryHasSelectors(query ScriptRecordQuery) bool {
	return len(query.IDs) > 0 || len(query.Equals) > 0 || len(query.Filters) > 0 || len(query.Groups) > 0
}

func buildScriptFilterClause(column Column, quotedColumn string, filter ScriptRecordFilter, startAt int) (string, []any, int, error) {
	operator, err := normalizeScriptFilterOperator(filter.Operator)
	if err != nil {
		return "", nil, 0, err
	}

	switch operator {
	case "in", "not in":
		items, err := normalizeScriptFilterArrayValue(filter.Value)
		if err != nil {
			return "", nil, 0, err
		}
		if len(items) == 0 {
			return "", nil, 0, fmt.Errorf("script query %s filter on %q requires at least one value", operator, column.NAME)
		}

		placeholders := make([]string, 0, len(items))
		args := make([]any, 0, len(items))
		nextIndex := startAt
		cast := scriptColumnCast(column.DATA_TYPE)
		for _, item := range items {
			normalized, isNull, err := normalizeScriptColumnValue(column, item)
			if err != nil {
				return "", nil, 0, err
			}
			if isNull {
				return "", nil, 0, fmt.Errorf("script query %s filter on %q does not support null values", operator, column.NAME)
			}
			placeholders = append(placeholders, fmt.Sprintf("$%d%s", nextIndex, cast))
			args = append(args, normalized)
			nextIndex++
		}

		return fmt.Sprintf(`%s %s (%s)`, quotedColumn, strings.ToUpper(operator), strings.Join(placeholders, ", ")), args, len(args), nil
	case "=", "!=", "<", "<=", ">", ">=":
		normalized, isNull, err := normalizeScriptColumnValue(column, filter.Value)
		if err != nil {
			return "", nil, 0, err
		}
		if isNull {
			switch operator {
			case "=":
				return fmt.Sprintf(`%s IS NULL`, quotedColumn), nil, 0, nil
			case "!=":
				return fmt.Sprintf(`%s IS NOT NULL`, quotedColumn), nil, 0, nil
			default:
				return "", nil, 0, fmt.Errorf("script query operator %q on %q does not support null", operator, column.NAME)
			}
		}

		cast := scriptColumnCast(column.DATA_TYPE)
		return fmt.Sprintf(`%s %s $%d%s`, quotedColumn, operator, startAt, cast), []any{normalized}, 1, nil
	default:
		return "", nil, 0, fmt.Errorf("unsupported script query operator %q", filter.Operator)
	}
}

func buildScriptFilterGroupClause(view View, columns map[string]Column, group ScriptRecordFilterGroup, startAt int) (string, []any, int, error) {
	mode, err := normalizeScriptFilterGroupMode(group.Mode)
	if err != nil {
		return "", nil, 0, err
	}
	if len(group.Filters) == 0 {
		return "", nil, 0, fmt.Errorf("script query filter group requires at least one filter")
	}

	parts := make([]string, 0, len(group.Filters))
	args := make([]any, 0, len(group.Filters))
	nextIndex := startAt
	for _, filter := range group.Filters {
		columnName := strings.TrimSpace(strings.ToLower(filter.Column))
		if columnName == "" {
			return "", nil, 0, fmt.Errorf("script query filter group column is required")
		}

		column, ok := columns[columnName]
		if !ok {
			return "", nil, 0, fmt.Errorf("unknown script query column %q", columnName)
		}

		quotedColumn, err := QuoteIdentifier(column.NAME)
		if err != nil {
			return "", nil, 0, err
		}

		clause, clauseArgs, consumed, err := buildScriptFilterClause(column, quotedColumn, filter, nextIndex)
		if err != nil {
			return "", nil, 0, err
		}
		if clause != "" {
			parts = append(parts, clause)
		}
		if len(clauseArgs) > 0 {
			args = append(args, clauseArgs...)
		}
		nextIndex += consumed
	}

	if len(parts) == 0 {
		return "", nil, 0, fmt.Errorf("script query filter group requires at least one filter")
	}
	if len(parts) == 1 {
		return parts[0], args, len(args), nil
	}

	joiner := " OR "
	if mode == "all" {
		joiner = " AND "
	}
	return "(" + strings.Join(parts, joiner) + ")", args, len(args), nil
}

func normalizeScriptFilterOperator(raw string) (string, error) {
	operator := strings.TrimSpace(strings.ToLower(raw))
	if operator == "" {
		operator = "="
	}

	switch operator {
	case "=", "==", "eq", "is":
		return "=", nil
	case "!=", "<>", "ne", "is not":
		return "!=", nil
	case "<", "lt":
		return "<", nil
	case "<=", "lte":
		return "<=", nil
	case ">", "gt":
		return ">", nil
	case ">=", "gte":
		return ">=", nil
	case "in":
		return "in", nil
	case "not in", "not_in", "nin":
		return "not in", nil
	default:
		return "", fmt.Errorf("unsupported script query operator %q", raw)
	}
}

func normalizeScriptFilterGroupMode(raw string) (string, error) {
	mode := strings.TrimSpace(strings.ToLower(raw))
	if mode == "" {
		return "any", nil
	}

	switch mode {
	case "any", "or":
		return "any", nil
	case "all", "and":
		return "all", nil
	default:
		return "", fmt.Errorf("unsupported script query group mode %q", raw)
	}
}

func normalizeScriptFilterArrayValue(value any) ([]any, error) {
	switch v := value.(type) {
	case nil:
		return nil, fmt.Errorf("script query filter value must be an array")
	case []any:
		return v, nil
	case []string:
		items := make([]any, 0, len(v))
		for _, item := range v {
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("script query filter value must be an array")
	}
}

func normalizeScriptOrderDirection(raw string) (string, error) {
	direction := strings.TrimSpace(strings.ToLower(raw))
	if direction == "" {
		return "ASC", nil
	}

	switch direction {
	case "asc", "ascending":
		return "ASC", nil
	case "desc", "descending":
		return "DESC", nil
	default:
		return "", fmt.Errorf("unsupported script query order direction %q", raw)
	}
}

func normalizeScriptColumnValue(column Column, value any) (string, bool, error) {
	if value == nil {
		return "", true, nil
	}

	raw, err := scriptAnyToString(value)
	if err != nil {
		return "", false, err
	}
	normalized, err := NormalizeSubmittedValue(column.DATA_TYPE, strings.TrimSpace(raw))
	if err != nil {
		return "", false, err
	}
	if shouldStoreNullForBlankColumn(column, strings.TrimSpace(raw), normalized) {
		return "", true, nil
	}
	if normalized != "" {
		if err := validateColumnValue(column, normalized); err != nil {
			return "", false, err
		}
	}

	return normalized, false, nil
}

func scriptAnyToString(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool, float64, float32, int, int64, int32, uint, uint64, uint32:
		return fmt.Sprint(v), nil
	case map[string]any, []any:
		raw, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("invalid script value")
		}
		return string(raw), nil
	default:
		return "", fmt.Errorf("unsupported script value type %T", value)
	}
}

func scriptColumnCast(dataType string) string {
	dt := normalizeDataType(dataType)
	switch {
	case dt == "uuid" || dt == "reference":
		return "::uuid"
	case dt == "bool" || dt == "boolean":
		return "::boolean"
	case dt == "int" || dt == "integer" || dt == "serial":
		return "::integer"
	case dt == "bigint" || dt == "bigserial":
		return "::bigint"
	case dt == "float" || dt == "double":
		return "::double precision"
	case dt == "decimal" || dt == "numeric":
		return "::numeric"
	case dt == "json":
		return "::json"
	case dt == "jsonb":
		return "::jsonb"
	case dt == "date":
		return "::date"
	case dt == "timestamp" || dt == "datetime":
		return "::timestamp"
	case dt == "timestamptz":
		return "::timestamptz"
	default:
		return ""
	}
}
