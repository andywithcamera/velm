package db

import (
	"strings"
	"time"
)

// Instance field seeding: values a spawned task should carry from its
// definition and from the request that started it.
//
// Evidence (live instance, 2026-11-11): of 45 definition-spawned tasks,
// 0 had a description, 0 had a due or planned start, and only 3 had an
// assignee. Description is the one the engine can fill with no product
// decisions: every definition already carries one, and a doer opening a
// blank form has no idea what "Run Tests" is meant to produce.

// defaultTaskPriority mirrors base_task.priority's default ('medium').
const defaultTaskPriority = "medium"

// taskPriorityChoices mirrors the base_task.priority choices in the base
// app definition. An input outside this set would violate
// chk_base_task_priority and fail the spawn outright, so unknown values
// fall back to the default instead of breaking the pipeline.
var taskPriorityChoices = map[string]bool{
	"very_low":  true,
	"low":       true,
	"medium":    true,
	"high":      true,
	"very_high": true,
}

// instanceDescription composes what the doer reads. The requester's own
// words come first because they are specific to this instance; the
// definition's description follows because it states the standing purpose.
// Neither replaces the other, and with neither we return "" rather than
// invent text.
func instanceDescription(def *TaskDefinition, inputs map[string]any) string {
	if def == nil {
		return ""
	}
	body := strings.TrimSpace(inputStringValue(inputs, "body"))
	purpose := strings.TrimSpace(def.Description)
	switch {
	case body != "" && purpose != "":
		return body + "\n\n" + purpose
	case body != "":
		return body
	default:
		return purpose
	}
}

// descriptionIsEngineAuthored reports whether the current description may
// be overwritten when a task's inputs are re-bound at promotion. A
// description a human has rewritten is left alone.
func descriptionIsEngineAuthored(current string, def *TaskDefinition) bool {
	trimmed := strings.TrimSpace(current)
	if trimmed == "" {
		return true
	}
	return def != nil && trimmed == strings.TrimSpace(def.Description)
}

// instancePriority resolves the priority column from request inputs.
func instancePriority(inputs map[string]any) string {
	p := strings.ToLower(strings.TrimSpace(inputStringValue(inputs, "priority")))
	if taskPriorityChoices[p] {
		return p
	}
	return defaultTaskPriority
}

// instancePlannedStart reads the scheduling input that definitions such as
// deployment_scheduling declare (window_start). Until #269 lands there is
// no duration field, so due_at is deliberately left alone: inventing a
// deadline would be worse than an empty one. An unparseable value is
// ignored rather than failing the spawn.
func instancePlannedStart(inputs map[string]any) any {
	raw := strings.TrimSpace(inputStringValue(inputs, "window_start"))
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts
		}
	}
	return nil
}

// nonNilInputs normalises a nil input map. Spawning from the catalog
// passed nil, which serialised as "inputs": null — a different type from
// the {} every script expects when it does input.variables.inputs ||
// {} (and what $inputs.x scope resolution walks).
func nonNilInputs(inputs map[string]any) map[string]any {
	if inputs == nil {
		return map[string]any{}
	}
	return inputs
}

func inputStringValue(inputs map[string]any, key string) string {
	if inputs == nil {
		return ""
	}
	value, ok := inputs[key]
	if !ok || value == nil {
		return ""
	}
	// Text inputs only: a number in "body" is a caller bug, not prose.
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
