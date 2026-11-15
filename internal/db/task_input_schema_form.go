package db

// The input-schema form gate (#358, Task Definition Editor slice 2).
//
// base_task_definition.input_schema_yaml is the requester's form and, until the
// editor, only hand-written YAML could change it. The designer edits rows, so it
// has to answer one question before it may show a row editor at all: can this
// stored document be read and written again without losing anything?
//
// The answer is deliberately conservative. ParseTaskInputSchema models seven
// keys; a document carrying an eighth is refused rather than silently reduced,
// because opening a form must never cost an author a key they did not know
// about. InspectTaskInputSchema is that gate, and it is the only copy of the
// rule: the handler asks it before rendering and before writing.

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// TaskInputFieldKeys is the set of keys TaskInputField models. The writer emits
// all seven for every item, even where the value is empty, false or []: an
// omitted key and its zero value mean the same thing to the parser, and emitting
// both makes the form's own output pass the gate that guards it.
var TaskInputFieldKeys = []string{
	"name", "label", "data_type", "is_nullable", "default_value", "help_text", "choices",
}

// TaskInputSchemaInspection is the gate's answer for one stored document.
// Lossless false carries the reason the row editor is not offered, in words an
// author can act on.
type TaskInputSchemaInspection struct {
	Fields   []TaskInputField
	Lossless bool
	Reason   string
}

// InspectTaskInputSchema decides whether the row editor may be offered.
func InspectTaskInputSchema(raw string) TaskInputSchemaInspection {
	fields, err := ParseTaskInputSchema(raw)
	if err != nil {
		return TaskInputSchemaInspection{Reason: "does not parse: " + stripSchemaParsePrefix(err.Error())}
	}
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "[]" {
		// A definition that takes no input is legal and has nothing to lose.
		return TaskInputSchemaInspection{Fields: fields, Lossless: true}
	}
	if keys := unmodelledInputSchemaKeys(raw); len(keys) > 0 {
		return TaskInputSchemaInspection{
			Reason: "contains keys this form does not model: " + strings.Join(keys, ", "),
		}
	}
	if !taskInputSchemaRoundTrips(raw, fields) {
		return TaskInputSchemaInspection{Reason: "contains values this form cannot represent"}
	}
	return TaskInputSchemaInspection{Fields: fields, Lossless: true}
}

// stripSchemaParsePrefix removes ParseTaskInputSchema's own wrapper so the
// reason reads "does not parse: ..." rather than repeating "parse input schema".
func stripSchemaParsePrefix(message string) string {
	return strings.TrimPrefix(message, "parse input schema: ")
}

// unmodelledInputSchemaKeys returns the sorted set of keys the struct does not
// model, including keys inside a choice pair. It runs after a successful parse,
// so the document's shape is known to be a list of mappings where the parser
// could read one.
func unmodelledInputSchemaKeys(raw string) []string {
	var document []map[string]any
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		return nil
	}
	modelled := make(map[string]bool, len(TaskInputFieldKeys))
	for _, key := range TaskInputFieldKeys {
		modelled[key] = true
	}
	found := map[string]bool{}
	for _, item := range document {
		for key, value := range item {
			if !modelled[key] {
				found[key] = true
			}
			if key != "choices" {
				continue
			}
			choices, ok := value.([]any)
			if !ok {
				continue
			}
			for _, choice := range choices {
				pair, ok := choice.(map[string]any)
				if !ok {
					continue
				}
				for pairKey := range pair {
					if pairKey != "value" && pairKey != "label" {
						found["choices."+pairKey] = true
					}
				}
			}
		}
	}
	keys := make([]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// taskInputSchemaRoundTrips re-marshals the parsed fields and compares them with
// the stored document semantically: an absent key equals its zero value, and
// surrounding whitespace is ignored because the parser trims it anyway (AC-27).
func taskInputSchemaRoundTrips(raw string, fields []TaskInputField) bool {
	reMarshalled, err := yaml.Marshal(fields)
	if err != nil {
		return false
	}
	var stored, written []map[string]any
	if err := yaml.Unmarshal([]byte(raw), &stored); err != nil {
		return false
	}
	if err := yaml.Unmarshal(reMarshalled, &written); err != nil {
		return false
	}
	return reflect.DeepEqual(normaliseInputSchemaDocument(stored), normaliseInputSchemaDocument(written))
}

// normaliseInputSchemaDocument fills every modelled key so absent and zero are
// indistinguishable, which is what makes the gate idempotent: the writer emits
// `choices: []` and `is_nullable: false` where an author may have omitted them.
func normaliseInputSchemaDocument(document []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(document))
	for _, item := range document {
		normalised := map[string]any{
			"name":          normaliseSchemaText(item["name"]),
			"label":         normaliseSchemaText(item["label"]),
			"data_type":     normalizeInputDataType(normaliseSchemaText(item["data_type"])),
			"is_nullable":   normaliseSchemaBool(item["is_nullable"]),
			"default_value": normaliseSchemaText(item["default_value"]),
			"help_text":     normaliseSchemaText(item["help_text"]),
			"choices":       normaliseSchemaChoices(item["choices"]),
		}
		out = append(out, normalised)
	}
	return out
}

func normaliseSchemaText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func normaliseSchemaBool(value any) bool {
	if value == nil {
		return false
	}
	if flag, ok := value.(bool); ok {
		return flag
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), "true")
}

func normaliseSchemaChoices(value any) []map[string]string {
	out := []map[string]string{}
	choices, ok := value.([]any)
	if !ok {
		return out
	}
	for _, choice := range choices {
		pair, ok := choice.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, map[string]string{
			"value": normaliseSchemaText(pair["value"]),
			"label": normaliseSchemaText(pair["label"]),
		})
	}
	return out
}

// MarshalTaskInputSchema writes fields as a block sequence, in order, emitting
// all seven keys per item. An empty field list writes an empty column, which the
// parser reads as "takes no input".
func MarshalTaskInputSchema(fields []TaskInputField) (string, error) {
	if len(fields) == 0 {
		return "", nil
	}
	out, err := yaml.Marshal(fields)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
