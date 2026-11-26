package db

import "testing"

// Inputs reach the record form typed and in declaration order (#383). The card
// this replaced flattened everything to prose; a form has to know a choice is a
// choice.
func TestTaskRequestInputFieldsFollowTheSchema(t *testing.T) {
	declared := []TaskInputField{
		{Name: "summary", Label: "What is this request?", DataType: "text", IsNullable: false},
		{Name: "detail", Label: "Describe it", DataType: "long_text", HelpText: "Plain words are fine."},
		{
			Name:     "priority",
			Label:    "Priority",
			DataType: "choice",
			Choices:  []ChoiceOption{{Value: "high", Label: "High"}},
		},
	}
	values := map[string]any{"summary": "Test", "priority": "high"}

	fields := taskRequestInputFields(values, declared)
	if len(fields) != 3 {
		t.Fatalf("fields = %+v, want all three declared inputs in schema order", fields)
	}
	if fields[0].Name != "summary" || fields[0].Value != "Test" {
		t.Fatalf("fields[0] = %+v", fields[0])
	}
	if fields[0].Label != "What is this request?" || fields[0].IsNullable {
		t.Fatalf("fields[0] label/nullable = %+v", fields[0])
	}
	// A declared input with no value still renders: it is part of the shape of
	// what was asked for.
	if fields[1].Value != "" || !fields[1].Declared {
		t.Fatalf("fields[1] = %+v, want an empty declared field", fields[1])
	}
	if fields[1].HelpText != "Plain words are fine." {
		t.Fatalf("help text = %q", fields[1].HelpText)
	}
	if fields[2].DataType != "choice" || len(fields[2].Choices) != 1 || fields[2].Choices[0].Label != "High" {
		t.Fatalf("fields[2] = %+v, want the choice and its options", fields[2])
	}
	// The raw value, not the label: the select matches on value.
	if fields[2].Value != "high" {
		t.Fatalf("choice value = %q, want high", fields[2].Value)
	}
}

// A value that flowed in from a predecessor has no schema entry. It is still
// worth reading, so it shows as plain text after the declared inputs.
func TestTaskRequestInputFieldsIncludeUndeclaredValues(t *testing.T) {
	declared := []TaskInputField{{Name: "detail", DataType: "long_text"}}
	values := map[string]any{
		"detail":  "the brief",
		"doc_id":  "DOC-000012",
		"amounts": map[string]any{"total": "42.00"},
	}

	fields := taskRequestInputFields(values, declared)
	if len(fields) != 3 {
		t.Fatalf("fields = %+v, want the declared input plus both undeclared values", fields)
	}
	if fields[0].Name != "detail" || !fields[0].Declared {
		t.Fatalf("fields[0] = %+v", fields[0])
	}
	// Alphabetical after the declared ones, so the order is stable between
	// loads of the same record.
	if fields[1].Name != "amounts" || fields[2].Name != "doc_id" {
		t.Fatalf("undeclared order = %q, %q", fields[1].Name, fields[2].Name)
	}
	if fields[1].Declared || fields[1].DataType != "text" {
		t.Fatalf("fields[1] = %+v, want undeclared text", fields[1])
	}
	if fields[1].Label != "Amounts" {
		t.Fatalf("label = %q, want the name humanised", fields[1].Label)
	}
}

func TestTaskRequestInputFieldsEmpty(t *testing.T) {
	if fields := taskRequestInputFields(nil, nil); fields != nil {
		t.Fatalf("fields = %+v, want nil", fields)
	}
}
