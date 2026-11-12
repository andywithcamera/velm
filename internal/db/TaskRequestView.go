package db

// The request a task carries (#262).
//
// base_task.variables holds {"inputs":…, "locals":…, "outputs":…}, written at
// spawn from the definition and the requester's form. Nothing read it back to
// the screen, so a doer opened "Write Doc Draft" with no idea what was asked
// for: the brief, the requester's words and every value that flowed in from a
// predecessor were all sitting in the row, invisible.
//
// This exposes it for the record page. It is deliberately read-only — inputs
// are the requester's words and are not editable by the doer.

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

// TaskRequestView is what the record page shows above the form.
type TaskRequestView struct {
	DefinitionName string
	DefinitionSlug string
	Inputs         []TaskRequestValue
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
		variablesRaw   string
		displayName    string
		slug           string
		inputSchemaRaw string
	)
	err := Pool.QueryRow(ctx, `
		SELECT COALESCE(t.variables::text, '{}'),
		       COALESCE(d.display_name, ''),
		       COALESCE(d.slug, ''),
		       COALESCE(d.input_schema_yaml, '')
		FROM base_task t
		LEFT JOIN base_task_definition d ON d._id = t.definition_id
		WHERE t._id::text = $1
	`, taskID).Scan(&variablesRaw, &displayName, &slug, &inputSchemaRaw)
	if err != nil {
		return view, fmt.Errorf("load task request: %w", err)
	}
	view.DefinitionName = strings.TrimSpace(displayName)
	view.DefinitionSlug = strings.TrimSpace(slug)

	var scope struct {
		Inputs  map[string]any `json:"inputs"`
		Outputs map[string]any `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(variablesRaw), &scope); err != nil {
		return view, nil // unreadable scope renders nothing rather than breaking the form
	}

	// The schema supplies labels and a stable order; a value with no schema
	// entry still shows, because a predecessor's output is worth reading even
	// when the definition never declared it.
	labels := map[string]string{}
	choiceLabels := map[string]map[string]string{}
	var declaredOrder []string
	if fields, err := ParseTaskInputSchema(inputSchemaRaw); err == nil {
		for _, field := range fields {
			labels[field.Name] = field.LabelOrName()
			declaredOrder = append(declaredOrder, field.Name)
			if len(field.Choices) > 0 {
				byValue := make(map[string]string, len(field.Choices))
				for _, choice := range field.Choices {
					byValue[choice.Value] = choice.Label
				}
				choiceLabels[field.Name] = byValue
			}
		}
	}

	view.Inputs = orderedScopeValues(scope.Inputs, declaredOrder, labels, choiceLabels)
	view.HasInputs = len(view.Inputs) > 0
	view.Outputs = orderedScopeValues(scope.Outputs, nil, nil, nil)
	view.HasOutputs = len(view.Outputs) > 0
	return view, nil
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
