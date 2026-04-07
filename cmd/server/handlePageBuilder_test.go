package main

import (
	"context"
	"testing"

	"velm/internal/db"
)

func TestDeriveRegisteredAppsFromTables(t *testing.T) {
	tables := []db.BuilderTableSummary{
		{Name: "itsm_incident"},
		{Name: "itsm_problem"},
		{Name: "_app"},
		{Name: "_work"},
	}

	apps := deriveRegisteredAppsFromTables(tables)
	if len(apps) != 1 {
		t.Fatalf("expected 1 derived app, got %d", len(apps))
	}

	if apps[0].Name != "itsm" {
		t.Fatalf("expected first derived app to be itsm, got %q", apps[0].Name)
	}
}

func TestBuildAppEditorFormElementSectionsBuildsRowsForFieldsAndRelatedLists(t *testing.T) {
	sections := buildAppEditorFormElementRows(context.Background(), db.RegisteredApp{
		Name:      "demo",
		Namespace: "demo",
	}, "demo_task", db.AppDefinitionTable{
		Name: "demo_task",
		Columns: []db.AppDefinitionColumn{
			{Name: "number", Label: "Number"},
			{Name: "state", Label: "State"},
		},
		RelatedLists: []db.AppDefinitionRelatedList{
			{Name: "comments", Label: "Comments", Table: "demo_comment", ReferenceField: "task_id"},
		},
	}, db.AppDefinitionForm{
		Name:   "default",
		Label:  "Default",
		Fields: []string{"number", "state"},
	})

	if len(sections) != 1 {
		t.Fatalf("len(sections) = %d, want 1", len(sections))
	}
	if got := sections[0].TableLabel; got != "Form Elements" {
		t.Fatalf("TableLabel = %q, want %q", got, "Form Elements")
	}
	if got := len(sections[0].Rows); got != 3 {
		t.Fatalf("len(rows) = %d, want 3", got)
	}
	if got := sections[0].Rows[0].Cells[1].Value; got != "Column" {
		t.Fatalf("first row kind = %q, want %q", got, "Column")
	}
	if got := sections[0].Rows[2].Cells[1].Value; got != "Related List" {
		t.Fatalf("third row kind = %q, want %q", got, "Related List")
	}
	if got := sections[0].Rows[2].Href; got != "/admin/app-editor?app=demo&active=table-related-list%3Ademo_task%3Acomments" {
		t.Fatalf("third row href = %q", got)
	}
}

func TestBuildYAMLAppEditorTableHandlesDraftOnlyInheritedTables(t *testing.T) {
	childDefinition, err := db.ParseAppDefinition(`
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

	explorerTable, _ := buildYAMLAppEditorTable(context.Background(), appRegistryItem{
		Name:            "devworks",
		Namespace:       "dw",
		DraftDefinition: childDefinition,
	}, childDefinition.Tables[0])

	names := map[string]bool{}
	for _, column := range explorerTable.Columns {
		names[column.Name] = true
	}
	if !names["epic"] {
		t.Fatalf("expected local column epic in explorer columns, got %#v", explorerTable.Columns)
	}
}

func TestAttachAppEditorTableClientScriptAttachesMatchingTableScripts(t *testing.T) {
	explorerTables := []appEditorExplorerTable{{
		Table: appEditorObject{
			Name:         "demo_task",
			PhysicalName: "demo_task",
		},
	}}
	group := appEditorClientScriptGroup{
		Script: appEditorObject{
			ID:   "client-script:demo:task_form_load",
			Name: "task_form_load",
		},
	}

	attached := attachAppEditorTableClientScript(explorerTables, db.AppDefinitionClientScript{
		Name:  "task_form_load",
		Table: "demo_task",
	}, group)
	if !attached {
		t.Fatal("expected table-scoped client script to attach")
	}
	if got := len(explorerTables[0].ClientScripts); got != 1 {
		t.Fatalf("len(ClientScripts) = %d, want 1", got)
	}
	if got := explorerTables[0].ClientScripts[0].Script.Name; got != "task_form_load" {
		t.Fatalf("first script = %q, want %q", got, "task_form_load")
	}
}

func TestBuildYAMLAppEditorTableBuildsCollectionItemEditors(t *testing.T) {
	definition, err := db.ParseAppDefinition(`
name: demo
namespace: demo
tables:
  - name: demo_task
    columns:
      - name: number
    data_policies:
      - name: require_number
        action: validate
        enabled: true
    triggers:
      - name: stamp_number
        event: record.update
        call: demo.stamp_number
        mode: async
        enabled: true
    related_lists:
      - name: comments
        table: demo_comment
        reference_field: task_id
        columns: [body]
    security:
      rules:
        - name: state_manager
          operation: U
          field: state
          role: task_manager
          order: 20
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}

	explorerTable, objects := buildYAMLAppEditorTable(context.Background(), appRegistryItem{
		Name:            "demo",
		Namespace:       "demo",
		DraftDefinition: definition,
	}, definition.Tables[0])

	if got := len(explorerTable.DataPolicies); got != 1 {
		t.Fatalf("len(DataPolicies) = %d, want 1", got)
	}
	if got := explorerTable.DataPolicies[0].ID; got != "table-data-policy:demo_task:require_number" {
		t.Fatalf("data policy id = %q", got)
	}
	if got := len(explorerTable.TriggerItems); got != 1 {
		t.Fatalf("len(TriggerItems) = %d, want 1", got)
	}
	if got := explorerTable.TriggerItems[0].ID; got != "table-trigger:demo_task:stamp_number" {
		t.Fatalf("table trigger id = %q", got)
	}
	if got := len(explorerTable.RelatedLists); got != 1 {
		t.Fatalf("len(RelatedLists) = %d, want 1", got)
	}
	if got := explorerTable.RelatedLists[0].ID; got != "table-related-list:demo_task:comments" {
		t.Fatalf("related list id = %q", got)
	}
	if got := len(explorerTable.SecurityItems); got != 1 {
		t.Fatalf("len(SecurityItems) = %d, want 1", got)
	}
	if got := explorerTable.SecurityItems[0].ID; got != "table-security-rule:demo_task:state_manager" {
		t.Fatalf("security rule id = %q", got)
	}

	objectIDs := map[string]bool{}
	for _, object := range objects {
		objectIDs[object.ID] = true
	}
	for _, want := range []string{
		"table-data-policy:demo_task:require_number",
		"table-trigger:demo_task:stamp_number",
		"table-related-list:demo_task:comments",
		"table-security-rule:demo_task:state_manager",
	} {
		if !objectIDs[want] {
			t.Fatalf("expected object %q in object list", want)
		}
	}
}
