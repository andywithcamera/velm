package db

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

var baseTaskStateChoices = []ChoiceOption{
	{Value: "new", Label: "New"},
	{Value: "pending", Label: "Pending"},
	{Value: "in_progress", Label: "In Progress"},
	{Value: "ready_to_close", Label: "Ready to Close"},
	{Value: "closed", Label: "Closed"},
}

var baseTaskClosureReasonChoices = []ChoiceOption{
	{Value: "completed", Label: "Completed"},
	{Value: "cancelled", Label: "Cancelled"},
}

var baseTaskPriorityChoices = []ChoiceOption{
	{Value: "very_low", Label: "Very Low"},
	{Value: "low", Label: "Low"},
	{Value: "medium", Label: "Medium"},
	{Value: "high", Label: "High"},
	{Value: "very_high", Label: "Very High"},
}

var legacyBaseTaskTextReplacer = strings.NewReplacer(
	"Work Order States", "Task States",
	"Work Order State", "Task State",
	"Work Order Transitions", "Task Transitions",
	"Work Order Transition", "Task Transition",
	"Work Orders", "Tasks",
	"Work Order", "Task",
	"work order states", "task states",
	"work order state", "task state",
	"work order transitions", "task transitions",
	"work order transition", "task transition",
	"work orders", "tasks",
	"work order", "task",
)

func SyncBaseAppDefinitionTaskModel(ctx context.Context) error {
	app, err := GetActiveAppByName(ctx, "base")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "app not found") {
			return nil
		}
		return err
	}

	draft := cloneAppDefinition(app.DraftDefinition)
	if draft == nil {
		draft = cloneAppDefinition(app.Definition)
	}
	published := cloneAppDefinition(app.Definition)
	if published == nil {
		published = cloneAppDefinition(app.DraftDefinition)
	}
	if draft == nil && published == nil {
		return nil
	}

	draftChanged := simplifyBaseTaskModelDefinition(draft)
	publishedChanged := simplifyBaseTaskModelDefinition(published)
	if !draftChanged && !publishedChanged {
		return nil
	}

	if draft != nil {
		if err := prepareDefinitionForApp(app, draft); err != nil {
			return err
		}
		if err := validateAppDefinitionForApp(ctx, app, draft); err != nil {
			return err
		}
	}
	if published != nil {
		if err := prepareDefinitionForApp(app, published); err != nil {
			return err
		}
		if err := validateAppDefinitionForApp(ctx, app, published); err != nil {
			return err
		}
	}

	draftContent := strings.TrimSpace(app.DefinitionYAML)
	if draft != nil {
		content, err := yaml.Marshal(draft)
		if err != nil {
			return fmt.Errorf("marshal base draft definition: %w", err)
		}
		draftContent = string(content)
	}

	publishedContent := strings.TrimSpace(app.PublishedDefinitionYAML)
	if published != nil {
		content, err := yaml.Marshal(published)
		if err != nil {
			return fmt.Errorf("marshal base published definition: %w", err)
		}
		publishedContent = string(content)
	}

	_, err = Pool.Exec(ctx, `
		UPDATE _app
		SET definition_yaml = $2,
			published_definition_yaml = $3,
			definition_version = CASE
				WHEN COALESCE(definition_yaml, '') = $2 THEN definition_version
				ELSE GREATEST(definition_version, published_version) + 1
			END,
			published_version = CASE
				WHEN COALESCE(published_definition_yaml, '') = $3 THEN published_version
				ELSE GREATEST(definition_version, published_version) + 1
			END,
			_updated_at = NOW()
		WHERE name = $1 OR namespace = $1
	`, app.Name, draftContent, publishedContent)
	if err != nil {
		return fmt.Errorf("sync base app definition: %w", err)
	}

	return nil
}

func simplifyBaseTaskModelDefinition(definition *AppDefinition) bool {
	if definition == nil {
		return false
	}

	before, _ := yaml.Marshal(definition)
	rewriteLegacyBaseTaskDefinition(definition)

	tables := make([]AppDefinitionTable, 0, len(definition.Tables)+1)
	hasEntityLinkTable := false
	for _, table := range definition.Tables {
		switch table.Name {
		case "base_task_state", "base_task_transition":
			continue
		case "base_task":
			table.Extensible = true
			simplifyBaseTaskTable(&table)
			table.Forms = removeFieldFromForms(table.Forms, "entity_id")
			table.Forms = removeFieldFromForms(table.Forms, "board_rank")
			table.Forms = removeFieldFromForms(table.Forms, "resolved_at")
			table.Lists = removeFieldFromLists(table.Lists, "entity_id")
			table.Lists = removeFieldFromLists(table.Lists, "board_rank")
			table.Lists = removeFieldFromLists(table.Lists, "resolved_at")
			table.RelatedLists = removeFieldFromRelatedLists(table.RelatedLists, "board_rank")
			table.RelatedLists = removeFieldFromRelatedLists(table.RelatedLists, "resolved_at")
			table.RelatedLists = upsertRelatedList(table.RelatedLists, AppDefinitionRelatedList{
				Name:           "affected_entities",
				Label:          "Affected Entities",
				Table:          "base_task_entity",
				ReferenceField: "task_id",
				Columns:        []string{"entity_id"},
			})
		case "base_entity":
			table.Extensible = true
			simplifyBaseEntityTable(&table)
			table.RelatedLists = upsertRelatedList(table.RelatedLists, AppDefinitionRelatedList{
				Name:           "tasks",
				Label:          "Tasks",
				Table:          "base_task_entity",
				ReferenceField: "entity_id",
				Columns:        []string{"task_id"},
			})
		case "base_task_entity":
			table = baseTaskEntityDefinition()
			hasEntityLinkTable = true
		}
		table.Forms = replaceStateFieldInForms(table.Forms)
		table.Lists = replaceStateFieldInLists(table.Lists)
		table.RelatedLists = replaceStateFieldInRelatedLists(table.RelatedLists)
		if table.Name == "base_task" {
			table.Forms = upsertFieldAfterInForms(table.Forms, "closure_reason", "state")
		}
		tables = append(tables, table)
	}
	if !hasEntityLinkTable {
		tables = append(tables, baseTaskEntityDefinition())
	}
	definition.Tables = tables

	after, _ := yaml.Marshal(definition)
	return string(before) != string(after)
}

func simplifyBaseTaskTable(table *AppDefinitionTable) {
	if table == nil {
		return
	}

	columns := make([]AppDefinitionColumn, 0, len(table.Columns))
	insertAt := -1
	for _, column := range table.Columns {
		if column.Name == "state_id" || column.Name == "state" || column.Name == "closure_reason" || column.Name == "entity_id" || column.Name == "board_rank" || column.Name == "resolved_at" {
			continue
		}
		if column.Name == "number" {
			column.DataType = "autnumber"
			column.Prefix = "TASK"
			column.DefaultValue = ""
		}
		if column.Name == "work_type" {
			column.Label = "Task Type"
			column.DefaultValue = "TASK"
		}
		if column.Name == "priority" {
			column.DataType = "choice"
			column.DefaultValue = "medium"
			column.Choices = append([]ChoiceOption(nil), baseTaskPriorityChoices...)
		}
		columns = append(columns, column)
		if column.Name == "work_type" && insertAt == -1 {
			insertAt = len(columns)
		}
	}
	if insertAt == -1 {
		for i, column := range columns {
			if column.Name == "priority" {
				insertAt = i
				break
			}
		}
	}
	if insertAt == -1 {
		insertAt = len(columns)
	}

	stateColumn := AppDefinitionColumn{
		Name:         "state",
		Label:        "State",
		DataType:     "choice",
		IsNullable:   false,
		DefaultValue: "new",
		Choices:      append([]ChoiceOption(nil), baseTaskStateChoices...),
	}
	closureReasonColumn := AppDefinitionColumn{
		Name:          "closure_reason",
		Label:         "Closure Reason",
		DataType:      "choice",
		IsNullable:    true,
		DefaultValue:  "completed",
		ConditionExpr: "state=closed",
		Choices:       append([]ChoiceOption(nil), baseTaskClosureReasonChoices...),
	}

	columns = append(columns, AppDefinitionColumn{}, AppDefinitionColumn{})
	copy(columns[insertAt+2:], columns[insertAt:])
	columns[insertAt] = stateColumn
	columns[insertAt+1] = closureReasonColumn
	table.Columns = columns
}

func simplifyBaseEntityTable(table *AppDefinitionTable) {
	if table == nil {
		return
	}

	for i := range table.Columns {
		switch table.Columns[i].Name {
		case "number":
			table.Columns[i].DataType = "autnumber"
			table.Columns[i].Prefix = "ENT"
			table.Columns[i].DefaultValue = ""
		case "entity_type":
			table.Columns[i].DefaultValue = "item"
		case "lifecycle_state":
			table.Columns[i].DefaultValue = "active"
		case "operational_status":
			table.Columns[i].DefaultValue = "operational"
		case "criticality":
			table.Columns[i].DefaultValue = "p3"
		}
	}
}

func baseTaskEntityDefinition() AppDefinitionTable {
	return AppDefinitionTable{
		Name:          "base_task_entity",
		LabelSingular: "Affected Entity",
		LabelPlural:   "Affected Entities",
		Description:   "Links tasks to the entities they affect.",
		DisplayField:  "entity_id",
		Columns: []AppDefinitionColumn{
			{
				Name:           "task_id",
				Label:          "Task",
				DataType:       "reference",
				IsNullable:     false,
				ReferenceTable: "base_task",
			},
			{
				Name:           "entity_id",
				Label:          "Entity",
				DataType:       "reference",
				IsNullable:     false,
				ReferenceTable: "base_entity",
			},
		},
		Forms: []AppDefinitionForm{{
			Name:   "default",
			Label:  "Default",
			Fields: []string{"task_id", "entity_id"},
		}},
		Lists: []AppDefinitionList{{
			Name:    "default",
			Label:   "Default",
			Columns: []string{"task_id", "entity_id", "_updated_at"},
		}},
	}
}

func rewriteLegacyBaseTaskDefinition(definition *AppDefinition) {
	if definition == nil {
		return
	}

	definition.Label = normalizeBaseTaskText(definition.Label)
	definition.Description = normalizeBaseTaskText(definition.Description)
	for i, form := range definition.Forms {
		form.Table = normalizeBaseTaskIdentifier(form.Table)
		form.Label = normalizeBaseTaskText(form.Label)
		form.Description = normalizeBaseTaskText(form.Description)
		form.Fields = renameBaseTaskFieldNames(form.Fields)
		form.Layout = renameBaseTaskFieldNames(form.Layout)
		definition.Forms[i] = form
	}
	for i, trigger := range definition.Triggers {
		trigger.Table = normalizeBaseTaskIdentifier(trigger.Table)
		definition.Triggers[i] = trigger
	}
	for i, script := range definition.ClientScripts {
		script.Table = normalizeBaseTaskIdentifier(script.Table)
		script.Field = normalizeBaseTaskIdentifier(script.Field)
		definition.ClientScripts[i] = script
	}
	for i, seed := range definition.Seeds {
		seed.Table = normalizeBaseTaskIdentifier(seed.Table)
		definition.Seeds[i] = seed
	}
	for i, table := range definition.Tables {
		definition.Tables[i] = rewriteLegacyBaseTaskTable(table)
	}
}

func rewriteLegacyBaseTaskTable(table AppDefinitionTable) AppDefinitionTable {
	table.Name = normalizeBaseTaskIdentifier(table.Name)
	table.Extends = normalizeBaseTaskIdentifier(table.Extends)
	table.LabelSingular = normalizeBaseTaskText(table.LabelSingular)
	table.LabelPlural = normalizeBaseTaskText(table.LabelPlural)
	table.Description = normalizeBaseTaskText(table.Description)
	table.DisplayField = normalizeBaseTaskIdentifier(table.DisplayField)

	for i, column := range table.Columns {
		column.Name = normalizeBaseTaskIdentifier(column.Name)
		column.Label = normalizeBaseTaskText(column.Label)
		column.ReferenceTable = normalizeBaseTaskIdentifier(column.ReferenceTable)
		table.Columns[i] = column
	}
	for i, form := range table.Forms {
		form.Label = normalizeBaseTaskText(form.Label)
		form.Description = normalizeBaseTaskText(form.Description)
		form.Fields = renameBaseTaskFieldNames(form.Fields)
		table.Forms[i] = form
	}
	for i, list := range table.Lists {
		list.Label = normalizeBaseTaskText(list.Label)
		list.Columns = renameBaseTaskFieldNames(list.Columns)
		table.Lists[i] = list
	}
	for i, related := range table.RelatedLists {
		related.Name = normalizeBaseTaskIdentifier(related.Name)
		related.Label = normalizeBaseTaskText(related.Label)
		related.Table = normalizeBaseTaskIdentifier(related.Table)
		related.ReferenceField = normalizeBaseTaskIdentifier(related.ReferenceField)
		related.Columns = renameBaseTaskFieldNames(related.Columns)
		table.RelatedLists[i] = related
	}

	return table
}

func normalizeBaseTaskText(value string) string {
	return legacyBaseTaskTextReplacer.Replace(strings.TrimSpace(value))
}

func normalizeBaseTaskIdentifier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "base_work_order":
		return "base_task"
	case "base_work_order_state":
		return "base_task_state"
	case "base_work_order_transition":
		return "base_task_transition"
	case "base_work_order_entity":
		return "base_task_entity"
	case "parent_work_order_id":
		return "parent_task_id"
	case "work_order_id":
		return "task_id"
	case "work_orders":
		return "tasks"
	case "child_work_orders":
		return "child_tasks"
	default:
		return value
	}
}

func renameBaseTaskFieldNames(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	items := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = normalizeBaseTaskIdentifier(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		items = append(items, value)
	}
	return items
}

func upsertRelatedList(lists []AppDefinitionRelatedList, item AppDefinitionRelatedList) []AppDefinitionRelatedList {
	item.Name = strings.TrimSpace(strings.ToLower(item.Name))
	item.Label = strings.TrimSpace(item.Label)
	item.Table = strings.TrimSpace(strings.ToLower(item.Table))
	item.ReferenceField = strings.TrimSpace(strings.ToLower(item.ReferenceField))
	item.Columns = normalizeFieldNameList(item.Columns)

	if item.Name == "" || item.Table == "" || item.ReferenceField == "" {
		return lists
	}

	items := make([]AppDefinitionRelatedList, 0, len(lists)+1)
	replaced := false
	for _, existing := range lists {
		if strings.TrimSpace(strings.ToLower(existing.Name)) == item.Name {
			items = append(items, item)
			replaced = true
			continue
		}
		items = append(items, existing)
	}
	if !replaced {
		items = append(items, item)
	}
	return items
}

func replaceStateFieldInForms(forms []AppDefinitionForm) []AppDefinitionForm {
	items := make([]AppDefinitionForm, len(forms))
	for i, form := range forms {
		form.Fields = replaceStateFieldName(form.Fields)
		items[i] = form
	}
	return items
}

func removeFieldFromForms(forms []AppDefinitionForm, fieldName string) []AppDefinitionForm {
	items := make([]AppDefinitionForm, len(forms))
	for i, form := range forms {
		form.Fields = removeFieldName(form.Fields, fieldName)
		items[i] = form
	}
	return items
}

func upsertFieldAfterInForms(forms []AppDefinitionForm, fieldName, afterField string) []AppDefinitionForm {
	items := make([]AppDefinitionForm, len(forms))
	for i, form := range forms {
		form.Fields = upsertFieldAfter(form.Fields, fieldName, afterField)
		items[i] = form
	}
	return items
}

func replaceStateFieldInLists(lists []AppDefinitionList) []AppDefinitionList {
	items := make([]AppDefinitionList, len(lists))
	for i, list := range lists {
		list.Columns = replaceStateFieldName(list.Columns)
		items[i] = list
	}
	return items
}

func removeFieldFromLists(lists []AppDefinitionList, fieldName string) []AppDefinitionList {
	items := make([]AppDefinitionList, len(lists))
	for i, list := range lists {
		list.Columns = removeFieldName(list.Columns, fieldName)
		items[i] = list
	}
	return items
}

func replaceStateFieldInRelatedLists(lists []AppDefinitionRelatedList) []AppDefinitionRelatedList {
	items := make([]AppDefinitionRelatedList, len(lists))
	for i, list := range lists {
		list.Columns = replaceStateFieldName(list.Columns)
		items[i] = list
	}
	return items
}

func removeFieldFromRelatedLists(lists []AppDefinitionRelatedList, fieldName string) []AppDefinitionRelatedList {
	items := make([]AppDefinitionRelatedList, len(lists))
	for i, list := range lists {
		list.Columns = removeFieldName(list.Columns, fieldName)
		items[i] = list
	}
	return items
}

func normalizeFieldNameList(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	items := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		items = append(items, value)
	}
	return items
}

func removeFieldName(values []string, fieldName string) []string {
	fieldName = strings.TrimSpace(strings.ToLower(fieldName))
	if len(values) == 0 {
		return nil
	}

	items := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" || value == fieldName || seen[value] {
			continue
		}
		seen[value] = true
		items = append(items, value)
	}
	return items
}

func replaceStateFieldName(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(values))
	items := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if value == "state_id" {
			value = "state"
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		items = append(items, value)
	}
	return items
}

func upsertFieldAfter(values []string, fieldName, afterField string) []string {
	fieldName = strings.TrimSpace(strings.ToLower(fieldName))
	afterField = strings.TrimSpace(strings.ToLower(afterField))
	if fieldName == "" {
		return normalizeFieldNameList(values)
	}

	items := normalizeFieldNameList(values)
	if len(items) == 0 {
		return []string{fieldName}
	}

	filtered := make([]string, 0, len(items)+1)
	for _, value := range items {
		if value == fieldName {
			continue
		}
		filtered = append(filtered, value)
	}

	insertAt := len(filtered)
	for i, value := range filtered {
		if value == afterField {
			insertAt = i + 1
			break
		}
	}

	filtered = append(filtered, "")
	copy(filtered[insertAt+1:], filtered[insertAt:])
	filtered[insertAt] = fieldName
	return filtered
}
