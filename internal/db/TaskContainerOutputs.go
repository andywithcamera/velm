package db

import (
	"context"
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"
)

// A container's declared outputs are the hand-off between stages: the design
// doc's "Check Uptime (uptime -> bubbles up as Reboot Server's output)".
// Children publish their outputs into the container's scope on close, but
// nothing lifted them into the container's own OUTPUT declarations, so
// $outputs.<stage>.<name> from a sibling stage resolved to nothing and every
// downstream input arrived blank.
//
// bubbleContainerOutputs walks a closing container's descendants and fills
// its declared output names from theirs. Named outputs win over the
// namespaced per-child map: the declaration is the contract, the namespace is
// the detail.
//
// Latest instance wins. A revsion loop re-spawns a producer, so the second
// attempt's artefact must replace the first, not lose to it — hence ordering
// by _created_at rather than taking the first hit.
const maxBubbleDepth = 4

type variableDeclarations struct {
	Inputs  []string `yaml:"inputs"`
	Locals  []string `yaml:"locals"`
	Outputs []string `yaml:"outputs"`
}

func parseDeclaredOutputs(variablesYAML string) []string {
	decl := variableDeclarations{}
	if err := yaml.Unmarshal([]byte(variablesYAML), &decl); err != nil {
		return nil
	}
	names := make([]string, 0, len(decl.Outputs))
	for _, name := range decl.Outputs {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// mergeContainerOutputs returns the container's outputs with descendant
// values folded in and written back to the task row.
func mergeContainerOutputs(ctx context.Context, q scriptQuerier, taskID string, vars map[string]any, variablesYAML string) (map[string]any, error) {
	outputs, _ := vars["outputs"].(map[string]any)
	if outputs == nil {
		outputs = map[string]any{}
	}

	type descendant struct {
		id   string
		vars map[string]any
	}
	rows, err := q.Query(ctx, `
		WITH RECURSIVE tree AS (
			SELECT _id, 1 AS depth
			FROM base_task
			WHERE parent_task_id = $1 AND _deleted_at IS NULL
			UNION ALL
			SELECT c._id, t.depth + 1
			FROM base_task c
			JOIN tree t ON c.parent_task_id = t._id
			WHERE c._deleted_at IS NULL AND t.depth < $2
		)
		SELECT b._id::text, b.variables
		FROM base_task b
		JOIN tree ON tree._id = b._id
		ORDER BY b._created_at`, taskID, maxBubbleDepth)
	if err != nil {
		return outputs, err
	}
	defer rows.Close()
	var descendants []descendant
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return outputs, err
		}
		childVars := map[string]any{}
		_ = json.Unmarshal(raw, &childVars)
		descendants = append(descendants, descendant{id: id, vars: childVars})
	}
	if err := rows.Err(); err != nil {
		return outputs, err
	}

	declared := parseDeclaredOutputs(variablesYAML)
	wanted := func(name string) bool {
		if len(declared) == 0 {
			return true // nothing declared: take whatever the children produced
		}
		for _, candidate := range declared {
			if candidate == name {
				return true
			}
		}
		return false
	}

	// A value the container set itself is an explicit statement and stands.
	own := make(map[string]bool, len(outputs))
	for name := range outputs {
		own[name] = true
	}

	// Descendants are ordered oldest first, so overwriting leaves the most
	// recent instance's answer in place — the re-drafted requirement, not the
	// rejected first attempt.
	for _, d := range descendants {
		childOutputs, _ := d.vars["outputs"].(map[string]any)
		for name, value := range childOutputs {
			if !wanted(name) || own[name] {
				continue
			}
			outputs[name] = value
		}
	}

	vars["outputs"] = outputs
	updated, err := json.Marshal(vars)
	if err != nil {
		return outputs, err
	}
	if _, err := q.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, taskID, string(updated)); err != nil {
		return outputs, err
	}
	return outputs, nil
}

// containerVariablesYAML reports whether a task is a container and returns
// its variable declarations.
func containerVariablesYAML(ctx context.Context, q scriptQuerier, taskID string) (string, bool, error) {
	var variablesYAML string
	var isContainer bool
	err := q.QueryRow(ctx, `
		SELECT COALESCE(d.variables_yaml, ''), d.is_container
		FROM base_task t
		JOIN base_task_definition d ON d._id = t.definition_id
		WHERE t._id = $1`, taskID).Scan(&variablesYAML, &isContainer)
	if err != nil {
		return "", false, nil
	}
	return variablesYAML, isContainer, nil
}
