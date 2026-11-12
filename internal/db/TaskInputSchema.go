package db

// Task input schema (#204): the requester's form.
//
// base_task_definition.input_schema_yaml has been in the schema since 076 and
// had no reader until now, so every catalog order spawned with an empty input
// set. The shape mirrors the column list an app definition uses (042-style),
// because a task builder already writes those and one field vocabulary is
// better than two:
//
//   - name: body
//     label: What do you need?
//     data_type: long_text
//     is_nullable: false
//   - name: priority
//     label: Priority
//     data_type: choice
//     choices:
//       - value: low
//         label: Low
//
// The engine already understands three input keys: body (composed into the
// instance description), priority, and window_start (planned start). Those
// names keep working because this parser passes strings through untouched.

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// TaskInputField is one declared input on a task definition.
type TaskInputField struct {
	Name         string         `yaml:"name" json:"name"`
	Label        string         `yaml:"label" json:"label"`
	DataType     string         `yaml:"data_type" json:"data_type"`
	IsNullable   bool           `yaml:"is_nullable" json:"is_nullable"`
	DefaultValue string         `yaml:"default_value" json:"default_value"`
	HelpText     string         `yaml:"help_text" json:"help_text"`
	Choices      []ChoiceOption `yaml:"choices" json:"choices"`
}

// LabelOrName is what the form prints when no label is declared.
func (f TaskInputField) LabelOrName() string {
	if label := strings.TrimSpace(f.Label); label != "" {
		return label
	}
	return humanizeInputName(f.Name)
}

// InputControlKind collapses the app data_type vocabulary onto the three HTML
// controls the catalog can render without client scripting.
func (f TaskInputField) InputControlKind() string {
	switch normalizeInputDataType(f.DataType) {
	case "long_text", "richtext":
		return "textarea"
	case "choice":
		return "select"
	case "boolean":
		return "checkbox"
	default:
		return "input"
	}
}

// InputHTMLType maps the data_type to the input's type attribute.
func (f TaskInputField) InputHTMLType() string {
	switch normalizeInputDataType(f.DataType) {
	case "number", "integer", "decimal":
		return "number"
	case "date":
		return "date"
	case "datetime":
		return "datetime-local"
	case "email":
		return "email"
	case "url":
		return "url"
	default:
		return "text"
	}
}

// IsRequired reports whether the requester must fill the field. A default
// value satisfies the requirement, so the field is optional to type.
//
// is_nullable omitted means required, matching the column convention in app
// definitions (an app column is NOT NULL unless it says otherwise), so a
// schema author gets one consistent rule rather than two.
func (f TaskInputField) IsRequired() bool {
	return !f.IsNullable && strings.TrimSpace(f.DefaultValue) == ""
}

func normalizeInputDataType(dataType string) string {
	return normalizeDataType(strings.ToLower(strings.TrimSpace(dataType)))
}

func humanizeInputName(name string) string {
	parts := strings.Split(strings.TrimSpace(name), "_")
	for index, part := range parts {
		if part == "" {
			continue
		}
		parts[index] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

// ParseTaskInputSchema reads input_schema_yaml. An empty or comment-only
// document is a definition that takes no input, which is legal and common.
func ParseTaskInputSchema(raw string) ([]TaskInputField, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}
	var fields []TaskInputField
	if err := yaml.Unmarshal([]byte(trimmed), &fields); err != nil {
		return nil, fmt.Errorf("parse input schema: %w", err)
	}
	seen := make(map[string]bool, len(fields))
	for index := range fields {
		field := &fields[index]
		field.Name = strings.TrimSpace(field.Name)
		field.Label = strings.TrimSpace(field.Label)
		field.DataType = strings.TrimSpace(field.DataType)
		field.DefaultValue = strings.TrimSpace(field.DefaultValue)
		if field.Name == "" {
			return nil, fmt.Errorf("input schema field %d has no name", index+1)
		}
		if !IsSafeIdentifier(field.Name) {
			return nil, fmt.Errorf("input schema field %q is not a valid name (letters, digits, underscore)", field.Name)
		}
		if strings.HasPrefix(field.Name, "_") {
			return nil, fmt.Errorf("input schema field %q may not start with an underscore", field.Name)
		}
		if seen[field.Name] {
			return nil, fmt.Errorf("input schema declares %q twice", field.Name)
		}
		seen[field.Name] = true
		field.DataType = normalizeInputDataType(field.DataType)
	}
	return fields, nil
}

// TaskInputValues resolves the requester's submission against the declared
// schema. Missing required fields and values outside a choice list are
// reported together, so the requester fixes one form rather than one error
// per attempt.
func TaskInputValues(fields []TaskInputField, submitted map[string]string) (map[string]any, error) {
	values := make(map[string]any, len(fields))
	var problems []string
	for _, field := range fields {
		raw := strings.TrimSpace(submitted[field.Name])
		if raw == "" {
			raw = field.DefaultValue
		}
		if raw == "" {
			// An unticked checkbox submits nothing, which is a "no", not a
			// missing answer: treating it as missing would make a required
			// boolean impossible to answer false.
			if field.InputControlKind() == "checkbox" {
				values[field.Name] = false
				continue
			}
			if field.IsRequired() {
				problems = append(problems, fmt.Sprintf("%s is required", field.LabelOrName()))
			}
			continue
		}
		value, err := coerceTaskInputValue(field, raw)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		values[field.Name] = value
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return values, nil
}

func coerceTaskInputValue(field TaskInputField, raw string) (any, error) {
	switch field.InputControlKind() {
	case "select":
		if len(field.Choices) == 0 {
			return raw, nil
		}
		for _, choice := range field.Choices {
			if choice.Value == raw {
				return raw, nil
			}
		}
		return nil, fmt.Errorf("%s must be one of the listed options", field.LabelOrName())
	case "checkbox":
		parsed, err := parseTaskInputBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%s must be yes or no", field.LabelOrName())
		}
		return parsed, nil
	}
	switch normalizeInputDataType(field.DataType) {
	case "number", "integer", "decimal":
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a number", field.LabelOrName())
		}
		if normalizeInputDataType(field.DataType) == "integer" && parsed != float64(int64(parsed)) {
			return nil, fmt.Errorf("%s must be a whole number", field.LabelOrName())
		}
		return parsed, nil
	}
	return raw, nil
}

func parseTaskInputBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("not a boolean")
}
