package db

// The request a task carries (#262).
//
// base_task.variables holds {"inputs":…, "locals":…, "outputs":…}, written at
// spawn from the definition and the requester's form. Nothing read it back to
// the screen, so a doer opened "Write Doc Draft" with no idea what was asked
// for: the brief, the requester's words and every value that flowed in from a
// predecessor were all sitting in the row, invisible.
//
// Inputs reach the record page as read-only form fields (#383), not as a card
// of prose above the form: the brief belongs where the doer already looks. They
// are deliberately read-only — inputs are the requester's words and are not
// editable by the doer. Values that flowed in without a schema entry still
// show, because a predecessor's output is worth reading even when the
// definition never declared it.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TaskTableName is the table that holds task instances. Named once because the
// request view and the comment thread both key off it.
const TaskTableName = "base_task"

// TaskRequestValue is one named value in a task's scope.
type TaskRequestValue struct {
	Name  string
	Label string
	Value string
}

// TaskRequestInputField is one input as the record page renders it: still
// typed, so the form can draw a select for a choice and a textarea for long
// text instead of flattening every input into a bullet of prose.
type TaskRequestInputField struct {
	Name       string
	Label      string
	DataType   string
	Value      string
	IsNullable bool
	HelpText   string
	Choices    []ChoiceOption
	// Declared is false for a value that arrived in the task's scope with no
	// schema entry. Those still render, as read-only text.
	Declared bool
}

// TaskRequestView is what a task record carries about the request that made it.
type TaskRequestView struct {
	DefinitionName string
	DefinitionSlug string
	InputFields    []TaskRequestInputField
	Outputs        []TaskRequestValue
	HasInputs      bool
	HasOutputs     bool
}

// LoadTaskRequest reads a task's inputs and outputs. A task with no
// definition, no variables, or nothing worth showing returns a zero view and a
// nil error: absence is normal (a hand-made task), not a failure.
func LoadTaskRequest(ctx context.Context, taskID string) (TaskRequestView, error) {
	var view TaskRequestView
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return view, nil
	}

	var (
		displayName    string
		slug           string
		inputSchemaRaw string
	)
	err := Pool.QueryRow(ctx, `
		SELECT COALESCE(d.display_name, ''),
		       COALESCE(d.slug, ''),
		       COALESCE(d.input_schema_yaml, '')
		FROM base_task t
		LEFT JOIN base_task_definition d ON d._id = t.definition_id
		WHERE t._id::text = $1
	`, taskID).Scan(&displayName, &slug, &inputSchemaRaw)
	if err != nil {
		return view, fmt.Errorf("load task request: %w", err)
	}
	view.DefinitionName = strings.TrimSpace(displayName)
	view.DefinitionSlug = strings.TrimSpace(slug)

	scope, err := LoadTaskVars(ctx, Pool, taskID)
	if err != nil {
		return view, nil // unreadable scope renders nothing rather than breaking the form
	}
	inputs, _ := scope.Values["inputs"].(map[string]any)
	outputs, _ := scope.Values["outputs"].(map[string]any)

	// The schema supplies labels, types and a stable order.
	var declaredFields []TaskInputField
	if fields, err := ParseTaskInputSchema(inputSchemaRaw); err == nil {
		declaredFields = fields
	}

	view.InputFields = taskRequestInputFields(inputs, declaredFields)
	view.HasInputs = len(view.InputFields) > 0
	view.Outputs = orderedScopeValues(outputs, nil, nil, nil)
	view.HasOutputs = len(view.Outputs) > 0
	return view, nil
}

// taskRequestInputFields keeps a choice's stored value rather than its label:
// the form renders the label from the option list, so substituting it here
// would leave the select with nothing selected.
func taskRequestInputFields(values map[string]any, declared []TaskInputField) []TaskRequestInputField {
	if len(values) == 0 && len(declared) == 0 {
		return nil
	}
	out := make([]TaskRequestInputField, 0, len(declared)+len(values))
	seen := make(map[string]bool, len(declared))
	for _, field := range declared {
		name := strings.TrimSpace(field.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, TaskRequestInputField{
			Name:       name,
			Label:      field.LabelOrName(),
			DataType:   strings.TrimSpace(field.DataType),
			Value:      renderScopeValue(values[name]),
			IsNullable: field.IsNullable,
			HelpText:   strings.TrimSpace(field.HelpText),
			Choices:    field.Choices,
			Declared:   true,
		})
	}
	rest := make([]string, 0, len(values))
	for name := range values {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		if strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, TaskRequestInputField{
			Name:       name,
			Label:      humanizeInputName(name),
			DataType:   "text",
			Value:      renderScopeValue(values[name]),
			IsNullable: true,
		})
	}
	return out
}

func orderedScopeValues(values map[string]any, declaredOrder []string, labels map[string]string, choiceLabels map[string]map[string]string) []TaskRequestValue {
	if len(values) == 0 {
		return nil
	}
	out := make([]TaskRequestValue, 0, len(values))
	seen := map[string]bool{}
	appendValue := func(name string) {
		value, ok := values[name]
		if !ok || value == nil || seen[name] {
			return
		}
		seen[name] = true
		// A choice stores its value ("high"); show the label ("High") so the
		// request reads as the requester saw it on the form.
		if chosen, ok := value.(string); ok {
			if mapped, ok := choiceLabels[name][chosen]; ok && strings.TrimSpace(mapped) != "" {
				value = mapped
			}
		}
		rendered := renderScopeValue(value)
		if strings.TrimSpace(rendered) == "" {
			return
		}
		label := strings.TrimSpace(labels[name])
		if label == "" {
			label = humanizeInputName(name)
		}
		out = append(out, TaskRequestValue{Name: name, Label: label, Value: rendered})
	}
	for _, name := range declaredOrder {
		appendValue(name)
	}
	rest := make([]string, 0, len(values))
	for name := range values {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		appendValue(name)
	}
	return out
}

// renderScopeValue keeps a scalar readable and a structured value legible
// rather than printing Go's map formatting.
func renderScopeValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case bool:
		if typed {
			return "Yes"
		}
		return "No"
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%v", typed)
	case map[string]any, []any:
		encoded, err := json.MarshalIndent(typed, "", "  ")
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(encoded)
	default:
		return fmt.Sprintf("%v", typed)
	}
}
