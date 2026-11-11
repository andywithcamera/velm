package db

import (
	"reflect"
	"testing"
)

// Routing is configured on the definition, so the assignee column and its form
// field have to be injected without disturbing the order an author sees. It sits
// directly after the group because the two are one decision: which queue, and
// which person in it.
func TestEnsureTaskDefinitionAssigneeColumn(t *testing.T) {
	definition := func() *AppDefinition {
		return &AppDefinition{
			Tables: []AppDefinitionTable{{
				Name: "base_task_definition",
				Columns: []AppDefinitionColumn{
					{Name: "display_name", DataType: "string"},
					{Name: "default_group_id", DataType: "reference"},
					{Name: "instructions", DataType: "text"},
				},
				Forms: []AppDefinitionForm{{
					Name:   "main",
					Fields: []string{"display_name", "default_group_id", "instructions"},
				}},
			}},
		}
	}

	names := func(columns []AppDefinitionColumn) []string {
		out := make([]string, len(columns))
		for i, column := range columns {
			out[i] = column.Name
		}
		return out
	}

	t.Run("inserts the column after the group", func(t *testing.T) {
		def := definition()
		if !ensureTaskDefinitionAssigneeColumn(def) {
			t.Fatal("expected the definition to be changed")
		}
		want := []string{"display_name", "default_group_id", "default_assignee_id", "instructions"}
		if got := names(def.Tables[0].Columns); !reflect.DeepEqual(got, want) {
			t.Fatalf("column order = %v, want %v", got, want)
		}
		column := def.Tables[0].Columns[2]
		if column.DataType != "reference" || !column.IsNullable {
			t.Fatalf("assignee column = %+v, want a nullable reference", column)
		}
		if column.ReferenceTable != "_user" {
			t.Fatalf("reference table = %q, want _user", column.ReferenceTable)
		}
	})

	t.Run("inserts the form field after the group", func(t *testing.T) {
		def := definition()
		ensureTaskDefinitionAssigneeColumn(def)
		want := []string{"display_name", "default_group_id", "default_assignee_id", "instructions"}
		if got := def.Tables[0].Forms[0].Fields; !reflect.DeepEqual(got, want) {
			t.Fatalf("form fields = %v, want %v", got, want)
		}
	})

	t.Run("is idempotent", func(t *testing.T) {
		def := definition()
		if !ensureTaskDefinitionAssigneeColumn(def) {
			t.Fatal("first run should change the definition")
		}
		if ensureTaskDefinitionAssigneeColumn(def) {
			t.Fatal("second run should be a no-op, or every boot rewrites the app")
		}
		if got := names(def.Tables[0].Columns); len(got) != 4 {
			t.Fatalf("columns = %v, want the assignee column added exactly once", got)
		}
	})

	t.Run("appends when there is no group column", func(t *testing.T) {
		def := &AppDefinition{Tables: []AppDefinitionTable{{
			Name:    "base_task_definition",
			Columns: []AppDefinitionColumn{{Name: "display_name"}},
		}}}
		if !ensureTaskDefinitionAssigneeColumn(def) {
			t.Fatal("expected the definition to be changed")
		}
		want := []string{"display_name", "default_assignee_id"}
		if got := names(def.Tables[0].Columns); !reflect.DeepEqual(got, want) {
			t.Fatalf("column order = %v, want %v", got, want)
		}
	})

	t.Run("ignores other tables and nil definitions", func(t *testing.T) {
		if ensureTaskDefinitionAssigneeColumn(nil) {
			t.Fatal("nil definition should be a no-op")
		}
		def := &AppDefinition{Tables: []AppDefinitionTable{{
			Name:    "base_task",
			Columns: []AppDefinitionColumn{{Name: "title"}},
		}}}
		if ensureTaskDefinitionAssigneeColumn(def) {
			t.Fatal("only base_task_definition should be touched")
		}
		if got := names(def.Tables[0].Columns); len(got) != 1 {
			t.Fatalf("unrelated table was modified: %v", got)
		}
	})
}
