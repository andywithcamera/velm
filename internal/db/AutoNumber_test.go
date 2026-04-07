package db

import "testing"

func TestDerivedTaskAutoNumberPrefixPrefersSingularLabel(t *testing.T) {
	app := RegisteredApp{Name: "devworks", Namespace: "dw"}
	table := AppDefinitionTable{
		Name:          "dw_task",
		LabelSingular: "Sub-task",
	}

	if got := derivedTaskAutoNumberPrefix(app, table); got != "SUBT" {
		t.Fatalf("derivedTaskAutoNumberPrefix() = %q, want %q", got, "SUBT")
	}
}

func TestDerivedTaskAutoNumberPrefixFallsBackToOwnedTableName(t *testing.T) {
	app := RegisteredApp{Name: "devworks", Namespace: "dw"}
	table := AppDefinitionTable{Name: "dw_story"}

	if got := derivedTaskAutoNumberPrefix(app, table); got != "STOR" {
		t.Fatalf("derivedTaskAutoNumberPrefix() = %q, want %q", got, "STOR")
	}
}

func TestResolveDefinitionColumnsWithAppsAppliesDerivedTaskNumberPrefix(t *testing.T) {
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
    label_singular: Story
    columns:
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
	if len(resolved) < 1 {
		t.Fatal("expected inherited number column")
	}
	if resolved[0].Name != "number" {
		t.Fatalf("expected first resolved column to be number, got %#v", resolved[0])
	}
	if got := resolved[0].Prefix; got != "STOR" {
		t.Fatalf("resolved number prefix = %q, want %q", got, "STOR")
	}
}

func TestBuildAutoNumberTriggerConfigsUsesDerivedTaskPrefix(t *testing.T) {
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
    label_singular: Story
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition(child) error = %v", err)
	}

	baseApp := RegisteredApp{Name: "base", Namespace: "base", Definition: baseDefinition}
	childApp := RegisteredApp{Name: "devworks", Namespace: "dw", Definition: childDefinition}

	resolvedTable := childDefinition.Tables[0]
	resolvedColumns, err := resolveDefinitionColumnsWithApps(
		[]RegisteredApp{baseApp, childApp},
		childApp,
		resolvedTable,
		map[string]bool{},
	)
	if err != nil {
		t.Fatalf("resolveDefinitionColumnsWithApps() error = %v", err)
	}
	resolvedTable.Columns = resolvedColumns

	configs, err := buildAutoNumberTriggerConfigs("dw", resolvedTable)
	if err != nil {
		t.Fatalf("buildAutoNumberTriggerConfigs() error = %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 autnumber config, got %d", len(configs))
	}
	if got := configs[0].Prefix; got != "STOR" {
		t.Fatalf("config prefix = %q, want %q", got, "STOR")
	}
	if got := configs[0].AppPrefix; got != "dw_STOR" {
		t.Fatalf("config app prefix = %q, want %q", got, "dw_STOR")
	}
}
