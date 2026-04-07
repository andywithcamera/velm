package db

import "testing"

func TestBuildScriptRegistryEntriesUsesDraftDefinitionAndSorts(t *testing.T) {
	t.Parallel()

	apps := []RegisteredApp{
		{
			Name:  "system",
			Label: "System",
			Definition: &AppDefinition{
				Services: []AppDefinitionService{
					{
						Name: "core",
						Methods: []AppDefinitionMethod{
							{Name: "bootstrap", Visibility: "public", Language: "javascript", Script: "async function run(ctx) {}"},
						},
					},
				},
			},
		},
		{
			Name:  "helpdesk",
			Label: "Helpdesk",
			DraftDefinition: &AppDefinition{
				Services: []AppDefinitionService{
					{
						Name: "ticket",
						Methods: []AppDefinitionMethod{
							{Name: "assign_owner", Label: "Assign Owner", Visibility: "private", Language: "javascript", Script: "async function run(ctx) {}"},
							{Name: "close_ticket", Visibility: "public", Language: "javascript", Script: ""},
						},
					},
				},
				Triggers: []AppDefinitionTrigger{
					{Name: "assign_owner_trigger", Table: "helpdesk_ticket", Event: "record.update", Call: "ticket.assign_owner", Enabled: true},
				},
			},
			Definition: &AppDefinition{
				Services: []AppDefinitionService{
					{
						Name: "published",
						Methods: []AppDefinitionMethod{
							{Name: "only", Visibility: "public", Language: "javascript", Script: "async function run(ctx) {}"},
						},
					},
				},
			},
		},
	}

	items := buildScriptRegistryEntries(apps)
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	if items[0].AppName != "helpdesk" || items[0].MethodName != "assign_owner" {
		t.Fatalf("first item = %#v", items[0])
	}
	if items[1].AppName != "helpdesk" || items[1].MethodName != "close_ticket" {
		t.Fatalf("second item = %#v", items[1])
	}
	if items[2].AppName != "system" || items[2].MethodName != "bootstrap" {
		t.Fatalf("third item = %#v", items[2])
	}
	if items[0].ID != SyntheticYAMLMethodID("helpdesk", "ticket", "assign_owner") {
		t.Fatalf("unexpected script id %d", items[0].ID)
	}
	if items[0].Call != "ticket.assign_owner" {
		t.Fatalf("Call = %q, want %q", items[0].Call, "ticket.assign_owner")
	}
	if items[0].BindingCount != 1 {
		t.Fatalf("BindingCount = %d, want 1", items[0].BindingCount)
	}
	if items[1].LatestVersion != 0 {
		t.Fatalf("LatestVersion = %d, want 0", items[1].LatestVersion)
	}
	if items[2].PublishedAt == "" {
		t.Fatalf("expected published method to expose PublishedAt marker")
	}
}

func TestResolveYAMLMethodByIDAndBuildVersions(t *testing.T) {
	t.Parallel()

	apps := []RegisteredApp{
		{
			Name:  "system",
			Label: "System",
			DraftDefinition: &AppDefinition{
				Services: []AppDefinitionService{
					{
						Name: "core",
						Methods: []AppDefinitionMethod{
							{Name: "bootstrap", Visibility: "public", Language: "javascript", Script: "async function run(ctx) { return true; }"},
						},
					},
				},
			},
		},
	}

	app, service, method, ok := resolveYAMLMethodByID(apps, SyntheticYAMLMethodID("system", "core", "bootstrap"))
	if !ok {
		t.Fatalf("expected method to resolve by synthetic id")
	}
	if app.Name != "system" || service.Name != "core" || method.Name != "bootstrap" {
		t.Fatalf("resolved (%q, %q, %q)", app.Name, service.Name, method.Name)
	}

	versions := buildYAMLMethodVersions(app, service, method)
	if len(versions) != 1 {
		t.Fatalf("len(versions) = %d, want 1", len(versions))
	}
	if versions[0].VersionNum != 1 {
		t.Fatalf("VersionNum = %d, want 1", versions[0].VersionNum)
	}
	if versions[0].Checksum == "" {
		t.Fatalf("expected checksum to be populated")
	}
	if versions[0].PublishedAt == "" {
		t.Fatalf("expected published version marker")
	}
}

func TestCountYAMLScriptDependenciesHelpers(t *testing.T) {
	t.Parallel()

	apps := []RegisteredApp{
		{
			Name: "helpdesk",
			DraftDefinition: &AppDefinition{
				Triggers: []AppDefinitionTrigger{
					{Name: "assign_owner", Table: "helpdesk_ticket", Condition: "record.state == \"open\"", Call: "ticket.assign_owner"},
				},
				ClientScripts: []AppDefinitionClientScript{
					{Name: "priority_client", Table: "helpdesk_ticket", Field: "priority"},
					{Name: "notify_client", Table: "helpdesk_task", Field: "status"},
				},
			},
		},
	}

	if got := countYAMLTableScriptDependencies(apps, "helpdesk_ticket"); got != 2 {
		t.Fatalf("countYAMLTableScriptDependencies() = %d, want 2", got)
	}
	if got := countYAMLColumnConditionDependencies(apps, "helpdesk_ticket", "priority"); got != 1 {
		t.Fatalf("countYAMLColumnConditionDependencies(priority) = %d, want 1", got)
	}
	if got := countYAMLColumnConditionDependencies(apps, "helpdesk_ticket", "state"); got != 1 {
		t.Fatalf("countYAMLColumnConditionDependencies(state) = %d, want 1", got)
	}
	if got := countYAMLColumnConditionDependencies(apps, "helpdesk_ticket", "missing"); got != 0 {
		t.Fatalf("countYAMLColumnConditionDependencies(missing) = %d, want 0", got)
	}
}
