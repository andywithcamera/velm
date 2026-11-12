package db

import (
	"strings"
	"testing"
)

func TestParseTaskInputSchema(t *testing.T) {
	fields, err := ParseTaskInputSchema(`
- name: body
  label: What do you need?
  data_type: long_text
  is_nullable: false
- name: priority
  label: Priority
  data_type: choice
  choices:
    - value: low
      label: Low
    - value: high
      label: High
- name: window_start
  data_type: datetime
  is_nullable: true
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("got %d fields, want 3", len(fields))
	}
	if fields[0].LabelOrName() != "What do you need?" {
		t.Errorf("label = %q", fields[0].LabelOrName())
	}
	if fields[0].InputControlKind() != "textarea" {
		t.Errorf("body control = %q, want textarea", fields[0].InputControlKind())
	}
	if !fields[0].IsRequired() {
		t.Error("body should be required")
	}
	if fields[1].InputControlKind() != "select" {
		t.Errorf("priority control = %q, want select", fields[1].InputControlKind())
	}
	// A label is not declared, so the name is humanised rather than blank.
	if fields[2].LabelOrName() != "Window Start" {
		t.Errorf("humanised label = %q", fields[2].LabelOrName())
	}
	if fields[2].IsRequired() {
		t.Error("is_nullable: true field should be optional")
	}
	// Omission means required, matching app column semantics.
	if !fields[1].IsRequired() {
		t.Error("field with no is_nullable should be required")
	}
}

func TestParseTaskInputSchemaEmptyIsNoInputs(t *testing.T) {
	for _, raw := range []string{"", "   ", "[]"} {
		fields, err := ParseTaskInputSchema(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if len(fields) != 0 {
			t.Errorf("parse %q returned %d fields", raw, len(fields))
		}
	}
}

func TestParseTaskInputSchemaRejectsBadNames(t *testing.T) {
	cases := map[string]string{
		"no name":         "- label: Something\n  data_type: string\n",
		"unsafe name":     "- name: \"drop table\"\n  data_type: string\n",
		"underscore lead": "- name: _hidden\n  data_type: string\n",
		"duplicate names": "- name: body\n  data_type: string\n- name: body\n  data_type: string\n",
	}
	for name, raw := range cases {
		if _, err := ParseTaskInputSchema(raw); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestTaskInputValuesCoercesAndValidates(t *testing.T) {
	fields, err := ParseTaskInputSchema(`
- name: body
  label: What do you need?
  data_type: long_text
- name: priority
  label: Priority
  data_type: choice
  choices:
    - value: low
      label: Low
    - value: high
      label: High
- name: count
  data_type: integer
- name: urgent
  data_type: boolean
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	values, err := TaskInputValues(fields, map[string]string{
		"body":     "write me a doc about cheese",
		"priority": "high",
		"count":    "3",
		"urgent":   "yes",
	})
	if err != nil {
		t.Fatalf("values: %v", err)
	}
	if values["body"] != "write me a doc about cheese" {
		t.Errorf("body = %v", values["body"])
	}
	if values["priority"] != "high" {
		t.Errorf("priority = %v", values["priority"])
	}
	if values["count"] != float64(3) {
		t.Errorf("count = %#v, want float64(3)", values["count"])
	}
	if values["urgent"] != true {
		t.Errorf("urgent = %#v, want true", values["urgent"])
	}
}

func TestTaskInputValuesReportsEveryProblemAtOnce(t *testing.T) {
	fields, err := ParseTaskInputSchema(`
- name: body
  label: Brief
  data_type: long_text
- name: priority
  label: Priority
  data_type: choice
  choices:
    - value: low
      label: Low
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = TaskInputValues(fields, map[string]string{"priority": "nonsense"})
	if err == nil {
		t.Fatal("expected an error")
	}
	message := err.Error()
	if !strings.Contains(message, "Brief is required") || !strings.Contains(message, "Priority must be one of the listed options") {
		t.Errorf("error should name both problems, got %q", message)
	}
}

func TestTaskInputValuesUsesDefaultWhenBlank(t *testing.T) {
	fields, err := ParseTaskInputSchema(`
- name: priority
  label: Priority
  data_type: choice
  default_value: low
  choices:
    - value: low
      label: Low
    - value: high
      label: High
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	values, err := TaskInputValues(fields, map[string]string{})
	if err != nil {
		t.Fatalf("values: %v", err)
	}
	if values["priority"] != "low" {
		t.Errorf("priority = %v, want the declared default", values["priority"])
	}
}

// A plain text input is one line. Mapping it to a textarea made every
// short answer render as a paragraph box.
func TestTaskInputControlKinds(t *testing.T) {
	cases := map[string]string{
		"text":      "input",
		"string":    "input",
		"long_text": "textarea",
		"richtext":  "textarea",
		"choice":    "select",
		"boolean":   "checkbox",
		"number":    "input",
	}
	for dataType, want := range cases {
		field := TaskInputField{Name: "f", DataType: dataType}
		if got := field.InputControlKind(); got != want {
			t.Errorf("data_type %q control = %q, want %q", dataType, got, want)
		}
	}
}

// An unticked checkbox submits nothing. If absence were treated as "missing",
// a required boolean could never be answered false.
func TestTaskInputValuesAllowsARequiredBooleanToBeFalse(t *testing.T) {
	fields := []TaskInputField{{Name: "approved", DataType: "boolean", IsNullable: false}}
	values, err := TaskInputValues(fields, map[string]string{})
	if err != nil {
		t.Fatalf("unticked required checkbox rejected: %v", err)
	}
	if values["approved"] != false {
		t.Fatalf("approved = %v, want false", values["approved"])
	}
}
