package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"velm/internal/auth"
	"velm/internal/db"
)

const (
	taskBoardScopeMine       = "mine"
	taskBoardScopeGroups     = "groups"
	taskBoardUnspecifiedLane = "__unspecified__"
	taskBoardAssignedToMe    = "__me__"
	taskBoardAssignedNone    = "__unassigned__"
)

type taskBoardChoice struct {
	Value string
	Label string
}

type taskBoardGroupOption struct {
	ID    string
	Label string
}

type taskBoardUserOption struct {
	ID    string
	Label string
}

type taskBoardCard struct {
	ID             string
	Href           string
	UpdateURL      string
	Number         string
	Title          string
	PriorityLabel  string
	PriorityClass  string
	GroupID        string
	GroupLabel     string
	AssignedUserID string
	AssigneeLabel  string
	DueAtLabel     string
	AttentionLabel string
	AttentionClass string
	CanDrag        bool
}

type taskBoardLane struct {
	Value   string
	Label   string
	Count   int
	CanDrop bool
	Cards   []taskBoardCard
}

type taskBoardItem struct {
	State string
	Card  taskBoardCard
}

type taskBoardFilters struct {
	Scope      string
	GroupID    string
	Type       string
	Priority   string
	AssignedTo string
	Search     string
}

type taskBoardInsight struct {
	Label string
	Count int
	Class string
}

type taskBoardMetadata struct {
	StateChoices   []taskBoardChoice
	TypeOptions    []taskBoardChoice
	PriorityLabels map[string]string
}

type taskBoardQueryContext struct {
	QueryColumns []tableQueryColumn
	QuotedColMap map[string]string
	SourceTables []string
	SourceQuery  string
}

func handleTaskBoardPage(w http.ResponseWriter, r *http.Request) {
	route := taskBoardRoute()
	if !requireAdminTableAccess(w, r, route.TableName) {
		return
	}

	table := db.GetTable(route.TableName)
	if table.ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	handleTaskBoardView(w, r, route, table)
}

func taskBoardRoute() tableRoute {
	return tableRoute{
		RouteName: "base_task",
		TableName: "base_task",
	}
}

func handleTaskBoardView(w http.ResponseWriter, r *http.Request, route tableRoute, table db.Table) {
	ctx := r.Context()
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	queryVals := r.URL.Query()
	scope := normalizeTaskBoardScope(queryVals.Get("scope"))

	groups, err := loadTaskBoardGroups(ctx, userID)
	if err != nil {
		http.Error(w, "Failed to load task groups", http.StatusInternalServerError)
		return
	}
	selectedGroupID := normalizeTaskBoardGroup(queryVals.Get("group_id"), groups)

	queryContext, err := loadTaskBoardQueryContext(ctx, route.TableName)
	if err != nil {
		http.Error(w, "Failed to load task metadata", http.StatusInternalServerError)
		return
	}

	metadata := loadTaskBoardMetadata(table.ID)
	metadata.TypeOptions = loadTaskBoardTypeOptions(ctx, queryContext.SourceTables)
	groupMembers := map[string][]taskBoardUserOption{}
	if len(groups) > 0 {
		if loadedGroupMembers, loadErr := loadTaskBoardGroupMembers(ctx, groups); loadErr == nil {
			groupMembers = loadedGroupMembers
		}
	}

	taskType := normalizeTaskBoardType(queryVals.Get("work_type"), metadata.TypeOptions)
	priority := normalizeTaskBoardPriority(queryVals.Get("priority"), metadata.PriorityLabels)
	priorityOptions := taskBoardPriorityOptions(metadata.PriorityLabels)
	assigneeOptions := buildTaskBoardAssigneeOptions(userID, groupMembers)
	assignedTo := normalizeTaskBoardAssignedTo(queryVals.Get("assigned_to"), userID, assigneeOptions)
	serverFilter := normalizeTaskBoardSearch(queryVals.Get("q"))

	filters := taskBoardFilters{
		Scope:      scope,
		GroupID:    selectedGroupID,
		Type:       taskType,
		Priority:   priority,
		AssignedTo: assignedTo,
		Search:     serverFilter,
	}

	items, err := loadTaskBoardItems(ctx, userID, filters, metadata.PriorityLabels, queryContext)
	if err != nil {
		http.Error(w, "Failed to load task board", http.StatusInternalServerError)
		return
	}

	lanes := buildTaskBoardLanes(metadata.StateChoices, items)
	insights := buildTaskBoardInsights(items)
	canCreate := false
	appID := strings.TrimSpace(auth.AppIDFromRequest(r))
	if userID != "" && !db.IsImmutableTableName(route.TableName) {
		canCreate, err = db.UserHasPermission(ctx, userID, "write", appID)
		if err != nil {
			canCreate = false
		}
	}

	tableLabel := strings.TrimSpace(table.LABEL_PLURAL)
	if tableLabel == "" {
		tableLabel = route.TableName
	}

	viewData := newViewData(w, r, taskBoardURL(), "Task Board", "Operations")
	viewData["Uri"] = r.URL.RequestURI()
	viewData["TableName"] = route.TableName
	viewData["TableRouteName"] = route.RouteName
	viewData["TableLabel"] = tableLabel
	viewData["TableDescription"] = strings.TrimSpace(table.DESCRIPTION)
	viewData["TableCanCreate"] = canCreate
	viewData["TableCreateURL"] = newRecordURL(route)
	viewData["TaskBoard"] = true
	viewData["TaskBoardGroups"] = groups
	viewData["TaskBoardHasGroups"] = len(groups) > 0
	viewData["TaskBoardLanes"] = lanes
	viewData["TaskBoardListURL"] = taskBoardListURL(route)
	viewData["TaskBoardPath"] = taskBoardURL()
	viewData["TaskBoardResetURL"] = taskBoardResetURL(scope)
	viewData["TaskBoardScope"] = scope
	viewData["TaskBoardSelectedGroupID"] = selectedGroupID
	viewData["TaskBoardType"] = taskType
	viewData["TaskBoardTypeOptions"] = metadata.TypeOptions
	viewData["TaskBoardPriority"] = priority
	viewData["TaskBoardPriorityOptions"] = priorityOptions
	viewData["TaskBoardAssignedTo"] = assignedTo
	viewData["TaskBoardAssigneeOptions"] = assigneeOptions
	viewData["TaskBoardInsights"] = insights
	viewData["TaskBoardHasActiveFilters"] = taskBoardHasActiveFilters(filters)
	viewData["TaskBoardSummary"] = taskBoardSummary(scope, selectedGroupID, groups)
	viewData["TaskBoardTotalTasks"] = len(items)
	viewData["TaskBoardCanUpdate"] = canCreate
	viewData["TaskBoardUpdateURL"] = "/api/bulk/update/" + route.TableName
	viewData["TaskBoardGroupMembers"] = groupMembers
	viewData["TaskBoardSourceTables"] = queryContext.SourceTables
	viewData["ServerQuery"] = serverFilter
	viewData["QueryColumns"] = queryContext.QueryColumns

	if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
		http.Error(w, "Failed to render task board", http.StatusInternalServerError)
	}
}

func normalizeTaskBoardScope(raw string) string {
	if strings.ToLower(strings.TrimSpace(raw)) == taskBoardScopeGroups {
		return taskBoardScopeGroups
	}
	return taskBoardScopeMine
}

func normalizeTaskBoardGroup(raw string, groups []taskBoardGroupOption) string {
	groupID := strings.TrimSpace(raw)
	if groupID == "" {
		return ""
	}
	for _, group := range groups {
		if group.ID == groupID {
			return groupID
		}
	}
	return ""
}

func taskBoardListURL(route tableRoute) string {
	return "/t/" + route.RouteName
}

func taskBoardURL() string {
	return "/task"
}

func taskBoardResetURL(scope string) string {
	if normalizeTaskBoardScope(scope) == taskBoardScopeGroups {
		return taskBoardURL() + "?scope=" + taskBoardScopeGroups
	}
	return taskBoardURL()
}

func loadTaskBoardQueryContext(ctx context.Context, tableName string) (taskBoardQueryContext, error) {
	quotedTableName, err := db.QuoteIdentifier(tableName)
	if err != nil {
		return taskBoardQueryContext{}, err
	}

	schemaRows, err := db.Pool.Query(ctx, "SELECT * FROM "+quotedTableName+" LIMIT 0")
	if err != nil {
		return taskBoardQueryContext{}, err
	}
	defer schemaRows.Close()

	fieldDescriptions := schemaRows.FieldDescriptions()
	schemaCols := make([]string, len(fieldDescriptions))
	baseQuotedColMap := make(map[string]string, len(fieldDescriptions))
	filterQuotedColMap := make(map[string]string, len(fieldDescriptions))
	for i, fd := range fieldDescriptions {
		name := string(fd.Name)
		schemaCols[i] = name
		quotedCol, err := db.QuoteIdentifier(name)
		if err != nil {
			return taskBoardQueryContext{}, err
		}
		baseQuotedColMap[name] = quotedCol
		filterQuotedColMap[name] = "t." + quotedCol
	}

	sourceTables := db.ListTableQuerySources(ctx, tableName)
	if len(sourceTables) == 0 {
		sourceTables = []string{tableName}
	}
	sourceQuery, err := buildTableListSourceQuery(schemaCols, baseQuotedColMap, sourceTables)
	if err != nil {
		return taskBoardQueryContext{}, err
	}

	cols := orderedListColumns(ctx, tableName, append([]string(nil), schemaCols...))
	return taskBoardQueryContext{
		QueryColumns: loadTableQueryColumns(tableName, cols),
		QuotedColMap: filterQuotedColMap,
		SourceTables: sourceTables,
		SourceQuery:  sourceQuery,
	}, nil
}

func loadTaskBoardGroups(ctx context.Context, userID string) ([]taskBoardGroupOption, error) {
	if userID == "" {
		return nil, nil
	}

	rows, err := db.Pool.Query(ctx, `
		SELECT
			g._id::text,
			COALESCE(NULLIF(BTRIM(g.name), ''), g._id::text) AS label
		FROM _group_membership gm
		JOIN _group g ON g._id = gm.group_id
		WHERE gm.user_id::text = $1
		ORDER BY LOWER(COALESCE(NULLIF(BTRIM(g.name), ''), g._id::text)), g._id::text
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]taskBoardGroupOption, 0, 8)
	for rows.Next() {
		var group taskBoardGroupOption
		if err := rows.Scan(&group.ID, &group.Label); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func loadTaskBoardGroupMembers(ctx context.Context, groups []taskBoardGroupOption) (map[string][]taskBoardUserOption, error) {
	groupMembers := make(map[string][]taskBoardUserOption, len(groups))
	if len(groups) == 0 {
		return groupMembers, nil
	}

	args := make([]any, 0, len(groups))
	placeholders := make([]string, 0, len(groups))
	seen := make(map[string]bool, len(groups))
	for _, group := range groups {
		groupID := strings.TrimSpace(group.ID)
		if groupID == "" || seen[groupID] {
			continue
		}
		seen[groupID] = true
		args = append(args, groupID)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	if len(placeholders) == 0 {
		return groupMembers, nil
	}

	query := fmt.Sprintf(`
		SELECT
			gm.group_id::text,
			u._id::text,
			COALESCE(NULLIF(BTRIM(u.name), ''), u._id::text) AS label
		FROM _group_membership gm
		JOIN _user u ON u._id::text = gm.user_id::text
		WHERE gm.group_id::text IN (%s)
		ORDER BY
			gm.group_id::text,
			LOWER(COALESCE(NULLIF(BTRIM(u.name), ''), u._id::text)),
			u._id::text
	`, strings.Join(placeholders, ", "))

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			groupID string
			user    taskBoardUserOption
		)
		if err := rows.Scan(&groupID, &user.ID, &user.Label); err != nil {
			return nil, err
		}
		groupMembers[groupID] = append(groupMembers[groupID], user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return groupMembers, nil
}

func normalizeTaskBoardPriority(raw string, labels map[string]string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return ""
	}
	if taskBoardKnownPriority(value) {
		return value
	}
	if _, ok := labels[value]; ok {
		return value
	}
	return ""
}

func normalizeTaskBoardType(raw string, options []taskBoardChoice) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	for _, option := range options {
		if option.Value == value {
			return value
		}
	}
	return ""
}

func loadTaskBoardTypeOptions(ctx context.Context, sourceTables []string) []taskBoardChoice {
	options := []taskBoardChoice{{Value: "", Label: "Any Type"}}
	seen := map[string]bool{"": true}
	for _, tableName := range sourceTables {
		defaultValue := strings.TrimSpace(db.TaskTypeValueForTable(ctx, tableName))
		if defaultValue == "" || seen[defaultValue] {
			continue
		}
		seen[defaultValue] = true
		options = append(options, taskBoardChoice{Value: defaultValue, Label: defaultValue})
	}

	typeSelects := make([]string, 0, len(sourceTables))
	for _, tableName := range sourceTables {
		quotedTable, err := db.QuoteIdentifier(tableName)
		if err != nil {
			continue
		}
		typeSelects = append(typeSelects, `
			SELECT NULLIF(BTRIM(work_type), '') AS work_type
			FROM `+quotedTable+`
			WHERE _deleted_at IS NULL
			  AND NULLIF(BTRIM(work_type), '') IS NOT NULL`)
	}
	if len(typeSelects) == 0 {
		return options
	}

	rows, err := db.Pool.Query(ctx, `
		SELECT DISTINCT work_type
		FROM (`+strings.Join(typeSelects, ` UNION ALL `)+`) AS task_types
		ORDER BY 1`)
	if err != nil {
		return options
	}
	defer rows.Close()

	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return options
		}
		item := strings.TrimSpace(value.String)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		options = append(options, taskBoardChoice{Value: item, Label: item})
	}
	return options
}

func taskBoardPriorityOptions(labels map[string]string) []taskBoardChoice {
	order := []string{"very_high", "high", "medium", "low", "very_low"}
	options := []taskBoardChoice{{Value: "", Label: "Any Priority"}}
	for _, value := range order {
		label := strings.TrimSpace(labels[value])
		if label == "" {
			label = humanizeTimelineIdentifier(value)
		}
		options = append(options, taskBoardChoice{Value: value, Label: label})
	}
	return options
}

func taskBoardKnownPriority(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "very_low", "low", "medium", "high", "very_high":
		return true
	default:
		return false
	}
}

func buildTaskBoardAssigneeOptions(userID string, groupMembers map[string][]taskBoardUserOption) []taskBoardChoice {
	options := []taskBoardChoice{{Value: "", Label: "Anyone"}}
	if strings.TrimSpace(userID) != "" {
		options = append(options, taskBoardChoice{Value: taskBoardAssignedToMe, Label: "Me"})
	}
	options = append(options, taskBoardChoice{Value: taskBoardAssignedNone, Label: "Unassigned"})

	seen := map[string]bool{
		"":                        true,
		strings.TrimSpace(userID): true,
		taskBoardAssignedToMe:     true,
		taskBoardAssignedNone:     true,
	}
	users := make([]taskBoardUserOption, 0, 16)
	for _, members := range groupMembers {
		for _, member := range members {
			memberID := strings.TrimSpace(member.ID)
			if seen[memberID] {
				continue
			}
			seen[memberID] = true
			users = append(users, taskBoardUserOption{
				ID:    memberID,
				Label: strings.TrimSpace(member.Label),
			})
		}
	}
	sort.Slice(users, func(i, j int) bool {
		leftLabel := strings.ToLower(strings.TrimSpace(users[i].Label))
		rightLabel := strings.ToLower(strings.TrimSpace(users[j].Label))
		if leftLabel != rightLabel {
			return leftLabel < rightLabel
		}
		return users[i].ID < users[j].ID
	})
	for _, user := range users {
		label := user.Label
		if label == "" {
			label = user.ID
		}
		options = append(options, taskBoardChoice{Value: user.ID, Label: label})
	}
	return options
}

func normalizeTaskBoardAssignedTo(raw, userID string, options []taskBoardChoice) string {
	value := strings.TrimSpace(raw)
	switch value {
	case "":
		return ""
	case taskBoardAssignedNone:
		return taskBoardAssignedNone
	case taskBoardAssignedToMe:
		return taskBoardAssignedToMe
	}
	if value == strings.TrimSpace(userID) {
		return taskBoardAssignedToMe
	}
	for _, option := range options {
		if option.Value == value {
			return value
		}
	}
	return ""
}

func normalizeTaskBoardSearch(raw string) string {
	value := strings.TrimSpace(raw)
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}

func taskBoardHasActiveFilters(filters taskBoardFilters) bool {
	return strings.TrimSpace(filters.GroupID) != "" ||
		strings.TrimSpace(filters.Type) != "" ||
		strings.TrimSpace(filters.Priority) != "" ||
		strings.TrimSpace(filters.AssignedTo) != "" ||
		strings.TrimSpace(filters.Search) != ""
}

func loadTaskBoardMetadata(tableID string) taskBoardMetadata {
	metadata := defaultTaskBoardMetadata()
	if strings.TrimSpace(tableID) == "" {
		return metadata
	}

	columns, err := db.GetColumns(tableID)
	if err != nil {
		return metadata
	}

	for _, column := range columns {
		switch strings.ToLower(strings.TrimSpace(column.NAME)) {
		case "state":
			if len(column.CHOICES) > 0 {
				choices := make([]taskBoardChoice, 0, len(column.CHOICES))
				for _, choice := range column.CHOICES {
					value := normalizeTaskBoardLaneValue(choice.Value)
					if !taskBoardShowsState(value) {
						continue
					}
					label := strings.TrimSpace(choice.Label)
					if label == "" {
						label = humanizeTimelineIdentifier(value)
					}
					choices = append(choices, taskBoardChoice{Value: value, Label: label})
				}
				if len(choices) > 0 {
					metadata.StateChoices = choices
				}
			}
		case "priority":
			if len(column.CHOICES) > 0 {
				labels := make(map[string]string, len(column.CHOICES))
				for _, choice := range column.CHOICES {
					value := strings.ToLower(strings.TrimSpace(choice.Value))
					if value == "" {
						continue
					}
					label := strings.TrimSpace(choice.Label)
					if label == "" {
						label = humanizeTimelineIdentifier(value)
					}
					labels[value] = label
				}
				if len(labels) > 0 {
					metadata.PriorityLabels = labels
				}
			}
		}
	}

	return metadata
}

func defaultTaskBoardMetadata() taskBoardMetadata {
	stateChoices := []taskBoardChoice{
		{Value: "new", Label: "New"},
		{Value: "pending", Label: "Pending"},
		{Value: "in_progress", Label: "In Progress"},
		{Value: "ready_to_close", Label: "Ready to Close"},
	}
	return taskBoardMetadata{
		StateChoices: stateChoices,
		PriorityLabels: map[string]string{
			"very_low":  "Very Low",
			"low":       "Low",
			"medium":    "Medium",
			"high":      "High",
			"very_high": "Very High",
		},
	}
}

func loadTaskBoardItems(ctx context.Context, userID string, filters taskBoardFilters, priorityLabels map[string]string, queryContext taskBoardQueryContext) ([]taskBoardItem, error) {
	if userID == "" {
		return nil, nil
	}
	securityEvaluators := map[string]*db.TableSecurityEvaluator{}
	securityLoaded := map[string]bool{}

	query := `
		SELECT
			t._id::text,
			COALESCE(NULLIF(BTRIM(t._record_table), ''), 'base_task') AS record_table,
			COALESCE(NULLIF(BTRIM(t.number), ''), t._id::text) AS number,
			COALESCE(NULLIF(BTRIM(t.title), ''), '(untitled task)') AS title,
			COALESCE(NULLIF(BTRIM(t.state), ''), '') AS state,
			COALESCE(NULLIF(BTRIM(t.priority), ''), '') AS priority,
			COALESCE(t.assignment_group_id::text, '') AS group_id,
			COALESCE(NULLIF(BTRIM(g.name), ''), '') AS group_label,
			COALESCE(t.assigned_user_id::text, '') AS assigned_user_id,
			COALESCE(NULLIF(BTRIM(u.name), ''), '') AS assignee_label,
			t.due_at,
			t._updated_at
		FROM (` + queryContext.SourceQuery + `) t
		LEFT JOIN _group g ON g._id = t.assignment_group_id
		LEFT JOIN _user u ON u._id = t.assigned_user_id
		WHERE t._deleted_at IS NULL
		  AND COALESCE(NULLIF(BTRIM(t.state), ''), 'new') <> 'closed'`

	args := make([]any, 0, 6)
	switch filters.Scope {
	case taskBoardScopeGroups:
		if filters.GroupID != "" {
			query += fmt.Sprintf(" AND t.assignment_group_id::text = $%d", len(args)+1)
			args = append(args, filters.GroupID)
		} else {
			query += fmt.Sprintf(` AND EXISTS (
				SELECT 1
				FROM _group_membership gm
				WHERE gm.group_id = t.assignment_group_id
				  AND gm.user_id::text = $%d
			)`, len(args)+1)
			args = append(args, userID)
		}
	default:
		if filters.GroupID != "" {
			query += fmt.Sprintf(" AND t.assignment_group_id::text = $%d", len(args)+1)
			args = append(args, filters.GroupID)
		}
		query += fmt.Sprintf(` AND (
			t.assigned_user_id::text = $%d
			OR (
				t.assigned_user_id IS NULL
				AND EXISTS (
					SELECT 1
					FROM _group_membership gm
					WHERE gm.group_id = t.assignment_group_id
					  AND gm.user_id::text = $%d
				)
			)
		)`, len(args)+1, len(args)+1)
		args = append(args, userID)
	}

	if filters.Type != "" {
		query += fmt.Sprintf(" AND COALESCE(NULLIF(BTRIM(t.work_type), ''), '') = $%d", len(args)+1)
		args = append(args, filters.Type)
	}

	if filters.Priority != "" {
		query += fmt.Sprintf(" AND COALESCE(NULLIF(BTRIM(t.priority), ''), '') = $%d", len(args)+1)
		args = append(args, filters.Priority)
	}

	switch filters.AssignedTo {
	case taskBoardAssignedToMe:
		query += fmt.Sprintf(" AND t.assigned_user_id::text = $%d", len(args)+1)
		args = append(args, userID)
	case taskBoardAssignedNone:
		query += " AND t.assigned_user_id IS NULL"
	case "":
	default:
		query += fmt.Sprintf(" AND t.assigned_user_id::text = $%d", len(args)+1)
		args = append(args, filters.AssignedTo)
	}

	if filters.Search != "" {
		filterClause, filterArgs, _, err := buildTableWhereClause(filters.Search, queryContext.QueryColumns, queryContext.QuotedColMap)
		if err != nil {
			filterClause = buildPlainTextWhereClause(queryContext.QueryColumns, queryContext.QuotedColMap)
			filterArgs = []any{"%" + filters.Search + "%"}
		}
		query += " AND " + filterClause
		args = append(args, filterArgs...)
	}

	query += `
		ORDER BY
			CASE t.priority
				WHEN 'very_high' THEN 1
				WHEN 'high' THEN 2
				WHEN 'medium' THEN 3
				WHEN 'low' THEN 4
				WHEN 'very_low' THEN 5
				ELSE 6
			END,
			CASE WHEN t.due_at IS NULL THEN 1 ELSE 0 END,
			t.due_at ASC,
			t._updated_at DESC,
			t.number ASC`

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]taskBoardItem, 0, 24)
	now := time.Now()
	for rows.Next() {
		var (
			id             string
			recordTable    string
			number         string
			title          string
			state          string
			priority       string
			groupID        string
			groupLabel     string
			assignedUserID string
			assigneeLabel  string
			dueAt          sql.NullTime
			updatedAt      sql.NullTime
		)
		if err := rows.Scan(&id, &recordTable, &number, &title, &state, &priority, &groupID, &groupLabel, &assignedUserID, &assigneeLabel, &dueAt, &updatedAt); err != nil {
			return nil, err
		}
		recordTable = strings.TrimSpace(recordTable)
		if recordTable == "" {
			recordTable = "base_task"
		}
		if !securityLoaded[recordTable] {
			evaluator, err := db.LoadTableSecurityEvaluator(ctx, recordTable, userID)
			if err != nil {
				return nil, err
			}
			securityEvaluators[recordTable] = evaluator
			securityLoaded[recordTable] = true
		}
		rawRecord := map[string]any{
			"_id":                 id,
			"_record_table":       recordTable,
			"number":              strings.TrimSpace(number),
			"title":               strings.TrimSpace(title),
			"state":               strings.TrimSpace(state),
			"priority":            strings.TrimSpace(priority),
			"assignment_group_id": strings.TrimSpace(groupID),
			"assigned_user_id":    strings.TrimSpace(assignedUserID),
			"due_at":              dueAt.Time,
			"_updated_at":         updatedAt.Time,
		}
		filteredRecord, readable := db.FilterReadableRecord(securityEvaluators[recordTable], rawRecord)
		if !readable {
			continue
		}
		filteredState, stateVisible := filteredRecord["state"]
		if !stateVisible || filteredState == nil {
			continue
		}
		numberValue := strings.TrimSpace(fmt.Sprint(filteredRecord["number"]))
		titleValue := strings.TrimSpace(fmt.Sprint(filteredRecord["title"]))
		priorityValue := strings.TrimSpace(fmt.Sprint(filteredRecord["priority"]))
		groupIDValue := strings.TrimSpace(fmt.Sprint(filteredRecord["assignment_group_id"]))
		assignedUserIDValue := strings.TrimSpace(fmt.Sprint(filteredRecord["assigned_user_id"]))
		stateValue := strings.TrimSpace(fmt.Sprint(filteredState))
		groupLabelValue := strings.TrimSpace(groupLabel)
		if filteredRecord["assignment_group_id"] == nil {
			groupLabelValue = ""
		}
		assigneeLabelValue := strings.TrimSpace(assigneeLabel)
		if filteredRecord["assigned_user_id"] == nil {
			assigneeLabelValue = ""
		}

		card := taskBoardCard{
			ID:             id,
			Href:           buildRecordFormURL(recordTable, id),
			UpdateURL:      taskBoardBulkUpdateURL(recordTable),
			Number:         numberValue,
			Title:          titleValue,
			PriorityLabel:  taskBoardPriorityLabel(priorityValue, priorityLabels),
			PriorityClass:  taskBoardPriorityClass(priorityValue),
			GroupID:        groupIDValue,
			GroupLabel:     groupLabelValue,
			AssignedUserID: assignedUserIDValue,
			AssigneeLabel:  assigneeLabelValue,
			DueAtLabel:     formatTaskBoardTimestamp(dueAt, updatedAt),
			AttentionLabel: taskBoardAttentionLabel(now, dueAt, updatedAt),
			AttentionClass: taskBoardAttentionClass(now, dueAt, updatedAt),
			CanDrag:        assignedUserIDValue != "",
		}
		if card.Number == "" {
			card.Number = id
		}
		if card.Title == "" {
			card.Title = "(untitled task)"
		}

		items = append(items, taskBoardItem{
			State: normalizeTaskBoardLaneValue(stateValue),
			Card:  card,
		})
	}

	return items, rows.Err()
}

func taskBoardBulkUpdateURL(tableName string) string {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		tableName = "base_task"
	}
	return "/api/bulk/update/" + tableName
}

func buildTaskBoardLanes(stateChoices []taskBoardChoice, items []taskBoardItem) []taskBoardLane {
	lanes := make([]taskBoardLane, 0, len(stateChoices)+1)
	laneIndex := make(map[string]int, len(stateChoices)+1)

	for _, choice := range stateChoices {
		value := normalizeTaskBoardLaneValue(choice.Value)
		if _, exists := laneIndex[value]; exists {
			continue
		}
		label := strings.TrimSpace(choice.Label)
		if label == "" {
			label = taskBoardLaneLabel(value)
		}
		laneIndex[value] = len(lanes)
		lanes = append(lanes, taskBoardLane{
			Value:   value,
			Label:   label,
			CanDrop: true,
		})
	}

	unknownStates := make([]string, 0, 4)
	seenUnknown := map[string]bool{}
	for _, item := range items {
		if _, exists := laneIndex[item.State]; exists || seenUnknown[item.State] {
			continue
		}
		seenUnknown[item.State] = true
		unknownStates = append(unknownStates, item.State)
	}
	sort.Slice(unknownStates, func(i, j int) bool {
		leftRank := taskBoardStateSortRank(unknownStates[i])
		rightRank := taskBoardStateSortRank(unknownStates[j])
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return taskBoardLaneLabel(unknownStates[i]) < taskBoardLaneLabel(unknownStates[j])
	})
	for _, state := range unknownStates {
		laneIndex[state] = len(lanes)
		lanes = append(lanes, taskBoardLane{
			Value:   state,
			Label:   taskBoardLaneLabel(state),
			CanDrop: false,
		})
	}

	for _, item := range items {
		index, ok := laneIndex[item.State]
		if !ok {
			continue
		}
		lanes[index].Cards = append(lanes[index].Cards, item.Card)
	}
	for index := range lanes {
		lanes[index].Count = len(lanes[index].Cards)
	}

	return lanes
}

func buildTaskBoardInsights(items []taskBoardItem) []taskBoardInsight {
	overdueCount := 0
	staleCount := 0
	for _, item := range items {
		switch strings.TrimSpace(item.Card.AttentionLabel) {
		case "Overdue":
			overdueCount++
		default:
			if strings.HasPrefix(item.Card.AttentionLabel, "Stale ") {
				staleCount++
			}
		}
	}

	insights := make([]taskBoardInsight, 0, 3)
	if overdueCount > 0 {
		insights = append(insights, taskBoardInsight{Label: "Overdue", Count: overdueCount, Class: "ui-badge-danger"})
	}
	if staleCount > 0 {
		insights = append(insights, taskBoardInsight{Label: "Stale", Count: staleCount, Class: "ui-badge-warn"})
	}
	return insights
}

func taskBoardPriorityLabel(priority string, labels map[string]string) string {
	value := strings.ToLower(strings.TrimSpace(priority))
	if value == "" {
		return ""
	}
	if label := strings.TrimSpace(labels[value]); label != "" {
		return label
	}
	return humanizeTimelineIdentifier(value)
}

func taskBoardPriorityClass(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "very_high", "high":
		return "ui-badge-danger"
	case "medium":
		return "ui-badge-warn"
	default:
		return "ui-badge-neutral"
	}
}

func formatTaskBoardTimestamp(dueAt, updatedAt sql.NullTime) string {
	if dueAt.Valid {
		return "Due " + dueAt.Time.Local().Format("2006-01-02")
	}
	if updatedAt.Valid {
		return "Updated " + updatedAt.Time.Local().Format("2006-01-02")
	}
	return ""
}

func taskBoardAttentionLabel(now time.Time, dueAt, updatedAt sql.NullTime) string {
	label, _ := taskBoardAttention(now, dueAt, updatedAt)
	return label
}

func taskBoardAttentionClass(now time.Time, dueAt, updatedAt sql.NullTime) string {
	_, className := taskBoardAttention(now, dueAt, updatedAt)
	return className
}

func taskBoardAttention(now time.Time, dueAt, updatedAt sql.NullTime) (string, string) {
	if dueAt.Valid {
		if dueAt.Time.Before(now) {
			return "Overdue", "ui-badge-danger"
		}
		if dueAt.Time.Before(now.Add(48 * time.Hour)) {
			return "Due Soon", "ui-badge-warn"
		}
	}
	if updatedAt.Valid {
		staleDays := int(now.Sub(updatedAt.Time).Hours() / 24)
		if staleDays >= 7 {
			return fmt.Sprintf("Stale %dd", staleDays), "ui-badge-neutral"
		}
	}
	return "", ""
}

func taskBoardSummary(scope, groupID string, groups []taskBoardGroupOption) string {
	if scope == taskBoardScopeGroups {
		if label := taskBoardGroupLabel(groupID, groups); label != "" {
			return "Tasks for " + label
		}
		return "Tasks for all of my groups"
	}
	if label := taskBoardGroupLabel(groupID, groups); label != "" {
		return "Tasks assigned to me plus unassigned work for " + label
	}
	return "Tasks assigned to me plus unassigned work from my groups"
}

func taskBoardGroupLabel(groupID string, groups []taskBoardGroupOption) string {
	for _, group := range groups {
		if group.ID == groupID {
			return group.Label
		}
	}
	return ""
}

func normalizeTaskBoardLaneValue(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return taskBoardUnspecifiedLane
	}
	return normalized
}

func taskBoardLaneLabel(value string) string {
	if value == taskBoardUnspecifiedLane {
		return "Unspecified"
	}
	return humanizeTimelineIdentifier(value)
}

func taskBoardShowsState(value string) bool {
	switch normalizeTaskBoardLaneValue(value) {
	case taskBoardUnspecifiedLane, "closed", "done", "cancelled":
		return false
	default:
		return true
	}
}

func taskBoardStateSortRank(value string) int {
	switch normalizeTaskBoardLaneValue(value) {
	case "new", "ready", "triage":
		return 10
	case "pending", "blocked":
		return 20
	case "in_progress":
		return 30
	case "ready_to_close":
		return 40
	case "closed", "done", "cancelled":
		return 50
	case taskBoardUnspecifiedLane:
		return 60
	default:
		return 70
	}
}
