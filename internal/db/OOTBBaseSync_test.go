package db

import "testing"

func TestComposeOOTBBaseDefinitionMergesSystemAndBase(t *testing.T) {
	t.Parallel()

	systemDefinition := &AppDefinition{
		Name:      "system",
		Namespace: "",
		Tables: []AppDefinitionTable{
			{
				Name: "_user",
				Columns: []AppDefinitionColumn{
					{Name: "_id", DataType: "uuid"},
					{Name: "name", DataType: "text"},
				},
			},
		},
		Pages: []AppDefinitionPage{{
			Name:   "Landing",
			Slug:   "landing",
			Status: "published",
		}},
	}

	baseDefinition := &AppDefinition{
		Name:         "base",
		Namespace:    "base",
		Dependencies: []string{"system"},
		Tables: []AppDefinitionTable{
			{
				Name: "base_task",
				Columns: []AppDefinitionColumn{
					{Name: "number", DataType: "autnumber", Prefix: "TASK"},
				},
			},
		},
	}

	composed := composeOOTBBaseDefinition(systemDefinition, baseDefinition)
	if composed == nil {
		t.Fatal("expected composed definition")
	}
	if composed.Name != "base" || composed.Namespace != "" {
		t.Fatalf("unexpected composed identity %#v", composed)
	}
	if len(composed.Dependencies) != 0 {
		t.Fatalf("expected system/base dependencies to collapse, got %#v", composed.Dependencies)
	}
	if len(composed.Tables) != 2 {
		t.Fatalf("expected merged tables, got %d", len(composed.Tables))
	}
	if composed.Tables[0].Name != "_user" || composed.Tables[1].Name != "base_task" {
		t.Fatalf("unexpected merged table order %#v", composed.Tables)
	}
	if len(composed.Pages) != 1 || composed.Pages[0].Slug != "landing" {
		t.Fatalf("expected merged landing page, got %#v", composed.Pages)
	}
}

func TestFindRegisteredAppByNameOrNamespaceUsesSystemAliasForOOTBBase(t *testing.T) {
	t.Parallel()

	apps := []RegisteredApp{
		{Name: "base", Namespace: "", Label: "Base"},
		{Name: "helpdesk", Namespace: "hd", Label: "Helpdesk"},
	}

	app, ok := findRegisteredAppByNameOrNamespace(apps, "system")
	if !ok {
		t.Fatal("expected system alias to resolve")
	}
	if app.Name != "base" || app.Namespace != "" {
		t.Fatalf("resolved app = %#v", app)
	}
}
