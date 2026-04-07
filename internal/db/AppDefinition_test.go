package db

import "testing"

func TestParseAppDefinitionNormalizesTablesAndColumns(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: ITSM
namespace: ITSM
dependencies:
  - Task
  - task
  - ITSM
tables:
  - name: ITSM_Task
    columns:
      - name: Short_Description
        data_type: VARCHAR(255)
        is_nullable: false
client_scripts:
  - name: Assign Owner
    table: ITSM_Task
    event: FORM.LOAD
    enabled: true
    script: console.log("x")
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}
	if definition == nil {
		t.Fatal("ParseAppDefinition() returned nil definition")
	}

	if definition.Name != "itsm" {
		t.Fatalf("expected normalized app name, got %q", definition.Name)
	}
	if got := definition.Tables[0].Name; got != "itsm_task" {
		t.Fatalf("expected normalized table name, got %q", got)
	}
	if got := definition.Tables[0].Columns[0].Name; got != "short_description" {
		t.Fatalf("expected normalized column name, got %q", got)
	}
	if got := definition.Tables[0].Columns[0].DataType; got != "varchar(255)" {
		t.Fatalf("expected normalized data type, got %q", got)
	}
	if got := len(definition.Dependencies); got != 1 {
		t.Fatalf("expected one normalized dependency, got %d", got)
	}
	if got := definition.Dependencies[0]; got != "task" {
		t.Fatalf("expected normalized dependency task, got %q", got)
	}
	if got := definition.ClientScripts[0].Name; got != "assign_owner" {
		t.Fatalf("expected normalized client script name, got %q", got)
	}
	if got := definition.ClientScripts[0].Event; got != "form.load" {
		t.Fatalf("expected normalized client script event, got %q", got)
	}
	if got := definition.ClientScripts[0].Language; got != "javascript" {
		t.Fatalf("expected default client script language javascript, got %q", got)
	}
}

func TestParseAppDefinitionRejectsLegacyTopLevelScripts(t *testing.T) {
	_, err := ParseAppDefinition(`
name: itsm
namespace: itsm
scripts:
  - name: legacy_script
    script: console.log("x")
`)
	if err == nil {
		t.Fatal("expected legacy scripts error")
	}
}

func TestParseAppDefinitionRejectsUnsafeIdentifiers(t *testing.T) {
	_, err := ParseAppDefinition(`
tables:
  - name: bad-name
`)
	if err == nil {
		t.Fatal("expected invalid table name error")
	}
}

func TestParseAppDefinitionRejectsUnprefixedTableName(t *testing.T) {
	_, err := ParseAppDefinition(`
name: task
namespace: task
tables:
  - name: work
`)
	if err == nil {
		t.Fatal("expected namespace prefix validation error")
	}
}

func TestParseAppDefinitionAllowsSystemNamespaceUnderscoreTables(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: system
namespace: ""
tables:
  - name: _user
    columns:
      - name: _id
        data_type: uuid
      - name: name
        data_type: text
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}
	if got := definition.Tables[0].Name; got != "_user" {
		t.Fatalf("expected system table name _user, got %q", got)
	}
}

func TestParseAppDefinitionRejectsNonUnderscoreTableForSystemNamespace(t *testing.T) {
	_, err := ParseAppDefinition(`
name: system
namespace: ""
tables:
  - name: user
`)
	if err == nil {
		t.Fatal("expected underscore table name validation error")
	}
}

func TestParseAppDefinitionAllowsOOTBBaseNamespaceMixedTables(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: base
namespace: ""
tables:
  - name: _user
    columns:
      - name: _id
        data_type: uuid
      - name: name
        data_type: text
  - name: base_task
    columns:
      - name: number
        data_type: autnumber
        prefix: task
      - name: title
        data_type: text
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}
	if len(definition.Tables) != 2 {
		t.Fatalf("expected two tables, got %d", len(definition.Tables))
	}
	if got := definition.Tables[1].Name; got != "base_task" {
		t.Fatalf("expected OOTB base table name base_task, got %q", got)
	}
}

func TestParseAppDefinitionRejectsUnprefixedTableForOOTBBaseNamespace(t *testing.T) {
	_, err := ParseAppDefinition(`
name: base
namespace: ""
tables:
  - name: task
`)
	if err == nil {
		t.Fatal("expected OOTB base table prefix validation error")
	}
}

func TestParseAppDefinitionRejectsUnsafeDependencyNames(t *testing.T) {
	_, err := ParseAppDefinition(`
dependencies:
  - bad-name
`)
	if err == nil {
		t.Fatal("expected invalid dependency error")
	}
}

func TestParseAppDefinitionNormalizesServicesAndViews(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: ITSM
namespace: ITSM
tables:
  - name: itsm_incident
    columns:
      - name: number
      - name: short_description
    lists:
      - name: DEFAULT
        columns: [NUMBER, short_description, short_description]
services:
  - name: Incident
    methods:
      - name: Assign_Owner
        script: |
          async function run(ctx) {}
triggers:
  - name: Assign_On_Update
    event: RECORD.UPDATE
    table: ITSM_INCIDENT
    call: INCIDENT.ASSIGN_OWNER
schedules:
  - name: Hourly_Recalc
    cron: "0 * * * *"
    call: incident.assign_owner
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}
	if got := definition.Tables[0].Lists[0].Name; got != "default" {
		t.Fatalf("expected normalized list name, got %q", got)
	}
	if got := len(definition.Tables[0].Lists[0].Columns); got != 2 {
		t.Fatalf("expected de-duplicated list columns, got %d", got)
	}
	if got := definition.Services[0].Name; got != "incident" {
		t.Fatalf("expected normalized service name, got %q", got)
	}
	if got := definition.Services[0].Methods[0].Visibility; got != "private" {
		t.Fatalf("expected default method visibility private, got %q", got)
	}
	if got := definition.Triggers[0].Mode; got != "async" {
		t.Fatalf("expected default trigger mode async, got %q", got)
	}
}

func TestParseAppDefinitionMigratesLegacyTopLevelFormsUnderTables(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: demo
namespace: demo
roles:
  - name: editor
tables:
  - name: demo_task
    columns:
      - name: number
      - name: state
    data_policies:
      - name: Require State
        action: validate
    triggers:
      - name: Update_State
        event: RECORD.UPDATE
        call: state_update
    related_lists:
      - name: comments
        table: demo_comment
        reference_field: task_id
forms:
  - name: Task_Main
    table: demo_task
    description: Main task editor
    layout: [number, state]
    actions:
      - name: reopen
        roles: [editor]
    security:
      roles: [editor]
services:
  - name: state
    methods:
      - name: update
        visibility: public
        script: console.log("ok")
endpoints:
  - name: state_update
    enabled: true
    method: post
    path: api/task/update
    call: state.update
    roles: [editor]
documentation:
  - name: task_overview
    visibility: INTERNAL
    related: [demo_task, state.update]
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}
	if len(definition.Forms) != 0 {
		t.Fatalf("expected top-level forms to be migrated, got %#v", definition.Forms)
	}
	if len(definition.Tables) != 1 || len(definition.Tables[0].Forms) != 1 {
		t.Fatalf("expected migrated table form, got %#v", definition.Tables)
	}
	if got := definition.Tables[0].Forms[0].Name; got != "task_main" {
		t.Fatalf("expected normalized form name, got %q", got)
	}
	if got := definition.Tables[0].Forms[0].Description; got != "Main task editor" {
		t.Fatalf("expected migrated description, got %q", got)
	}
	if got := len(definition.Tables[0].Forms[0].Actions); got != 1 {
		t.Fatalf("expected migrated actions, got %d", got)
	}
	if got := len(definition.Tables[0].Forms[0].Security.Roles); got != 1 {
		t.Fatalf("expected migrated security roles, got %d", got)
	}
	if got := definition.Endpoints[0].Name; got != "state_update" {
		t.Fatalf("expected normalized endpoint name, got %q", got)
	}
	if got := definition.Endpoints[0].Method; got != "POST" {
		t.Fatalf("expected normalized endpoint method, got %q", got)
	}
	if got := definition.Endpoints[0].Path; got != "/api/task/update" {
		t.Fatalf("expected normalized endpoint path, got %q", got)
	}
	if got := definition.Documentation[0].Visibility; got != "internal" {
		t.Fatalf("expected normalized documentation visibility, got %q", got)
	}
}

func TestResolveTableFormReturnsNamedVariant(t *testing.T) {
	table := AppDefinitionTable{
		Forms: []AppDefinitionForm{
			{Name: "default", Label: "Default Form"},
			{Name: "manager", Label: "Manager Review"},
		},
	}

	form, ok := ResolveTableForm(table, "manager")
	if !ok {
		t.Fatal("expected manager form to resolve")
	}
	if form.Name != "manager" {
		t.Fatalf("ResolveTableForm() name = %q, want manager", form.Name)
	}
}

func TestResolveTableFormFallsBackToDefaultWhenBlank(t *testing.T) {
	table := AppDefinitionTable{
		Forms: []AppDefinitionForm{
			{Name: "manager", Label: "Manager Review"},
			{Name: "default", Label: "Default Form"},
		},
	}

	form, ok := ResolveTableForm(table, "")
	if !ok {
		t.Fatal("expected blank form name to resolve")
	}
	if form.Name != "default" {
		t.Fatalf("ResolveTableForm() name = %q, want default", form.Name)
	}
}

func TestParseAppDefinitionRejectsLongNamespace(t *testing.T) {
	_, err := ParseAppDefinition(`
name: itsm
namespace: itsmtools
`)
	if err == nil {
		t.Fatal("expected namespace length validation error")
	}
}

func TestParseAppDefinitionNormalizesReferenceAndChoiceColumns(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: task
namespace: task
tables:
  - name: task_work
    columns:
      - name: requested_for
        data_type: reference
        reference_table: _USER
      - name: state
        data_type: choice
        choices:
          - value: in_progress
          - value: done
            label: Done
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}

	requestedFor := definition.Tables[0].Columns[0]
	if requestedFor.ReferenceTable != "_user" {
		t.Fatalf("expected normalized reference table, got %q", requestedFor.ReferenceTable)
	}

	state := definition.Tables[0].Columns[1]
	if got := len(state.Choices); got != 2 {
		t.Fatalf("expected two normalized choices, got %d", got)
	}
	if state.Choices[0].Label != "In Progress" {
		t.Fatalf("expected default choice label, got %q", state.Choices[0].Label)
	}
}

func TestParseAppDefinitionNormalizesSecurityRules(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: task
namespace: task
roles:
  - name: task_manager
  - name: task_assignee
tables:
  - name: task_task
    columns:
      - name: state
    security:
      rules:
        - name: Manager Rule
          effect: DENY
          operation: update
          table: TASK_TASK
          field: State
          role: TASK_MANAGER
          order: 20
        - name: Assignee Rule
          operation: read
          role: TASK_ASSIGNEE
          order: 10
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}

	rules := definition.Tables[0].Security.Rules
	if got := len(rules); got != 2 {
		t.Fatalf("len(rules) = %d, want 2", got)
	}
	if got := rules[0].Name; got != "assignee_rule" {
		t.Fatalf("rules[0].Name = %q, want %q", got, "assignee_rule")
	}
	if got := rules[0].Operation; got != "R" {
		t.Fatalf("rules[0].Operation = %q, want %q", got, "R")
	}
	if got := rules[0].Effect; got != "" {
		t.Fatalf("rules[0].Effect = %q, want empty for backward-compatible allow", got)
	}
	if got := rules[0].Role; got != "task_assignee" {
		t.Fatalf("rules[0].Role = %q, want %q", got, "task_assignee")
	}
	if got := rules[1].Name; got != "manager_rule" {
		t.Fatalf("rules[1].Name = %q, want %q", got, "manager_rule")
	}
	if got := rules[1].Operation; got != "U" {
		t.Fatalf("rules[1].Operation = %q, want %q", got, "U")
	}
	if got := rules[1].Effect; got != "deny" {
		t.Fatalf("rules[1].Effect = %q, want %q", got, "deny")
	}
	if got := rules[1].Table; got != "task_task" {
		t.Fatalf("rules[1].Table = %q, want %q", got, "task_task")
	}
	if got := rules[1].Field; got != "state" {
		t.Fatalf("rules[1].Field = %q, want %q", got, "state")
	}
}

func TestParseAppDefinitionNormalizesAutnumberPrefix(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: task
namespace: task
tables:
  - name: task_work
    columns:
      - name: number
        data_type: autonumber
        is_nullable: false
        prefix: task
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}

	column := definition.Tables[0].Columns[0]
	if got := column.DataType; got != "autnumber" {
		t.Fatalf("expected normalized autnumber type, got %q", got)
	}
	if got := column.Prefix; got != "TASK" {
		t.Fatalf("expected normalized prefix TASK, got %q", got)
	}
}

func TestParseAppDefinitionRejectsInvalidAutnumberPrefix(t *testing.T) {
	_, err := ParseAppDefinition(`
name: task
namespace: task
tables:
  - name: task_work
    columns:
      - name: number
        data_type: autnumber
        is_nullable: false
        prefix: TS
`)
	if err == nil {
		t.Fatal("expected invalid autnumber prefix error")
	}
}

func TestParseAppDefinitionNormalizesDisplayField(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: task
namespace: task
tables:
  - name: task_work
    display_field: EMAIL
    columns:
      - name: email
        data_type: email
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}
	if got := definition.Tables[0].DisplayField; got != "email" {
		t.Fatalf("expected normalized display_field, got %q", got)
	}
}

func TestParseAppDefinitionRetainsPageSearchKeywords(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: demo
namespace: demo
pages:
  - slug: launchpad
    label: Launchpad
    search_keywords:   home, dashboard, welcome  
    content: <section>Welcome</section>
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}
	if len(definition.Pages) != 1 {
		t.Fatalf("expected one page, got %d", len(definition.Pages))
	}
	if got := definition.Pages[0].SearchKeywords; got != "home, dashboard, welcome" {
		t.Fatalf("expected normalized search keywords, got %q", got)
	}
	if got := definition.Pages[0].Name; got != "Launchpad" {
		t.Fatalf("expected page name to default from label, got %q", got)
	}
}

func TestBuildYAMLColumnsIncludesSystemColumns(t *testing.T) {
	app := RegisteredApp{Name: "task", Namespace: "task"}
	table := AppDefinitionTable{
		Name: "task_work",
		Columns: []AppDefinitionColumn{
			{Name: "number", Label: "Number", DataType: "autnumber", Prefix: "TASK"},
			{Name: "short_description", Label: "Short Description", DataType: "text"},
		},
	}

	columns := BuildYAMLColumns(app, table)
	if len(columns) != 10 {
		t.Fatalf("expected 10 columns including system columns, got %d", len(columns))
	}
	if columns[0].NAME != "_id" || columns[0].DATA_TYPE != "uuid" {
		t.Fatalf("expected first system column to be _id uuid, got %s %s", columns[0].NAME, columns[0].DATA_TYPE)
	}
	if columns[3].NAME != "_update_count" || columns[3].DATA_TYPE != "bigint" {
		t.Fatalf("expected _update_count system column, got %#v", columns[3])
	}
	if columns[5].NAME != "_created_by" || !columns[5].REFERENCE_TABLE.Valid || columns[5].REFERENCE_TABLE.String != "_user" {
		t.Fatalf("expected _created_by to be a reference to _user, got %#v", columns[5])
	}
	if columns[len(columns)-2].NAME != "number" || columns[len(columns)-2].PREFIX.String != "TASK" {
		t.Fatalf("expected autnumber column with TASK prefix, got %#v", columns[len(columns)-2])
	}
	if columns[len(columns)-1].NAME != "short_description" {
		t.Fatalf("expected user column to remain after system columns, got %q", columns[len(columns)-1].NAME)
	}
}

func TestBuildYAMLTableIncludesDisplayField(t *testing.T) {
	app := RegisteredApp{Name: "task", Namespace: "task"}
	table := AppDefinitionTable{
		Name:          "task_work",
		LabelSingular: "Work",
		LabelPlural:   "Work",
		DisplayField:  "number",
	}

	item := BuildYAMLTable(app, table)
	if item.DISPLAY_FIELD != "number" {
		t.Fatalf("BuildYAMLTable().DISPLAY_FIELD = %q, want %q", item.DISPLAY_FIELD, "number")
	}
}

func TestBuildYAMLColumnsSystemAppUsesExplicitColumns(t *testing.T) {
	app := RegisteredApp{Name: "system", Namespace: ""}
	table := AppDefinitionTable{
		Name: "_group",
		Columns: []AppDefinitionColumn{
			{Name: "_id", Label: "ID", DataType: "uuid"},
			{Name: "name", Label: "Name", DataType: "text"},
		},
	}

	columns := BuildYAMLColumns(app, table)
	if len(columns) != 2 {
		t.Fatalf("expected explicit columns only for system app, got %d", len(columns))
	}
	if columns[0].NAME != "_id" || !columns[0].IS_READONLY {
		t.Fatalf("expected explicit _id to stay read-only, got %#v", columns[0])
	}
}

func TestBuildYAMLColumnsOOTBBaseUsesImplicitColumnsForBaseTables(t *testing.T) {
	app := RegisteredApp{Name: "base", Namespace: ""}
	table := AppDefinitionTable{
		Name: "base_task",
		Columns: []AppDefinitionColumn{
			{Name: "number", Label: "Number", DataType: "autnumber", Prefix: "TASK"},
			{Name: "title", Label: "Title", DataType: "text"},
		},
	}

	columns := BuildYAMLColumns(app, table)
	if len(columns) != 10 {
		t.Fatalf("expected 10 columns including implicit system columns, got %d", len(columns))
	}
	if columns[0].NAME != "_id" || columns[0].DATA_TYPE != "uuid" {
		t.Fatalf("expected first implicit column to be _id uuid, got %s %s", columns[0].NAME, columns[0].DATA_TYPE)
	}
	if columns[len(columns)-2].NAME != "number" || columns[len(columns)-2].PREFIX.String != "TASK" {
		t.Fatalf("expected autnumber column with TASK prefix, got %#v", columns[len(columns)-2])
	}
}

func TestResolveDefinitionColumnsWithAppsIncludesInheritedColumns(t *testing.T) {
	baseDefinition, err := ParseAppDefinition(`
name: base
namespace: base
tables:
  - name: base_task
    extensible: true
    columns:
      - name: number
        data_type: autnumber
        prefix: TASK
      - name: title
        data_type: text
        is_nullable: false
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition(base) error = %v", err)
	}
	childDefinition, err := ParseAppDefinition(`
name: devworks
namespace: dw
dependencies:
  - base
tables:
  - name: dw_story
    extends: base_task
    columns:
      - name: epic
        data_type: reference
        reference_table: dw_epic
      - name: story_points
        data_type: integer
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition(child) error = %v", err)
	}

	baseApp := RegisteredApp{Name: "base", Namespace: "base", Definition: baseDefinition}
	childApp := RegisteredApp{Name: "devworks", Namespace: "dw", Definition: childDefinition}

	resolved, err := resolveDefinitionColumnsWithApps(
		[]RegisteredApp{baseApp, childApp},
		childApp,
		childDefinition.Tables[0],
		map[string]bool{},
	)
	if err != nil {
		t.Fatalf("resolveDefinitionColumnsWithApps() error = %v", err)
	}
	if len(resolved) != 4 {
		t.Fatalf("expected 4 resolved columns, got %d", len(resolved))
	}
	if resolved[0].Name != "number" || resolved[1].Name != "title" {
		t.Fatalf("expected inherited columns first, got %#v", resolved)
	}
	if resolved[2].Name != "epic" || resolved[3].Name != "story_points" {
		t.Fatalf("expected local columns after inherited columns, got %#v", resolved)
	}
}

func TestResolveDefinitionColumnsWithAppsUsesDraftDefinitionForInheritedTables(t *testing.T) {
	baseDefinition, err := ParseAppDefinition(`
name: base
namespace: base
tables:
  - name: base_task
    extensible: true
    columns:
      - name: number
        data_type: autnumber
        prefix: TASK
      - name: title
        data_type: text
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition(base) error = %v", err)
	}
	childDefinition, err := ParseAppDefinition(`
name: devworks
namespace: dw
dependencies:
  - base
tables:
  - name: dw_story
    extends: base_task
    columns:
      - name: epic
        data_type: reference
        reference_table: dw_epic
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition(child) error = %v", err)
	}

	resolved, err := resolveDefinitionColumnsWithApps(
		[]RegisteredApp{
			{Name: "base", Namespace: "base", Definition: baseDefinition, DraftDefinition: baseDefinition},
			{Name: "devworks", Namespace: "dw", DraftDefinition: childDefinition},
		},
		RegisteredApp{Name: "devworks", Namespace: "dw", DraftDefinition: childDefinition},
		childDefinition.Tables[0],
		map[string]bool{},
	)
	if err != nil {
		t.Fatalf("resolveDefinitionColumnsWithApps() error = %v", err)
	}
	if len(resolved) != 3 {
		t.Fatalf("expected 3 resolved columns, got %d", len(resolved))
	}
	if resolved[0].Name != "number" || resolved[1].Name != "title" || resolved[2].Name != "epic" {
		t.Fatalf("expected inherited columns from base task plus local epic, got %#v", resolved)
	}
}
