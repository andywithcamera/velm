package main

// Slice 2 of the task definition editor: the inputs designer. These tests need
// no database; the ones that do live in
// handleTaskDefinitionsInputs_integration_test.go and are gated on
// TEST_DATABASE_URL.

import (
	"strings"
	"testing"

	"velm/internal/db"
)

// AC-34, AC-35: the gate must accept anything the struct models and refuse
// anything it would silently drop.
func TestInspectTaskInputSchemaGate(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		lossless bool
		reason   string
	}{
		{"empty", "", true, ""},
		{"empty list", "[]", true, ""},
		{"comment only", "# nothing declared yet\n", true, ""},
		{"plain document", "- name: server\n  data_type: text\n", true, ""},
		{"omitted zero keys are not lossy", "- name: server\n", true, ""},
		{"choices empty list", "- name: pick\n  data_type: choice\n  choices: []\n", true, ""},
		{"reference type is modellable", "- name: target\n  data_type: 'reference:table'\n", true, ""},
		{"reference type survives normalisation", "- name: target\n  data_type: 'reference:Tables'\n", true, ""},
		{"unknown key", "- name: server\n  data_type: text\n  colour: red\n", false, "contains keys this form does not model: colour"},
		{"unknown key inside a choice", "- name: pick\n  data_type: choice\n  choices:\n    - value: a\n      label: A\n      hidden: true\n", false, "choices.hidden"},
		{"unparseable", "- name: [\n", false, "does not parse"},
		{"duplicate names", "- name: twice\n- name: twice\n", false, `declares "twice" twice`},
		{"non-string value", "- name: 5\n", false, "does not parse"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inspection := db.InspectTaskInputSchema(tc.raw)
			if inspection.Lossless != tc.lossless {
				t.Fatalf("Lossless = %v, want %v (reason %q)", inspection.Lossless, tc.lossless, inspection.Reason)
			}
			if tc.reason != "" && !strings.Contains(inspection.Reason, tc.reason) {
				t.Errorf("reason = %q, want it to contain %q", inspection.Reason, tc.reason)
			}
			if !inspection.Lossless && inspection.Reason == "" {
				t.Error("the gate refused the document without saying why")
			}
		})
	}
}

// AC-22, AC-36: the writer emits all seven keys, and its own output passes the
// gate. This is what stops a form save from disabling the row editor.
func TestMarshalTaskInputSchemaEmitsAllKeysAndPassesTheGate(t *testing.T) {
	written, err := db.MarshalTaskInputSchema([]db.TaskInputField{
		{Name: "server", Label: "Server", DataType: "text", IsNullable: false},
		{Name: "ticket", DataType: "text", IsNullable: true, DefaultValue: "none", HelpText: "the ticket"},
		{Name: "pick", DataType: "choice", IsNullable: true, Choices: []db.ChoiceOption{{Value: "a", Label: "A"}}},
	})
	if err != nil {
		t.Fatalf("MarshalTaskInputSchema: %v", err)
	}
	for _, key := range db.TaskInputFieldKeys {
		count := 0
		for _, line := range strings.Split(written, "\n") {
			// Only top-level item keys count: a choice pair carries its own label.
			content := line
			if strings.HasPrefix(content, "- ") {
				content = "  " + strings.TrimPrefix(content, "- ")
			}
			indent := len(content) - len(strings.TrimLeft(content, " "))
			if indent == 2 && strings.HasPrefix(strings.TrimSpace(content), key+":") {
				count++
			}
		}
		if count != 3 {
			t.Errorf("key %q appears %d times, want 3 (one per field):\n%s", key, count, written)
		}
	}
	if !strings.Contains(written, "is_nullable: false") {
		t.Errorf("writer omitted is_nullable: false:\n%s", written)
	}
	if !strings.Contains(written, "choices: []") {
		t.Errorf("writer omitted an empty choices list:\n%s", written)
	}

	inspection := db.InspectTaskInputSchema(written)
	if !inspection.Lossless {
		t.Fatalf("the writer's own output fails the gate: %s\n%s", inspection.Reason, written)
	}
	if len(inspection.Fields) != 3 || inspection.Fields[0].Name != "server" || inspection.Fields[2].Name != "pick" {
		t.Errorf("re-parse lost order: %+v", inspection.Fields)
	}
}

// AC-22: no rows writes an empty column, which the parser reads as "no inputs".
func TestMarshalTaskInputSchemaEmpty(t *testing.T) {
	written, err := db.MarshalTaskInputSchema(nil)
	if err != nil {
		t.Fatalf("MarshalTaskInputSchema: %v", err)
	}
	if written != "" {
		t.Errorf("empty field list wrote %q, want an empty column", written)
	}
	if inspection := db.InspectTaskInputSchema(written); !inspection.Lossless || len(inspection.Fields) != 0 {
		t.Errorf("empty output did not inspect as an empty, lossless list: %+v", inspection)
	}
}

// AC-29: the offered vocabulary is the renderable set, with no reference: or
// code: type.
func TestTaskInputDataTypeOptionsAreTheRenderableSet(t *testing.T) {
	want := []string{"text", "long_text", "richtext", "number", "integer", "decimal", "date", "datetime", "email", "url", "boolean", "choice"}
	if len(taskInputDataTypeOptions) != len(want) {
		t.Fatalf("offered %d types, want %d: %v", len(taskInputDataTypeOptions), len(want), taskInputDataTypeOptions)
	}
	for index, expected := range want {
		if taskInputDataTypeOptions[index] != expected {
			t.Errorf("type %d = %q, want %q", index, taskInputDataTypeOptions[index], expected)
		}
	}
	for _, option := range taskInputDataTypeOptions {
		if strings.HasPrefix(option, "reference:") || strings.HasPrefix(option, "code:") {
			t.Errorf("the designer offers %q, which the catalog cannot render", option)
		}
	}
	if taskInputDataTypeOffered("reference:table") || taskInputDataTypeOffered("code:go") {
		t.Error("a non-renderable type is reported as offered")
	}
}

// AC-31, AC-32: the parser's message is matched to the row it belongs to. The
// scan finds the row; it does not decide anything.
func TestAttributeInputSchemaError(t *testing.T) {
	rows := []submittedInputRow{{Name: "server"}, {Name: "reason"}, {Name: "server"}}
	cases := []struct {
		message string
		want    int
	}{
		{`input schema declares "server" twice`, 2},
		{`input schema field "reason" is not a valid name (letters, digits, underscore)`, 1},
		{`input schema field 2 has no name`, 1},
		{`input schema field 9 has no name`, -1},
		{"something else entirely", -1},
	}
	for _, tc := range cases {
		if got := attributeInputSchemaError(tc.message, rows); got != tc.want {
			t.Errorf("attribute(%q) = %d, want %d", tc.message, got, tc.want)
		}
	}
}
