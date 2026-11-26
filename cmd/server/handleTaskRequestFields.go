package main

// Task inputs on the record page (#383).
//
// A task's inputs used to reach the doer as a card of prose above the form.
// They are the brief, so they belong in the form: declared inputs render as
// read-only fields with the control each type deserves — a select for a
// choice, a textarea for long text — instead of a flattened bullet list.
//
// These columns are synthetic. They exist for one render and are never written:
// HandleSave only admits real metadata columns, and the template disables them
// so nothing is submitted at all.

import (
	"context"
	"encoding/json"
	"strings"

	"velm/internal/db"
)

// injectTaskRequestFormFields puts the request's inputs at the top of the form
// and returns the widened set. It runs after the security pass, like the
// synthetic password field: inputs are not columns, so field rules neither see
// nor strip them.
func injectTaskRequestFormFields(ctx context.Context, request db.TaskRequestView, columns []db.Column, configs map[string]formFieldConfig, values map[string]string) ([]db.Column, map[string]formFieldConfig, map[string]string) {
	synthetic := taskRequestFormColumns(request, columns)
	if len(synthetic) == 0 {
		return columns, configs, values
	}
	built := buildFormFieldConfigs(ctx, db.TaskTableName, synthetic, nil)
	helpText := make(map[string]string, len(request.InputFields))
	fieldValues := make(map[string]string, len(request.InputFields))
	for _, field := range request.InputFields {
		name := strings.TrimSpace(field.Name)
		helpText[name] = field.HelpText
		fieldValues[name] = field.Value
	}
	for _, column := range synthetic {
		cfg := built[column.NAME]
		cfg.ReadOnly = true
		cfg.Synthetic = true
		cfg.HelpText = helpText[column.NAME]
		configs[column.NAME] = cfg
		values[column.NAME] = fieldValues[column.NAME]
	}
	return append(synthetic, columns...), configs, values
}

// taskRequestFormColumns turns a task's inputs into read-only form columns.
// Values with no schema entry become plain text, in alphabetical order after
// the declared ones, so a predecessor's output is still readable.
func taskRequestFormColumns(request db.TaskRequestView, existing []db.Column) []db.Column {
	if len(request.InputFields) == 0 {
		return nil
	}
	taken := make(map[string]bool, len(existing))
	for _, column := range existing {
		taken[strings.ToLower(strings.TrimSpace(column.NAME))] = true
	}
	columns := make([]db.Column, 0, len(request.InputFields))
	for _, field := range request.InputFields {
		name := strings.TrimSpace(field.Name)
		// A definition may declare an input named "title". The record's own
		// column wins: two fields with one name read as a bug, and the column
		// is the one the doer can actually act on.
		if name == "" || taken[strings.ToLower(name)] {
			continue
		}
		label := strings.TrimSpace(field.Label)
		if label == "" {
			label = name
		}
		columns = append(columns, db.Column{
			NAME:        name,
			LABEL:       label,
			DATA_TYPE:   taskRequestColumnDataType(field),
			IS_NULLABLE: true,
			IS_READONLY: true,
			CHOICES:     field.Choices,
		})
	}
	return columns
}

// taskRequestColumnDataType picks the control. It is deliberately conservative:
// anything the form has no read-only rendering for falls back to text rather
// than guessing, because a mis-rendered brief is worse than a plain one.
func taskRequestColumnDataType(field db.TaskRequestInputField) string {
	if isStructuredScopeValue(field.Value) {
		return "json"
	}
	switch db.CanonicalDataType(field.DataType) {
	case "long_text", "richtext":
		// richtext is stored as markdown; the textarea shows the source, which
		// is what a read-only brief is worth reading as.
		return "long_text"
	case "choice":
		if len(field.Choices) == 0 {
			return "text"
		}
		return "choice"
	case "boolean":
		return "bool"
	case "number":
		return "number"
	case "integer":
		return "int"
	case "decimal":
		return "decimal"
	case "date":
		return "date"
	case "datetime":
		return "datetime"
	case "email":
		return "email"
	case "url":
		return "url"
	default:
		// text, reference:*, code:* and anything unrecognised.
		return "text"
	}
}

// isStructuredScopeValue reports whether a value is a JSON object or array,
// which renderScopeValue prints indented. Those belong in a monospace pane.
func isStructuredScopeValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return false
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return false
	}
	switch decoded.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}
