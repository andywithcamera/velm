package main

import (
	"bytes"
	"strings"
	"testing"

	"velm/internal/db"
)

// Plan T2: the Assigned To picker is scoped to the task's assignment group in
// the browser as well as on the server. The server half is asserted in
// assignment_group_scope_integration_test.go; this asserts that the rendered
// form carries the scope and that the script that uses it survives into the
// page (html/template silently drops script text after a stray {{end}}).
func TestAssignedToPickerIsScopedToAssignmentGroup(t *testing.T) {
	tmpl := parseFormTemplates(t)

	columns := []db.Column{
		{NAME: "assignment_group_id", DATA_TYPE: "reference", IS_NULLABLE: true, LABEL: "Assignment Group"},
		{NAME: "assigned_user_id", DATA_TYPE: "reference", IS_NULLABLE: true, LABEL: "Assigned To"},
		{NAME: "title", DATA_TYPE: "text", IS_NULLABLE: false, LABEL: "Title"},
	}
	configs := map[string]formFieldConfig{
		"assignment_group_id": {Kind: "reference", ReferenceTo: "_group", ReferenceTableLabel: "Group"},
		"assigned_user_id":    {Kind: "reference", ReferenceTo: "_user", ReferenceTableLabel: "User"},
		"title":               {Kind: "text"},
	}

	render := func(tableName string) string {
		t.Helper()
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "form-view", map[string]any{
			"FormTable":           tableName,
			"FormID":              "11111111-1111-1111-1111-111111111111",
			"FormTitle":           "Task",
			"FormTableLabel":      "Task",
			"FormColumns":         columns,
			"FormFieldConfigs":    configs,
			"FormFieldValues":     map[string]string{},
			"FormExpectedVersion": "",
		}); err != nil {
			t.Fatalf("execute form-view: %v", err)
		}
		return buf.String()
	}

	out := render("base_task")
	if !strings.Contains(out, `data-reference-scope-field="assignment_group_id"`) {
		t.Fatal("Assigned To picker is not scoped to assignment_group_id")
	}
	if got := strings.Count(out, `data-reference-scope-field=`); got != 1 {
		t.Fatalf("scope attribute appears %d times, want exactly once (Assigned To only)", got)
	}
	for _, want := range []string{
		"&group_id=${encodeURIComponent(scopeValue)}",
		"scopeField.addEventListener(\"change\", clearOnScopeChange)",
		"setPickerValue(picker, \"\", \"\")",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered form is missing %q; the picker script did not reach the page", want)
		}
	}

	if other := render("story"); strings.Contains(other, "data-reference-scope-field=") {
		t.Fatal("a non-task form must not scope its reference picker")
	}
}
