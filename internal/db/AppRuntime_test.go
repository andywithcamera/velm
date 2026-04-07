package db

import "testing"

func TestResolveAppRuntimeEndpointWithAppsFindsPublishedScriptEndpoint(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: devworks
namespace: dw
services:
  - name: board
    methods:
      - name: data
        visibility: public
        script: |
          function run(ctx) { return { ok: true }; }
endpoints:
  - name: board_data
    enabled: true
    method: post
    path: /api/devworks/board-data
    call: board.data
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}

	endpoint := resolveAppRuntimeEndpointWithApps([]RegisteredApp{{
		Name:       "devworks",
		Namespace:  "dw",
		Definition: definition,
	}}, "POST", "/api/devworks/board-data")

	if endpoint.App.Name != "devworks" {
		t.Fatalf("expected devworks app, got %#v", endpoint.App)
	}
	if endpoint.Endpoint.Name != "board_data" {
		t.Fatalf("expected board_data endpoint, got %#v", endpoint.Endpoint)
	}
}

func TestResolveRuntimeServiceMethodWithAppsAllowsDependencyPublicOnly(t *testing.T) {
	baseDefinition, err := ParseAppDefinition(`
name: base
namespace: base
services:
  - name: release
    methods:
      - name: generate_notes
        visibility: public
        script: |
          function run(ctx) { return "ok"; }
      - name: secret
        visibility: private
        script: |
          function run(ctx) { return "nope"; }
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition(base) error = %v", err)
	}
	childDefinition, err := ParseAppDefinition(`
name: devworks
namespace: dw
dependencies:
  - base
services:
  - name: story
    methods:
      - name: handle_write
        visibility: private
        script: |
          function run(ctx) { return "local"; }
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition(child) error = %v", err)
	}

	apps := []RegisteredApp{
		{Name: "base", Namespace: "base", Definition: baseDefinition},
		{Name: "devworks", Namespace: "dw", Definition: childDefinition},
	}

	app, method, ok := resolveRuntimeServiceMethodWithApps(apps, apps[1], "release.generate_notes", false)
	if !ok {
		t.Fatal("expected dependency public method to resolve")
	}
	if app.Name != "base" || method.Name != "generate_notes" {
		t.Fatalf("resolved wrong method: app=%q method=%q", app.Name, method.Name)
	}

	if _, _, ok := resolveRuntimeServiceMethodWithApps(apps, apps[1], "release.secret", false); ok {
		t.Fatal("expected dependency private method to remain inaccessible")
	}
}

func TestResolveRuntimeTriggersWithAppsCombinesAndSortsTriggers(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: devworks
namespace: dw
tables:
  - name: dw_story
    triggers:
      - name: late_table_trigger
        event: record.update
        call: story.table_late
        mode: sync
        order: 20
        enabled: true
      - name: early_table_trigger
        event: record.update
        call: story.table_early
        mode: sync
        order: 5
        enabled: true
triggers:
  - name: root_trigger
    table: dw_story
    event: record.update
    call: story.root
    mode: sync
    order: 10
    enabled: true
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}

	triggers := resolveRuntimeTriggersWithApps([]RegisteredApp{{
		Name:       "devworks",
		Namespace:  "dw",
		Definition: definition,
	}}, "dw_story", "record.update")

	if len(triggers) != 3 {
		t.Fatalf("expected 3 triggers, got %d", len(triggers))
	}
	if triggers[0].Trigger.Name != "early_table_trigger" || triggers[1].Trigger.Name != "root_trigger" || triggers[2].Trigger.Name != "late_table_trigger" {
		t.Fatalf("unexpected trigger order: %#v", triggers)
	}
}
