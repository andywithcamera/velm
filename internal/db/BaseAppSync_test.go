package db

import "testing"

func TestSimplifyBaseTaskModelDefinition(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: base
namespace: base
tables:
  - name: base_work_order
    columns:
      - name: number
        data_type: text
      - name: work_type
        data_type: text
      - name: state_id
        data_type: reference
        reference_table: base_work_order_state
      - name: entity_id
        data_type: reference
        reference_table: base_entity
      - name: priority
        data_type: choice
        choices:
          - value: p3
    forms:
      - name: default
        fields: [number, work_type, state_id, entity_id, priority]
    lists:
      - name: default
        columns: [number, state_id, entity_id, priority]
    related_lists:
      - name: child_work_orders
        table: base_work_order
        reference_field: parent_work_order_id
        columns: [number, state_id, priority]
  - name: base_work_order_state
    columns:
      - name: label
        data_type: text
    related_lists:
      - name: work_orders
        table: base_work_order
        reference_field: state_id
        columns: [number, state_id]
  - name: base_work_order_transition
    columns:
      - name: from_state_id
        data_type: reference
        reference_table: base_work_order_state
  - name: base_entity
    columns:
      - name: number
        data_type: text
      - name: name
        data_type: text
      - name: entity_type
        data_type: text
      - name: lifecycle_state
        data_type: choice
        choices:
          - value: active
      - name: operational_status
        data_type: choice
        choices:
          - value: operational
      - name: criticality
        data_type: choice
        choices:
          - value: p3
    related_lists:
      - name: work_orders
        table: base_work_order
        reference_field: entity_id
        columns: [number, state_id, priority]
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}

	if !simplifyBaseTaskModelDefinition(definition) {
		t.Fatal("expected definition to change")
	}
	if simplifyBaseTaskModelDefinition(definition) {
		t.Fatal("expected definition rewrite to be idempotent")
	}

	if got := len(definition.Tables); got != 3 {
		t.Fatalf("expected three tables after cleanup, got %d", got)
	}

	task := requireDefinitionTable(t, definition, "base_task")
	if !task.Extensible {
		t.Fatal("expected base_task to remain extensible")
	}
	numberColumn := requireDefinitionColumn(t, task, "number")
	if got := numberColumn.DataType; got != "autnumber" {
		t.Fatalf("expected number column to become autnumber, got %q", got)
	}
	if got := numberColumn.Prefix; got != "TASK" {
		t.Fatalf("expected number prefix TASK, got %q", got)
	}
	workTypeColumn := requireDefinitionColumn(t, task, "work_type")
	if got := workTypeColumn.Label; got != "Task Type" {
		t.Fatalf("expected work_type label Task Type, got %q", got)
	}
	if got := workTypeColumn.DefaultValue; got != "TASK" {
		t.Fatalf("expected work_type default TASK, got %q", got)
	}
	priorityColumn := requireDefinitionColumn(t, task, "priority")
	if got := priorityColumn.DefaultValue; got != "medium" {
		t.Fatalf("expected priority default medium, got %q", got)
	}
	if got := len(priorityColumn.Choices); got != 5 {
		t.Fatalf("expected five priority choices, got %d", got)
	}
	if priorityColumn.Choices[0].Value != "very_low" || priorityColumn.Choices[4].Value != "very_high" {
		t.Fatalf("expected normalized priority choices, got %#v", priorityColumn.Choices)
	}
	if hasDefinitionColumn(task, "state_id") {
		t.Fatal("expected state_id column to be removed from base_task")
	}
	if hasDefinitionColumn(task, "entity_id") {
		t.Fatal("expected entity_id column to be removed from base_task")
	}
	if hasDefinitionColumn(task, "board_rank") {
		t.Fatal("expected board_rank column to be removed from base_task")
	}
	if hasDefinitionColumn(task, "resolved_at") {
		t.Fatal("expected resolved_at column to be removed from base_task")
	}

	stateColumn := requireDefinitionColumn(t, task, "state")
	if got := stateColumn.DataType; got != "choice" {
		t.Fatalf("expected state column to be choice, got %q", got)
	}
	if got := stateColumn.DefaultValue; got != "new" {
		t.Fatalf("expected state default new, got %q", got)
	}
	if got := len(stateColumn.Choices); got != 5 {
		t.Fatalf("expected five state choices, got %d", got)
	}
	if got := stateColumn.Choices[0].Value; got != "new" {
		t.Fatalf("expected first state choice new, got %q", got)
	}
	if got := stateColumn.Choices[3].Value; got != "ready_to_close" {
		t.Fatalf("expected fourth state choice ready_to_close, got %q", got)
	}
	closureReasonColumn := requireDefinitionColumn(t, task, "closure_reason")
	if got := closureReasonColumn.DataType; got != "choice" {
		t.Fatalf("expected closure_reason column to be choice, got %q", got)
	}
	if got := closureReasonColumn.ConditionExpr; got != "state=closed" {
		t.Fatalf("expected closure_reason condition expr state=closed, got %q", got)
	}
	if got := len(closureReasonColumn.Choices); got != 2 {
		t.Fatalf("expected two closure reason choices, got %d", got)
	}

	if fields := task.Forms[0].Fields; containsString(fields, "state_id") || containsString(fields, "entity_id") {
		t.Fatalf("expected task form to drop state_id/entity_id, got %v", fields)
	}
	if !containsString(task.Forms[0].Fields, "state") {
		t.Fatalf("expected task form to use state, got %v", task.Forms[0].Fields)
	}
	if !containsString(task.Forms[0].Fields, "closure_reason") {
		t.Fatalf("expected task form to include closure_reason, got %v", task.Forms[0].Fields)
	}
	if columns := task.Lists[0].Columns; containsString(columns, "state_id") || containsString(columns, "entity_id") {
		t.Fatalf("expected task list to drop state_id/entity_id, got %v", columns)
	}
	if containsString(task.Lists[0].Columns, "board_rank") {
		t.Fatalf("expected task list to drop board_rank, got %v", task.Lists[0].Columns)
	}
	if containsString(task.Lists[0].Columns, "resolved_at") {
		t.Fatalf("expected task list to drop resolved_at, got %v", task.Lists[0].Columns)
	}
	if !containsString(task.Lists[0].Columns, "state") {
		t.Fatalf("expected task list to use state, got %v", task.Lists[0].Columns)
	}

	childTasks := requireRelatedList(t, task, "child_tasks")
	if columns := childTasks.Columns; containsString(columns, "state_id") || !containsString(columns, "state") {
		t.Fatalf("expected child tasks related list columns to use state, got %v", columns)
	}

	affectedEntities := requireRelatedList(t, task, "affected_entities")
	if affectedEntities.Table != "base_task_entity" {
		t.Fatalf("expected affected_entities table base_task_entity, got %q", affectedEntities.Table)
	}
	if affectedEntities.ReferenceField != "task_id" {
		t.Fatalf("expected affected_entities reference_field task_id, got %q", affectedEntities.ReferenceField)
	}
	if got := affectedEntities.Columns; len(got) != 1 || got[0] != "entity_id" {
		t.Fatalf("expected affected_entities columns [entity_id], got %v", got)
	}

	entity := requireDefinitionTable(t, definition, "base_entity")
	if !entity.Extensible {
		t.Fatal("expected base_entity to remain extensible")
	}
	entityNumber := requireDefinitionColumn(t, entity, "number")
	if got := entityNumber.DataType; got != "autnumber" {
		t.Fatalf("expected base_entity number to become autnumber, got %q", got)
	}
	if got := entityNumber.Prefix; got != "ENT" {
		t.Fatalf("expected base_entity number prefix ENT, got %q", got)
	}
	if got := requireDefinitionColumn(t, entity, "entity_type").DefaultValue; got != "item" {
		t.Fatalf("expected entity_type default item, got %q", got)
	}
	if got := requireDefinitionColumn(t, entity, "lifecycle_state").DefaultValue; got != "active" {
		t.Fatalf("expected lifecycle_state default active, got %q", got)
	}
	if got := requireDefinitionColumn(t, entity, "operational_status").DefaultValue; got != "operational" {
		t.Fatalf("expected operational_status default operational, got %q", got)
	}
	if got := requireDefinitionColumn(t, entity, "criticality").DefaultValue; got != "p3" {
		t.Fatalf("expected criticality default p3, got %q", got)
	}
	tasks := requireRelatedList(t, entity, "tasks")
	if tasks.Table != "base_task_entity" {
		t.Fatalf("expected entity tasks table base_task_entity, got %q", tasks.Table)
	}
	if tasks.ReferenceField != "entity_id" {
		t.Fatalf("expected entity tasks reference_field entity_id, got %q", tasks.ReferenceField)
	}
	if got := tasks.Columns; len(got) != 1 || got[0] != "task_id" {
		t.Fatalf("expected entity tasks columns [task_id], got %v", got)
	}

	linkTable := requireDefinitionTable(t, definition, "base_task_entity")
	if linkTable.DisplayField != "entity_id" {
		t.Fatalf("expected link table display field entity_id, got %q", linkTable.DisplayField)
	}
	if !hasDefinitionColumn(linkTable, "task_id") || !hasDefinitionColumn(linkTable, "entity_id") {
		t.Fatalf("expected link table columns task_id/entity_id, got %+v", linkTable.Columns)
	}
	if fields := linkTable.Forms[0].Fields; len(fields) != 2 || fields[0] != "task_id" || fields[1] != "entity_id" {
		t.Fatalf("expected link table form fields [task_id entity_id], got %v", fields)
	}
	if columns := linkTable.Lists[0].Columns; len(columns) < 2 || columns[0] != "task_id" || columns[1] != "entity_id" {
		t.Fatalf("expected link table list columns to start with task_id/entity_id, got %v", columns)
	}
}

func requireDefinitionTable(t *testing.T, definition *AppDefinition, tableName string) AppDefinitionTable {
	t.Helper()
	for _, table := range definition.Tables {
		if table.Name == tableName {
			return table
		}
	}
	t.Fatalf("missing table %q", tableName)
	return AppDefinitionTable{}
}

func requireDefinitionColumn(t *testing.T, table AppDefinitionTable, columnName string) AppDefinitionColumn {
	t.Helper()
	for _, column := range table.Columns {
		if column.Name == columnName {
			return column
		}
	}
	t.Fatalf("missing column %q on %q", columnName, table.Name)
	return AppDefinitionColumn{}
}

func requireRelatedList(t *testing.T, table AppDefinitionTable, relatedListName string) AppDefinitionRelatedList {
	t.Helper()
	for _, related := range table.RelatedLists {
		if related.Name == relatedListName {
			return related
		}
	}
	t.Fatalf("missing related list %q on %q", relatedListName, table.Name)
	return AppDefinitionRelatedList{}
}

func hasDefinitionColumn(table AppDefinitionTable, columnName string) bool {
	for _, column := range table.Columns {
		if column.Name == columnName {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
