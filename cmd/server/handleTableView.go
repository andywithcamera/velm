package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/utils"
)

type listViewServerDefaults struct {
	Query     string `json:"query"`
	Sort      string `json:"sort"`
	Direction string `json:"direction"`
	PageSize  int    `json:"pageSize"`
}

type listViewPersistedState struct {
	ServerDefaults listViewServerDefaults `json:"serverDefaults"`
}

type secureListPageResult struct {
	Page       int
	Rows       []map[string]any
	TotalRows  int
	TotalPages int
	HasNext    bool
	CountExact bool
}

func handleTableView(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	route := resolveTableRoute(r)
	if route.TableName == "" {
		http.Error(w, "Missing table name", http.StatusBadRequest)
		return
	}

	tableName := route.TableName
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if !requireAdminTableAccess(w, r, tableName) {
		return
	}
	table := db.GetTable(tableName)
	if table.ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	securityEvaluator, err := db.LoadTableSecurityEvaluator(ctx, tableName, userID)
	if err != nil {
		http.Error(w, "Failed to evaluate security rules", http.StatusInternalServerError)
		return
	}

	quotedTableName, err := db.QuoteIdentifier(tableName)
	if err != nil {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}

	schemaRows, err := db.Pool.Query(ctx, "SELECT * FROM "+quotedTableName+" LIMIT 0")
	if err != nil {
		http.Error(w, "Error querying table", http.StatusInternalServerError)
		return
	}
	defer schemaRows.Close()

	fieldDescriptions := schemaRows.FieldDescriptions()
	schemaCols := make([]string, len(fieldDescriptions))
	quotedColMap := make(map[string]string, len(fieldDescriptions))
	colSet := make(map[string]bool, len(fieldDescriptions))
	for i, fd := range fieldDescriptions {
		name := string(fd.Name)
		schemaCols[i] = name
		colSet[name] = true
		quotedCol, err := db.QuoteIdentifier(name)
		if err != nil {
			http.Error(w, "Invalid column schema", http.StatusInternalServerError)
			return
		}
		quotedColMap[name] = quotedCol
	}
	cols := orderedListColumns(ctx, tableName, append([]string(nil), schemaCols...))
	queryColumns := loadTableQueryColumns(tableName, cols)
	querySources := db.ListTableQuerySources(ctx, tableName)
	if len(querySources) == 0 {
		querySources = []string{tableName}
	}
	listSourceQuery, err := buildTableListSourceQuery(schemaCols, quotedColMap, querySources)
	if err != nil {
		http.Error(w, "Error preparing table query", http.StatusInternalServerError)
		return
	}

	queryVals := r.URL.Query()
	defaults := loadListViewServerDefaults(r, tableName)

	serverFilter := strings.TrimSpace(queryVals.Get("q"))
	if serverFilter == "" {
		serverFilter = strings.TrimSpace(defaults.Query)
	}
	if len(serverFilter) > 500 {
		serverFilter = serverFilter[:500]
	}

	sortColumn := strings.TrimSpace(queryVals.Get("sort"))
	if sortColumn == "" {
		sortColumn = strings.TrimSpace(defaults.Sort)
	}
	if !colSet[sortColumn] {
		sortColumn = defaultSortColumn(cols)
	}

	sortDirection := strings.ToLower(strings.TrimSpace(queryVals.Get("dir")))
	if sortDirection == "" {
		sortDirection = strings.ToLower(strings.TrimSpace(defaults.Direction))
	}
	if sortDirection != "desc" {
		sortDirection = "asc"
	}

	defaultPageSize := defaults.PageSize
	if defaultPageSize <= 0 {
		defaultPageSize = 50
	}
	pageSize := parseIntRange(queryVals.Get("size"), defaultPageSize, 10, 200)
	page := parseIntRange(queryVals.Get("page"), 1, 1, 1000000)

	whereClause := ""
	args := make([]any, 0, len(cols)+2)
	clauses := make([]string, 0, 2)
	showDeleted := strings.TrimSpace(queryVals.Get("deleted")) == "1"
	if colSet["_deleted_at"] && !showDeleted {
		clauses = append(clauses, "_deleted_at IS NULL")
	}
	if route.FilterColumn != "" {
		quotedFilterColumn, err := db.QuoteIdentifier(route.FilterColumn)
		if err != nil {
			http.Error(w, "Invalid filter column", http.StatusInternalServerError)
			return
		}
		clauses = append(clauses, quotedFilterColumn+" = $"+strconv.Itoa(len(args)+1))
		args = append(args, route.FilterValue)
	}
	queryMode := "plain"
	queryError := ""
	if serverFilter != "" {
		filterClause, filterArgs, structured, err := buildTableWhereClause(serverFilter, queryColumns, quotedColMap)
		if err != nil {
			queryError = err.Error()
			fallback := buildPlainTextWhereClause(queryColumns, quotedColMap)
			clauses = append(clauses, fallback)
			args = append(args, "%"+serverFilter+"%")
		} else {
			if structured {
				queryMode = "structured"
			}
			clauses = append(clauses, filterClause)
			args = append(args, filterArgs...)
		}
	}
	if len(clauses) > 0 {
		whereClause = " WHERE " + strings.Join(clauses, " AND ")
	}

	var totalRows int
	quotedSortColumn, err := db.QuoteIdentifier(sortColumn)
	if err != nil {
		http.Error(w, "Invalid sort column", http.StatusBadRequest)
		return
	}

	var data []map[string]any
	totalPages := 1
	countExact := true
	if securityEvaluator != nil && securityEvaluator.HasReadAccessControls() {
		query := "SELECT * FROM (" + listSourceQuery + ") AS list_source" + whereClause +
			" ORDER BY " + quotedSortColumn + " " + strings.ToUpper(sortDirection)
		pageResult, err := loadSecureListPage(ctx, tableName, schemaCols, query, args, page, pageSize, securityEvaluator)
		if err != nil {
			http.Error(w, "Error querying table", http.StatusInternalServerError)
			return
		}
		page = pageResult.Page
		totalRows = pageResult.TotalRows
		totalPages = pageResult.TotalPages
		countExact = pageResult.CountExact
		data = decorateTableRows(ctx, queryColumns, append([]map[string]any(nil), pageResult.Rows...))
		prevURL, nextURL := paginationURLs(r.URL.Query(), route.RouteName, page, totalPages, pageResult.HasNext, countExact)
		viewData := buildTableViewData(w, r, route, tableName, table, data, serverFilter, sortColumn, sortDirection, page, pageSize, totalRows, totalPages, countExact, prevURL, nextURL, defaults, showDeleted, queryColumns, queryMode, queryError, querySources, canCreateForTable(ctx, userID, tableName, securityEvaluator, auth.AppIDFromRequest(r)))
		if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
			http.Error(w, "Failed to render table view", http.StatusInternalServerError)
		}
		return
	} else {
		countQuery := "SELECT COUNT(*) FROM (" + listSourceQuery + ") AS list_source" + whereClause
		if err := db.Pool.QueryRow(ctx, countQuery, args...).Scan(&totalRows); err != nil {
			http.Error(w, "Error counting records", http.StatusInternalServerError)
			return
		}
		if totalRows > 0 {
			totalPages = (totalRows + pageSize - 1) / pageSize
		}
		if page > totalPages {
			page = totalPages
		}
		offset := (page - 1) * pageSize
		query := "SELECT * FROM (" + listSourceQuery + ") AS list_source" + whereClause +
			" ORDER BY " + quotedSortColumn + " " + strings.ToUpper(sortDirection) +
			fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
		queryArgs := append(args, pageSize, offset)

		rows, err := db.Pool.Query(ctx, query, queryArgs...)
		if err != nil {
			http.Error(w, "Error querying table", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				http.Error(w, "Failed to scan row", http.StatusInternalServerError)
				return
			}

			row := mapListRowValues(schemaCols, values, tableName)
			data = append(data, row)
		}
		data = decorateTableRows(ctx, queryColumns, data)
	}

	prevURL, nextURL := paginationURLs(r.URL.Query(), route.RouteName, page, totalPages, page < totalPages, countExact)
	viewData := buildTableViewData(w, r, route, tableName, table, data, serverFilter, sortColumn, sortDirection, page, pageSize, totalRows, totalPages, countExact, prevURL, nextURL, defaults, showDeleted, queryColumns, queryMode, queryError, querySources, canCreateForTable(ctx, userID, tableName, securityEvaluator, auth.AppIDFromRequest(r)))

	// Render the table view template
	err = templates.ExecuteTemplate(w, "layout.html", viewData)
	if err != nil {
		http.Error(w, "Failed to render table view", http.StatusInternalServerError)
	}
}

func mapRowValues(schemaCols []string, values []any) map[string]any {
	row := make(map[string]any, len(schemaCols))
	for i, col := range schemaCols {
		if i >= len(values) {
			row[col] = nil
			continue
		}
		row[col] = utils.NormalizeValue(values[i])
	}
	return row
}

func mapListRowValues(schemaCols []string, values []any, fallbackTableName string) map[string]any {
	row := mapRowValues(schemaCols, values)
	recordTable := fallbackTableName
	if len(values) > len(schemaCols) {
		value := strings.TrimSpace(fmt.Sprint(utils.NormalizeValue(values[len(schemaCols)])))
		if value != "" && value != "<nil>" {
			recordTable = value
		}
	}
	if recordTable != "" {
		row["_record_table"] = recordTable
	}

	recordID := strings.TrimSpace(fmt.Sprint(row["_id"]))
	if recordID != "" && recordID != "<nil>" && recordTable != "" {
		row["_record_url"] = buildRecordFormURL(recordTable, recordID)
	}
	return row
}

func buildRecordFormURL(tableName, recordID string) string {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	recordID = strings.TrimSpace(recordID)
	if tableName == "" || recordID == "" {
		return ""
	}
	return "/f/" + tableName + "/" + url.PathEscape(recordID)
}

func buildTableListSourceQuery(schemaCols []string, quotedColMap map[string]string, sourceTables []string) (string, error) {
	if len(sourceTables) == 0 {
		return "", fmt.Errorf("no source tables")
	}

	selectCols := make([]string, 0, len(schemaCols))
	for _, col := range schemaCols {
		quotedCol := strings.TrimSpace(quotedColMap[col])
		if quotedCol == "" {
			return "", fmt.Errorf("missing quoted column for %q", col)
		}
		selectCols = append(selectCols, quotedCol)
	}

	selects := make([]string, 0, len(sourceTables))
	for _, sourceTable := range sourceTables {
		if !db.IsSafeIdentifier(sourceTable) {
			return "", fmt.Errorf("invalid source table %q", sourceTable)
		}
		quotedTable, err := db.QuoteIdentifier(sourceTable)
		if err != nil {
			return "", err
		}
		selects = append(selects, fmt.Sprintf(
			"SELECT %s, %s AS _record_table FROM %s",
			strings.Join(selectCols, ", "),
			sqlTextLiteral(sourceTable),
			quotedTable,
		))
	}

	return strings.Join(selects, " UNION ALL "), nil
}

func sqlTextLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

type tableRoute struct {
	RouteName    string
	TableName    string
	FilterColumn string
	FilterValue  string
}

func resolveTableRoute(r *http.Request) tableRoute {
	routeName := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/t/"))
	route := tableRoute{
		RouteName: routeName,
		TableName: routeName,
	}

	switch routeName {
	case "preferences":
		route.TableName = "_user_preference"
		route.FilterColumn = "user_id"
		route.FilterValue = auth.UserIDFromRequest(r)
	}

	return route
}

func newRecordURL(route tableRoute) string {
	tableName := strings.TrimSpace(route.TableName)
	if tableName == "" {
		return ""
	}

	base := "/f/" + tableName + "/new"
	if strings.TrimSpace(route.FilterColumn) == "" || strings.TrimSpace(route.FilterValue) == "" {
		return base
	}

	values := url.Values{}
	values.Set(route.FilterColumn, route.FilterValue)
	return base + "?" + values.Encode()
}

func decorateTableRows(ctx context.Context, columns []tableQueryColumn, rows []map[string]any) []map[string]any {
	if len(rows) == 0 || len(columns) == 0 {
		return rows
	}
	for _, column := range columns {
		switch strings.ToLower(strings.TrimSpace(column.DataType)) {
		case "choice":
			for _, row := range rows {
				raw := strings.TrimSpace(fmt.Sprint(row[column.Name]))
				if label, ok := column.ChoiceLabels[raw]; ok && raw != "" {
					row[column.Name] = label
					continue
				}
				row[column.Name] = truncateListCellValue(raw, column.DataType)
			}
		case "reference":
			referenceLabels := loadReferenceLabels(ctx, column.ReferenceTo, collectReferenceIDs(rows, column.Name))
			for _, row := range rows {
				raw := strings.TrimSpace(fmt.Sprint(row[column.Name]))
				if label, ok := referenceLabels[raw]; ok && raw != "" {
					row[column.Name] = label
					continue
				}
				row[column.Name] = truncateListCellValue(raw, column.DataType)
			}
		default:
			for _, row := range rows {
				row[column.Name] = truncateListCellValue(fmt.Sprint(row[column.Name]), column.DataType)
			}
		}
	}
	return rows
}

func collectReferenceIDs(rows []map[string]any, columnName string) []string {
	seen := map[string]bool{}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		value := strings.TrimSpace(fmt.Sprint(row[columnName]))
		if value == "" || value == "<nil>" || seen[value] {
			continue
		}
		seen[value] = true
		ids = append(ids, value)
	}
	return ids
}

func loadReferenceLabels(ctx context.Context, tableName string, ids []string) map[string]string {
	if tableName == "" || len(ids) == 0 || !db.IsSafeIdentifier(tableName) {
		return map[string]string{}
	}
	quotedTable, err := db.QuoteIdentifier(tableName)
	if err != nil {
		return map[string]string{}
	}
	labelColumn := referenceLabelColumn(tableName)
	quotedLabel, err := db.QuoteIdentifier(labelColumn)
	if err != nil {
		return map[string]string{}
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	recordIDKind := inferReferenceRecordIDKind(tableName)
	if recordIDKind == "" {
		recordIDKind = "uuid"
	}
	for _, id := range ids {
		if recordIDKind == "int" {
			value, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
			if err != nil || value <= 0 {
				continue
			}
			args = append(args, value)
			placeholders = append(placeholders, fmt.Sprintf("$%d::bigint", len(args)))
			continue
		}
		if !utils.IsValidUUID(id) {
			continue
		}
		args = append(args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d::uuid", len(args)))
	}
	if len(placeholders) == 0 {
		return map[string]string{}
	}

	sqlQuery := fmt.Sprintf(
		`SELECT _id::text, COALESCE(CAST(%s AS text), _id::text) AS label
		 FROM %s
		 WHERE _id IN (%s)`,
		quotedLabel,
		quotedTable,
		strings.Join(placeholders, ", "),
	)
	rows, err := db.Pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return map[string]string{}
	}
	defer rows.Close()

	labels := make(map[string]string, len(ids))
	for rows.Next() {
		var id string
		var label string
		if err := rows.Scan(&id, &label); err != nil {
			continue
		}
		labels[id] = label
	}
	return labels
}

func truncateListCellValue(value, dataType string) string {
	if value == "<nil>" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "long_text", "markdown", "json", "jsonb":
		const maxLen = 120
		runes := []rune(value)
		if len(runes) > maxLen {
			return strings.TrimSpace(string(runes[:maxLen])) + "..."
		}
	}
	return value
}

func orderedListColumns(ctx context.Context, tableName string, columns []string) []string {
	_, _, list, ok, err := db.FindYAMLDefaultListByTable(ctx, tableName)
	if err != nil || !ok {
		return columns
	}
	return applyPreferredListOrder(columns, list.Columns)
}

func applyPreferredListOrder(columns []string, preferred []string) []string {
	available := make(map[string]bool, len(columns))
	hidden := make([]string, 0, len(columns))
	for _, column := range columns {
		available[column] = true
		if strings.HasPrefix(column, "_") {
			hidden = append(hidden, column)
		}
	}

	ordered := make([]string, 0, len(columns))
	seen := map[string]bool{}
	for _, column := range preferred {
		if available[column] && !seen[column] {
			ordered = append(ordered, column)
			seen[column] = true
		}
	}
	for _, column := range columns {
		if strings.HasPrefix(column, "_") || seen[column] {
			continue
		}
		ordered = append(ordered, column)
		seen[column] = true
	}
	for _, column := range hidden {
		if seen[column] {
			continue
		}
		ordered = append(ordered, column)
		seen[column] = true
	}
	return ordered
}

func loadListViewServerDefaults(r *http.Request, tableName string) listViewServerDefaults {
	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		return listViewServerDefaults{}
	}

	key := listViewPreferenceKey(r, tableName)
	raw, err := db.GetUserPreference(context.Background(), userID, listViewPreferenceNamespace, key)
	if err != nil || len(raw) == 0 {
		return listViewServerDefaults{}
	}

	var state listViewPersistedState
	if err := json.Unmarshal(raw, &state); err != nil {
		return listViewServerDefaults{}
	}
	return state.ServerDefaults
}

func defaultSortColumn(columns []string) string {
	for _, col := range columns {
		if col == "_created_at" {
			return col
		}
	}
	if len(columns) == 0 {
		return "_id"
	}
	return columns[0]
}

func parseIntRange(raw string, fallback, min, max int) int {
	val, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func buildTableViewData(w http.ResponseWriter, r *http.Request, route tableRoute, tableName string, table db.Table, data []map[string]any, serverFilter, sortColumn, sortDirection string, page, pageSize, totalRows, totalPages int, countExact bool, prevURL, nextURL string, defaults listViewServerDefaults, showDeleted bool, queryColumns []tableQueryColumn, queryMode, queryError string, querySources []string, canCreate bool) map[string]any {
	tableLabel := strings.TrimSpace(table.LABEL_PLURAL)
	if tableLabel == "" {
		tableLabel = tableName
	}
	viewData := newViewData(w, r, "/t/"+route.RouteName, "Table: "+tableLabel, "Builder")
	viewData["Uri"] = r.URL.RequestURI()
	viewData["TableName"] = tableName
	viewData["TableRouteName"] = route.RouteName
	viewData["TableLabel"] = tableLabel
	viewData["TableDescription"] = strings.TrimSpace(table.DESCRIPTION)
	viewData["Rows"] = data
	viewData["ServerQuery"] = serverFilter
	viewData["SortColumn"] = sortColumn
	viewData["SortDirection"] = sortDirection
	viewData["Page"] = page
	viewData["PageSize"] = pageSize
	viewData["TotalRows"] = totalRows
	viewData["TotalPages"] = totalPages
	viewData["PaginationCountExact"] = countExact
	viewData["HasPrev"] = page > 1
	viewData["HasNext"] = strings.TrimSpace(nextURL) != ""
	viewData["PrevPageURL"] = prevURL
	viewData["NextPageURL"] = nextURL
	viewData["TableCanCreate"] = canCreate
	viewData["TableCreateURL"] = newRecordURL(route)
	viewData["HasStoredDefaults"] = defaults.Sort != "" || defaults.Query != "" || defaults.PageSize > 0
	viewData["ShowDeleted"] = showDeleted
	viewData["QueryColumns"] = queryColumns
	viewData["QueryMode"] = queryMode
	viewData["QueryError"] = queryError
	viewData["ListSourceTables"] = querySources
	if strings.EqualFold(tableName, "base_task") {
		viewData["TaskBoardURL"] = taskBoardURL()
	}
	return viewData
}

func canCreateForTable(ctx context.Context, userID, tableName string, securityEvaluator *db.TableSecurityEvaluator, appID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" || db.IsImmutableTableName(tableName) {
		return false
	}
	allowed, err := db.UserHasPermission(ctx, userID, "write", strings.TrimSpace(appID))
	if err != nil || !allowed {
		return false
	}
	if securityEvaluator != nil && !securityEvaluator.AllowsRecord("C", map[string]any{}) {
		return false
	}
	return true
}

func loadSecureListPage(ctx context.Context, tableName string, schemaCols []string, query string, args []any, page, pageSize int, evaluator *db.TableSecurityEvaluator) (secureListPageResult, error) {
	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return secureListPageResult{}, err
	}
	defer rows.Close()

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	targetStart := (page - 1) * pageSize
	result := secureListPageResult{
		Page:       page,
		Rows:       []map[string]any{},
		TotalPages: 1,
		CountExact: true,
	}
	pageRows := make([]map[string]any, 0, pageSize)
	lastPageRows := make([]map[string]any, 0, pageSize)
	readableCount := 0

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return secureListPageResult{}, err
		}
		row := mapListRowValues(schemaCols, values, tableName)
		filteredRow, readable := db.FilterReadableRecord(evaluator, row)
		if !readable {
			continue
		}

		readableCount++
		if len(lastPageRows) == pageSize {
			lastPageRows = append(lastPageRows[1:], filteredRow)
		} else {
			lastPageRows = append(lastPageRows, filteredRow)
		}

		if readableCount <= targetStart {
			continue
		}
		if len(pageRows) < pageSize {
			pageRows = append(pageRows, filteredRow)
			continue
		}

		result.HasNext = true
		result.CountExact = false
		result.Rows = pageRows
		result.TotalRows = targetStart + len(pageRows) + 1
		result.TotalPages = page + 1
		return result, nil
	}
	if err := rows.Err(); err != nil {
		return secureListPageResult{}, err
	}

	result.TotalRows = readableCount
	if readableCount > 0 {
		result.TotalPages = (readableCount + pageSize - 1) / pageSize
	}
	if result.TotalPages < 1 {
		result.TotalPages = 1
	}
	if page > result.TotalPages {
		result.Page = result.TotalPages
		result.Rows = append([]map[string]any(nil), lastPageRows...)
		result.HasNext = false
		return result, nil
	}
	result.Rows = pageRows
	result.HasNext = result.Page < result.TotalPages
	return result, nil
}

func paginationURLs(values url.Values, tableName string, page, totalPages int, hasNext, countExact bool) (string, string) {
	prev := cloneURLValues(values)
	next := cloneURLValues(values)

	prevURL := ""
	nextURL := ""
	if page > 1 {
		prev.Set("page", strconv.Itoa(page-1))
		prevURL = "/t/" + tableName
		if encoded := prev.Encode(); encoded != "" {
			prevURL += "?" + encoded
		}
	}
	if hasNext || (countExact && page < totalPages) {
		next.Set("page", strconv.Itoa(page+1))
		nextURL = "/t/" + tableName
		if encoded := next.Encode(); encoded != "" {
			nextURL += "?" + encoded
		}
	}
	return prevURL, nextURL
}

func cloneURLValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for k, vs := range values {
		cp := make([]string, len(vs))
		copy(cp, vs)
		cloned[k] = cp
	}
	return cloned
}
