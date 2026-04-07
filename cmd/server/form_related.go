package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/utils"
)

const formRelatedSectionLimit = 12

type formRelatedCell struct {
	Label string
	Value string
}

type formRelatedRow struct {
	ID    string
	Href  string
	Cells []formRelatedCell
}

type formRelatedSection struct {
	TableName       string
	TableLabel      string
	ReferenceColumn string
	ReferenceLabel  string
	Summary         string
	ViewAllURL      string
	Count           int
	HasMore         bool
	Rows            []formRelatedRow
}

type formRelatedSectionConfig struct {
	TableName      string
	ReferenceField string
	Label          string
	Columns        []string
}

type formRelatedRowTarget struct {
	TableName  string
	ColumnName string
}

type formRelatedLoadCaches struct {
	tableExists        map[string]bool
	orderedListColumns map[string][]string
	userID             string
	securityEvaluators map[string]*db.TableSecurityEvaluator
	securityLoaded     map[string]bool
}

func loadFormRelatedSections(ctx context.Context, r *http.Request, currentTable, currentID string) []formRelatedSection {
	currentTable = strings.TrimSpace(strings.ToLower(currentTable))
	currentID = strings.TrimSpace(currentID)
	if currentTable == "" || currentID == "" || currentID == "new" {
		return nil
	}

	recordIDValue, err := db.ParseRecordIDValue(currentTable, currentID)
	if err != nil {
		return nil
	}

	canViewAdminTables := currentUserCanViewAdminTables(ctx, r)
	configs := loadFormRelatedSectionConfigs(ctx, currentTable)
	caches := &formRelatedLoadCaches{
		tableExists:        map[string]bool{},
		orderedListColumns: map[string][]string{},
		userID:             strings.TrimSpace(auth.UserIDFromRequest(r)),
		securityEvaluators: map[string]*db.TableSecurityEvaluator{},
		securityLoaded:     map[string]bool{},
	}
	if configuredTargets := configuredFormRelatedTargets(configs); len(configuredTargets) > 0 {
		sections := make([]formRelatedSection, 0, len(configuredTargets))
		for _, config := range configuredTargets {
			tableName := strings.TrimSpace(strings.ToLower(config.TableName))
			if tableName == "" {
				continue
			}
			if db.IsAdminOnlyTableName(tableName) && !canViewAdminTables {
				continue
			}

			view := db.GetViewContext(ctx, tableName)
			if view.Table == nil {
				continue
			}

			relationColumn, ok := findFormRelatedColumn(view.Columns, config.ReferenceField)
			if !ok {
				continue
			}
			if target := relatedReferenceTarget(ctx, tableName, relationColumn, caches); target != currentTable {
				continue
			}

			section, ok := loadFormRelatedSection(ctx, currentID, recordIDValue, view, relationColumn, config, caches)
			if ok {
				sections = append(sections, section)
			}
		}
		sortFormRelatedSections(sections)
		return sections
	}

	tableNames, err := db.ListPhysicalBaseTables(ctx)
	if err != nil {
		return nil
	}

	sections := make([]formRelatedSection, 0, 4)
	for _, tableName := range tableNames {
		if tableName == "" {
			continue
		}
		if db.IsAdminOnlyTableName(tableName) && !canViewAdminTables {
			continue
		}

		view := db.GetViewContext(ctx, tableName)
		if view.Table == nil {
			continue
		}

		for _, column := range relatedReferenceColumns(ctx, tableName, view.Columns, currentTable, caches) {
			config := configs[formRelatedConfigKey(tableName, column.NAME)]
			section, ok := loadFormRelatedSection(ctx, currentID, recordIDValue, view, column, config, caches)
			if ok {
				sections = append(sections, section)
			}
		}
	}

	sortFormRelatedSections(sections)
	return sections
}

func loadFormRelatedSectionConfigs(ctx context.Context, currentTable string) map[string]formRelatedSectionConfig {
	currentTable = strings.TrimSpace(strings.ToLower(currentTable))
	if currentTable == "" {
		return nil
	}

	_, table, ok, err := db.FindYAMLTableByName(ctx, currentTable)
	if err != nil || !ok {
		return nil
	}

	configs := make(map[string]formRelatedSectionConfig, len(table.RelatedLists))
	for _, related := range table.RelatedLists {
		key := formRelatedConfigKey(related.Table, related.ReferenceField)
		if key == "" {
			continue
		}
		configs[key] = formRelatedSectionConfig{
			TableName:      strings.TrimSpace(strings.ToLower(related.Table)),
			ReferenceField: strings.TrimSpace(strings.ToLower(related.ReferenceField)),
			Label:          strings.TrimSpace(related.Label),
			Columns:        append([]string(nil), related.Columns...),
		}
	}
	return configs
}

func configuredFormRelatedTargets(configs map[string]formRelatedSectionConfig) []formRelatedSectionConfig {
	if len(configs) == 0 {
		return nil
	}

	targets := make([]formRelatedSectionConfig, 0, len(configs))
	for _, config := range configs {
		if strings.TrimSpace(config.TableName) == "" || strings.TrimSpace(config.ReferenceField) == "" {
			continue
		}
		targets = append(targets, config)
	}
	sort.Slice(targets, func(i, j int) bool {
		leftTable := strings.ToLower(strings.TrimSpace(targets[i].TableName))
		rightTable := strings.ToLower(strings.TrimSpace(targets[j].TableName))
		if leftTable == rightTable {
			return strings.ToLower(strings.TrimSpace(targets[i].ReferenceField)) < strings.ToLower(strings.TrimSpace(targets[j].ReferenceField))
		}
		return leftTable < rightTable
	})
	return targets
}

func sortFormRelatedSections(sections []formRelatedSection) {
	sort.Slice(sections, func(i, j int) bool {
		leftLabel := strings.ToLower(strings.TrimSpace(sections[i].TableLabel))
		rightLabel := strings.ToLower(strings.TrimSpace(sections[j].TableLabel))
		if leftLabel == rightLabel {
			return strings.ToLower(sections[i].ReferenceLabel) < strings.ToLower(sections[j].ReferenceLabel)
		}
		return leftLabel < rightLabel
	})
}

func formRelatedConfigKey(tableName, referenceField string) string {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	referenceField = strings.TrimSpace(strings.ToLower(referenceField))
	if tableName == "" || referenceField == "" {
		return ""
	}
	return tableName + ":" + referenceField
}

func currentUserCanViewAdminTables(ctx context.Context, r *http.Request) bool {
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		return false
	}
	allowed, err := db.UserHasGlobalPermission(ctx, userID, "admin")
	return err == nil && allowed
}

func loadFormRelatedSecurityEvaluator(ctx context.Context, tableName string, caches *formRelatedLoadCaches) (*db.TableSecurityEvaluator, bool) {
	if caches == nil {
		return nil, true
	}
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		return nil, true
	}
	if caches.securityLoaded[tableName] {
		return caches.securityEvaluators[tableName], true
	}
	evaluator, err := db.LoadTableSecurityEvaluator(ctx, tableName, caches.userID)
	if err != nil {
		return nil, false
	}
	caches.securityEvaluators[tableName] = evaluator
	caches.securityLoaded[tableName] = true
	return evaluator, true
}

func findFormRelatedColumn(columns []db.Column, columnName string) (db.Column, bool) {
	columnName = strings.TrimSpace(strings.ToLower(columnName))
	if columnName == "" {
		return db.Column{}, false
	}
	for _, column := range columns {
		if strings.EqualFold(strings.TrimSpace(column.NAME), columnName) {
			return column, true
		}
	}
	return db.Column{}, false
}

func relatedReferenceColumns(ctx context.Context, tableName string, columns []db.Column, currentTable string, caches *formRelatedLoadCaches) []db.Column {
	matches := make([]db.Column, 0, 2)
	for _, column := range columns {
		target := relatedReferenceTarget(ctx, tableName, column, caches)
		if target == currentTable {
			matches = append(matches, column)
		}
	}
	return matches
}

func relatedReferenceTarget(ctx context.Context, tableName string, column db.Column, caches *formRelatedLoadCaches) string {
	if strings.EqualFold(strings.TrimSpace(column.NAME), "_id") {
		return ""
	}
	if column.REFERENCE_TABLE.Valid {
		return strings.TrimSpace(strings.ToLower(column.REFERENCE_TABLE.String))
	}
	if caches == nil {
		return strings.TrimSpace(strings.ToLower(inferReferenceTable(tableName, column.NAME)))
	}
	return strings.TrimSpace(strings.ToLower(inferReferenceTableCached(ctx, tableName, column.NAME, caches.tableExists)))
}

func loadFormRelatedSection(ctx context.Context, currentID string, recordIDValue any, view db.View, relationColumn db.Column, config formRelatedSectionConfig, caches *formRelatedLoadCaches) (formRelatedSection, bool) {
	tableName := strings.TrimSpace(view.Table.NAME)
	if tableName == "" {
		return formRelatedSection{}, false
	}
	securityEvaluator, ok := loadFormRelatedSecurityEvaluator(ctx, tableName, caches)
	if !ok {
		return formRelatedSection{}, false
	}
	secureRead := securityEvaluator != nil && securityEvaluator.HasReadAccessControls()

	displayColumns := selectFormRelatedColumns(ctx, tableName, view.Columns, config.Columns, caches)
	if len(displayColumns) == 0 {
		return formRelatedSection{}, false
	}
	rowTarget := resolveFormRelatedRowTarget(ctx, tableName, view.Columns, displayColumns, config, caches)

	selectColumns := append([]string(nil), displayColumns...)
	hasRecordID := hasColumnName(view.Columns, "_id")
	if hasRecordID && !containsColumnName(selectColumns, "_id") {
		selectColumns = append(selectColumns, "_id")
	}

	quotedSelectColumns, err := db.QuoteIdentifierList(selectColumns)
	if err != nil {
		return formRelatedSection{}, false
	}
	quotedTable, err := db.QuoteIdentifier(tableName)
	if err != nil {
		return formRelatedSection{}, false
	}
	quotedReferenceColumn, err := db.QuoteIdentifier(relationColumn.NAME)
	if err != nil {
		return formRelatedSection{}, false
	}

	whereClause := quotedReferenceColumn + " = $1"
	if hasColumnName(view.Columns, "_deleted_at") {
		whereClause += ` AND "_deleted_at" IS NULL`
	}

	orderColumn, orderDirection := formRelatedOrder(view.Columns, displayColumns)
	quotedOrderColumn, err := db.QuoteIdentifier(orderColumn)
	if err != nil {
		return formRelatedSection{}, false
	}

	dataQuery := fmt.Sprintf(
		`SELECT %s
		 FROM %s
		 WHERE %s
		 ORDER BY %s %s`,
		strings.Join(quotedSelectColumns, ", "),
		quotedTable,
		whereClause,
		quotedOrderColumn,
		orderDirection,
	)
	if !secureRead {
		dataQuery = fmt.Sprintf(
			`SELECT %s, COUNT(*) OVER() AS __velm_total_count
			 FROM %s
			 WHERE %s
			 ORDER BY %s %s
			 LIMIT %d`,
			strings.Join(quotedSelectColumns, ", "),
			quotedTable,
			whereClause,
			quotedOrderColumn,
			orderDirection,
			formRelatedSectionLimit,
		)
	}
	rows, err := db.Pool.Query(ctx, dataQuery, recordIDValue)
	if err != nil {
		return formRelatedSection{}, false
	}
	defer rows.Close()

	rawRows := make([]map[string]any, 0, formRelatedSectionLimit)
	count := 0
	for rows.Next() {
		valueCount := len(selectColumns)
		if !secureRead {
			valueCount++
		}
		values := make([]any, valueCount)
		pointers := make([]any, valueCount)
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return formRelatedSection{}, false
		}

		row := make(map[string]any, len(selectColumns))
		for i, name := range selectColumns {
			row[name] = utils.NormalizeValue(values[i])
		}
		filteredRow, readable := db.FilterReadableRecord(securityEvaluator, row)
		if !readable {
			continue
		}
		if secureRead {
			count++
			if len(rawRows) < formRelatedSectionLimit {
				rawRows = append(rawRows, filteredRow)
			}
			continue
		}
		if count == 0 {
			switch total := utils.NormalizeValue(values[len(selectColumns)]).(type) {
			case int:
				count = total
			case int32:
				count = int(total)
			case int64:
				count = int(total)
			case float64:
				count = int(total)
			default:
				count = len(rawRows) + 1
			}
		}
		rawRows = append(rawRows, filteredRow)
	}
	if err := rows.Err(); err != nil {
		return formRelatedSection{}, false
	}
	if len(rawRows) == 0 {
		return formRelatedSection{}, false
	}
	if count == 0 {
		count = len(rawRows)
	}

	queryColumns := buildFormRelatedQueryColumns(ctx, tableName, view.Columns, displayColumns, caches)
	decoratedRows := decorateTableRows(ctx, queryColumns, rawRows)
	sectionRows := make([]formRelatedRow, 0, len(decoratedRows))
	for i, row := range decoratedRows {
		rawRow := rawRows[i]
		item := formRelatedRow{
			Cells: make([]formRelatedCell, 0, len(displayColumns)),
		}
		if targetID, ok := formRelatedRowHrefTarget(rawRow, rowTarget); ok {
			item.ID = targetID
			item.Href = "/f/" + rowTarget.TableName + "/" + targetID
		} else if hasRecordID {
			item.ID = strings.TrimSpace(fmt.Sprint(row["_id"]))
			if item.ID != "" && item.ID != "<nil>" {
				item.Href = "/f/" + tableName + "/" + item.ID
			} else {
				item.ID = ""
			}
		}
		for _, column := range queryColumns {
			item.Cells = append(item.Cells, formRelatedCell{
				Label: column.Label,
				Value: displayCellValue(row[column.Name]),
			})
		}
		sectionRows = append(sectionRows, item)
	}

	tableLabel := strings.TrimSpace(config.Label)
	if tableLabel == "" {
		tableLabel = strings.TrimSpace(view.Table.LABEL_PLURAL)
	}
	if tableLabel == "" {
		tableLabel = humanizeTimelineIdentifier(tableName)
	}
	referenceLabel := strings.TrimSpace(relationColumn.LABEL)
	if referenceLabel == "" {
		referenceLabel = humanizeTimelineIdentifier(relationColumn.NAME)
	}

	summary := fmt.Sprintf("%d related records", count)
	if count == 1 {
		summary = "1 related record"
	}
	if strings.TrimSpace(config.Label) == "" {
		summary += " via " + referenceLabel
	}

	filter := url.Values{}
	filter.Set("q", relationColumn.NAME+"="+currentID)

	return formRelatedSection{
		TableName:       tableName,
		TableLabel:      tableLabel,
		ReferenceColumn: relationColumn.NAME,
		ReferenceLabel:  referenceLabel,
		Summary:         summary,
		ViewAllURL:      "/t/" + tableName + "?" + filter.Encode(),
		Count:           count,
		HasMore:         count > len(sectionRows),
		Rows:            sectionRows,
	}, true
}

func selectFormRelatedColumns(ctx context.Context, tableName string, columns []db.Column, preferred []string, caches *formRelatedLoadCaches) []string {
	if selected := selectPreferredFormRelatedColumns(columns, preferred); len(selected) > 0 {
		return selected
	}

	allNames := make([]string, 0, len(columns))
	visible := make([]string, 0, len(columns))
	for _, column := range columns {
		allNames = append(allNames, column.NAME)
		if column.IS_HIDDEN {
			continue
		}
		visible = append(visible, column.NAME)
	}

	ordered := orderedListColumnsCached(ctx, tableName, allNames, caches)
	selected := make([]string, 0, 5)
	for _, name := range ordered {
		if !containsColumnName(visible, name) {
			continue
		}
		selected = append(selected, name)
		if len(selected) == 5 {
			return selected
		}
	}

	if len(selected) > 0 {
		return selected
	}
	if hasColumnName(columns, "_id") {
		return []string{"_id"}
	}
	if len(allNames) > 0 {
		return []string{allNames[0]}
	}
	return nil
}

func orderedListColumnsCached(ctx context.Context, tableName string, columns []string, caches *formRelatedLoadCaches) []string {
	if caches == nil {
		return orderedListColumns(ctx, tableName, columns)
	}
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if ordered, ok := caches.orderedListColumns[tableName]; ok {
		return append([]string(nil), ordered...)
	}
	ordered := orderedListColumns(ctx, tableName, columns)
	caches.orderedListColumns[tableName] = append([]string(nil), ordered...)
	return append([]string(nil), ordered...)
}

func buildFormRelatedQueryColumns(ctx context.Context, tableName string, columns []db.Column, selected []string, caches *formRelatedLoadCaches) []tableQueryColumn {
	metas := make(map[string]db.Column, len(columns))
	for _, col := range columns {
		metas[strings.ToLower(col.NAME)] = col
	}

	result := make([]tableQueryColumn, 0, len(selected))
	for _, name := range selected {
		meta, ok := metas[strings.ToLower(name)]
		label := name
		dataType := "text"
		if ok {
			if strings.TrimSpace(meta.LABEL) != "" {
				label = meta.LABEL
			}
			if strings.TrimSpace(meta.DATA_TYPE) != "" {
				dataType = meta.DATA_TYPE
			}
		}

		referenceTo := ""
		choiceLabels := map[string]string{}
		if ok {
			if meta.REFERENCE_TABLE.Valid {
				referenceTo = strings.TrimSpace(meta.REFERENCE_TABLE.String)
			}
			for _, choice := range meta.CHOICES {
				value := strings.TrimSpace(choice.Value)
				if value == "" {
					continue
				}
				label := strings.TrimSpace(choice.Label)
				if label == "" {
					label = value
				}
				choiceLabels[value] = label
			}
		}
		if referenceTo == "" && strings.HasSuffix(strings.ToLower(name), "_id") {
			if caches == nil {
				referenceTo = inferReferenceTable(tableName, name)
			} else {
				referenceTo = inferReferenceTableCached(ctx, tableName, name, caches.tableExists)
			}
		}
		if len(choiceLabels) == 0 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(dataType)), "enum:") {
			for _, item := range strings.Split(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(dataType)), "enum:"), "|") {
				value := strings.TrimSpace(item)
				if value == "" {
					continue
				}
				choiceLabels[value] = strings.ReplaceAll(value, "_", " ")
			}
		}

		result = append(result, tableQueryColumn{
			Name:         name,
			Label:        label,
			DataType:     dataType,
			IsDateLike:   isDateLikeDataType(dataType, name),
			IsNumber:     isNumericDataType(dataType, name),
			IsBoolean:    isBooleanDataType(dataType, name),
			InputKind:    queryInputKind(dataType, name),
			ReferenceTo:  referenceTo,
			ChoiceLabels: choiceLabels,
		})
	}
	return result
}

func selectPreferredFormRelatedColumns(columns []db.Column, preferred []string) []string {
	if len(preferred) == 0 {
		return nil
	}

	selected := make([]string, 0, minInt(len(preferred), 5))
	for _, name := range preferred {
		name = strings.TrimSpace(strings.ToLower(name))
		if name == "" || containsColumnName(selected, name) || !hasColumnName(columns, name) || isHiddenColumnName(columns, name) {
			continue
		}
		selected = append(selected, name)
		if len(selected) == 5 {
			break
		}
	}
	return selected
}

func resolveFormRelatedRowTarget(ctx context.Context, tableName string, columns []db.Column, displayColumns []string, config formRelatedSectionConfig, caches *formRelatedLoadCaches) formRelatedRowTarget {
	if len(config.Columns) == 0 || len(displayColumns) == 0 {
		return formRelatedRowTarget{}
	}

	targetColumn := strings.TrimSpace(strings.ToLower(displayColumns[0]))
	if targetColumn == "" {
		return formRelatedRowTarget{}
	}

	for _, column := range columns {
		if !strings.EqualFold(strings.TrimSpace(column.NAME), targetColumn) {
			continue
		}
		targetTable := relatedReferenceTarget(ctx, tableName, column, caches)
		if targetTable == "" {
			return formRelatedRowTarget{}
		}
		return formRelatedRowTarget{
			TableName:  targetTable,
			ColumnName: targetColumn,
		}
	}
	return formRelatedRowTarget{}
}

func formRelatedRowHrefTarget(row map[string]any, target formRelatedRowTarget) (string, bool) {
	if target.TableName == "" || target.ColumnName == "" {
		return "", false
	}
	value := strings.TrimSpace(fmt.Sprint(row[target.ColumnName]))
	if value == "" || value == "<nil>" {
		return "", false
	}
	return value, true
}

func formRelatedOrder(columns []db.Column, selected []string) (string, string) {
	for _, preferred := range []string{"_updated_at", "_created_at", "_id"} {
		if hasColumnName(columns, preferred) {
			return preferred, "DESC"
		}
	}
	if len(selected) == 0 {
		return "_id", "ASC"
	}
	return selected[0], "ASC"
}

func hasColumnName(columns []db.Column, name string) bool {
	for _, column := range columns {
		if strings.EqualFold(strings.TrimSpace(column.NAME), name) {
			return true
		}
	}
	return false
}

func isHiddenColumnName(columns []db.Column, name string) bool {
	for _, column := range columns {
		if strings.EqualFold(strings.TrimSpace(column.NAME), name) {
			return column.IS_HIDDEN
		}
	}
	return false
}

func containsColumnName(names []string, name string) bool {
	for _, item := range names {
		if strings.EqualFold(strings.TrimSpace(item), name) {
			return true
		}
	}
	return false
}

func displayCellValue(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return ""
	}
	return text
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
