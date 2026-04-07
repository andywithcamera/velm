package db

import (
	"reflect"
	"testing"
)

func TestDescendantTableNamesFromAppsIncludesRecursiveChildren(t *testing.T) {
	baseDef := &AppDefinition{
		Name: "base",
		Tables: []AppDefinitionTable{
			{Name: "base_task"},
		},
	}
	devworksDef := &AppDefinition{
		Name:         "devworks",
		Dependencies: []string{"base"},
		Tables: []AppDefinitionTable{
			{Name: "dw_story", Extends: "base_task"},
			{Name: "dw_task", Extends: "base_task"},
		},
	}
	qaDef := &AppDefinition{
		Name:         "qa",
		Dependencies: []string{"devworks"},
		Tables: []AppDefinitionTable{
			{Name: "qa_story_check", Extends: "dw_story"},
			{Name: "qa_case"},
		},
	}

	apps := []RegisteredApp{
		{Name: "base", Definition: baseDef},
		{Name: "devworks", Definition: devworksDef},
		{Name: "qa", Definition: qaDef},
	}

	got := descendantTableNamesFromApps(apps, "base_task")
	want := []string{"dw_story", "dw_task", "qa_story_check"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("descendantTableNamesFromApps() = %v, want %v", got, want)
	}
}
