package db

import "testing"

func TestResolveScriptScopePath(t *testing.T) {
	scope := ScriptScope{
		CurrentApp: ScriptScopeApp{Name: "itsm", Namespace: "itsm", Label: "ITSM"},
		DependencyApps: []ScriptScopeApp{
			{Name: "task_mgmt", Namespace: "task", Label: "Task"},
		},
		Objects: []ScriptScopeObject{
			{
				App:       ScriptScopeApp{Name: "itsm", Namespace: "itsm", Label: "ITSM"},
				TableName: "itsm_incident",
				Alias:     "incident",
				Path:      "incident",
				Columns: []ScriptScopeColumn{
					{Name: "resolution", Path: "incident.resolution"},
				},
			},
			{
				App:       ScriptScopeApp{Name: "task_mgmt", Namespace: "task", Label: "Task"},
				TableName: "_work",
				Alias:     "work",
				Path:      "task.work",
				Columns: []ScriptScopeColumn{
					{Name: "status", Path: "task.work.status"},
				},
			},
		},
	}

	tests := []struct {
		name       string
		path       string
		wantApp    string
		wantTable  string
		wantAlias  string
		wantColumn string
		wantPath   string
	}{
		{
			name:      "local table",
			path:      "incident",
			wantApp:   "itsm",
			wantTable: "itsm_incident",
			wantAlias: "incident",
			wantPath:  "incident",
		},
		{
			name:       "local column",
			path:       "incident.resolution",
			wantApp:    "itsm",
			wantTable:  "itsm_incident",
			wantAlias:  "incident",
			wantColumn: "resolution",
			wantPath:   "incident.resolution",
		},
		{
			name:      "dependency table",
			path:      "task.work",
			wantApp:   "task_mgmt",
			wantTable: "_work",
			wantAlias: "work",
			wantPath:  "task.work",
		},
		{
			name:       "dependency column",
			path:       "task.work.status",
			wantApp:    "task_mgmt",
			wantTable:  "_work",
			wantAlias:  "work",
			wantColumn: "status",
			wantPath:   "task.work.status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveScriptScopePath(scope, tt.path)
			if err != nil {
				t.Fatalf("ResolveScriptScopePath(%q) error = %v", tt.path, err)
			}
			if got.App.Name != tt.wantApp {
				t.Fatalf("app = %q, want %q", got.App.Name, tt.wantApp)
			}
			if got.TableName != tt.wantTable {
				t.Fatalf("table = %q, want %q", got.TableName, tt.wantTable)
			}
			if got.TableAlias != tt.wantAlias {
				t.Fatalf("alias = %q, want %q", got.TableAlias, tt.wantAlias)
			}
			if got.ColumnName != tt.wantColumn {
				t.Fatalf("column = %q, want %q", got.ColumnName, tt.wantColumn)
			}
			if got.Path != tt.wantPath {
				t.Fatalf("path = %q, want %q", got.Path, tt.wantPath)
			}
		})
	}
}

func TestResolveScriptScopePathRejectsOutOfScopeReferences(t *testing.T) {
	scope := ScriptScope{
		CurrentApp: ScriptScopeApp{Name: "itsm", Namespace: "itsm", Label: "ITSM"},
		DependencyApps: []ScriptScopeApp{
			{Name: "task_mgmt", Namespace: "task", Label: "Task"},
		},
		Objects: []ScriptScopeObject{
			{
				App:       ScriptScopeApp{Name: "itsm", Namespace: "itsm", Label: "ITSM"},
				TableName: "itsm_incident",
				Alias:     "incident",
				Path:      "incident",
				Columns: []ScriptScopeColumn{
					{Name: "resolution", Path: "incident.resolution"},
				},
			},
			{
				App:       ScriptScopeApp{Name: "task_mgmt", Namespace: "task", Label: "Task"},
				TableName: "_work",
				Alias:     "work",
				Path:      "task.work",
				Columns: []ScriptScopeColumn{
					{Name: "status", Path: "task.work.status"},
				},
			},
		},
	}

	tests := []string{
		"sales.order",
		"incident.missing",
		"task.work.missing",
		"task.unknown",
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			if _, err := ResolveScriptScopePath(scope, path); err == nil {
				t.Fatalf("ResolveScriptScopePath(%q) expected error", path)
			}
		})
	}
}

func TestScriptScopeAliasForTable(t *testing.T) {
	tests := []struct {
		name     string
		app      RegisteredApp
		table    string
		wantPath string
	}{
		{
			name:     "strip namespace prefix",
			app:      RegisteredApp{Name: "itsm", Namespace: "itsm"},
			table:    "itsm_incident",
			wantPath: "incident",
		},
		{
			name:     "strip leading underscore for core-style table",
			app:      RegisteredApp{Name: "task", Namespace: "task"},
			table:    "_work",
			wantPath: "work",
		},
		{
			name:     "fallback to raw table name",
			app:      RegisteredApp{Name: "ops", Namespace: "ops"},
			table:    "queue",
			wantPath: "queue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scriptScopeAliasForTable(tt.app, tt.table); got != tt.wantPath {
				t.Fatalf("scriptScopeAliasForTable(%q) = %q, want %q", tt.table, got, tt.wantPath)
			}
		})
	}
}
