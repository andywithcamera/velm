package main

import (
	"testing"

	"velm/internal/db"
)

func taskRequestViewForFields(fields ...db.TaskRequestInputField) db.TaskRequestView {
	return db.TaskRequestView{InputFields: fields, HasInputs: len(fields) > 0}
}

// Every synthetic column is read-only and nullable: the template draws the
// "From the request" pill instead of Required/Optional, and a frozen field is
// never submitted (#383).
func TestTaskRequestFormColumnsAreReadOnly(t *testing.T) {
	view := taskRequestViewForFields(
		db.TaskRequestInputField{Name: "summary", Label: "What is this request?", DataType: "text", Value: "Test", Declared: true},
		db.TaskRequestInputField{Name: "detail", Label: "Describe it", DataType: "long_text", Value: "test", Declared: true},
	)

	columns := taskRequestFormColumns(view, nil)
	if len(columns) != 2 {
		t.Fatalf("columns = %+v", columns)
	}
	for _, column := range columns {
		if !column.IS_READONLY {
			t.Fatalf("%s: want IS_READONLY", column.NAME)
		}
		if !column.IS_NULLABLE {
			t.Fatalf("%s: want IS_NULLABLE so no Required pill or attribute", column.NAME)
		}
	}
	if columns[0].LABEL != "What is this request?" || columns[0].DATA_TYPE != "text" {
		t.Fatalf("columns[0] = %+v", columns[0])
	}
	if columns[1].DATA_TYPE != "long_text" {
		t.Fatalf("detail type = %q, want long_text so it draws a textarea", columns[1].DATA_TYPE)
	}
}

// A definition may declare an input named "title". The record's own column
// wins: two fields with one name read as a bug.
func TestTaskRequestFormColumnsSkipColumnCollisions(t *testing.T) {
	existing := []db.Column{{NAME: "title", DATA_TYPE: "text"}, {NAME: "description", DATA_TYPE: "long_text"}}
	view := taskRequestViewForFields(
		db.TaskRequestInputField{Name: "title", DataType: "text", Value: "Test", Declared: true},
		db.TaskRequestInputField{Name: "Description", DataType: "text", Value: "test", Declared: true},
		db.TaskRequestInputField{Name: "summary", DataType: "text", Declared: true},
	)

	columns := taskRequestFormColumns(view, existing)
	if len(columns) != 1 || columns[0].NAME != "summary" {
		t.Fatalf("columns = %+v, want only summary", columns)
	}
}

func TestTaskRequestFormColumnsPickTheControl(t *testing.T) {
	cases := []struct {
		name  string
		field db.TaskRequestInputField
		want  string
	}{
		{"choice with options", db.TaskRequestInputField{DataType: "choice", Choices: []db.ChoiceOption{{Value: "high", Label: "High"}}}, "choice"},
		{"choice without options", db.TaskRequestInputField{DataType: "choice"}, "text"},
		{"boolean", db.TaskRequestInputField{DataType: "boolean", Value: "Yes"}, "bool"},
		{"number", db.TaskRequestInputField{DataType: "number", Value: "42"}, "number"},
		{"integer", db.TaskRequestInputField{DataType: "integer", Value: "7"}, "int"},
		{"date", db.TaskRequestInputField{DataType: "date", Value: "2026-11-26"}, "date"},
		{"datetime", db.TaskRequestInputField{DataType: "datetime", Value: "2026-11-26T10:00"}, "datetime"},
		{"email", db.TaskRequestInputField{DataType: "email", Value: "a@b.ie"}, "email"},
		{"richtext falls back to a textarea", db.TaskRequestInputField{DataType: "richtext", Value: "# Heading"}, "long_text"},
		{"reference is text", db.TaskRequestInputField{DataType: "reference:base_doc", Value: "DOC-000001"}, "text"},
		{"structured value is a json pane", db.TaskRequestInputField{DataType: "text", Value: "{\n  \"total\": \"42.00\"\n}"}, "json"},
		{"array value is a json pane", db.TaskRequestInputField{DataType: "text", Value: "[\n  1,\n  2\n]"}, "json"},
		{"plain text that merely starts with a brace", db.TaskRequestInputField{DataType: "text", Value: "{not valid"}, "text"},
	}
	for _, tc := range cases {
		tc.field.Name = "field"
		got := taskRequestColumnDataType(tc.field)
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTaskRequestFormColumnsEmpty(t *testing.T) {
	if columns := taskRequestFormColumns(db.TaskRequestView{}, nil); columns != nil {
		t.Fatalf("columns = %+v, want nil", columns)
	}
}
