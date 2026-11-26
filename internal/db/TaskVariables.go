package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// Variable scopes. A task's scope is a map handed to Goja, read by run_if,
// gate conditions and input mappings; the table splits it into named rows so
// a value can be written without rewriting its neighbours and so a filter can
// use an index.
const (
	VarScopeInput  = "input"
	VarScopeLocal  = "local"
	VarScopeOutput = "output"
	VarScopeBlock  = "block"
	// VarScopeTop holds every other top-level key. `parent` and `blocks` live
	// here as documents: one is the ancestor's scope snapshot, the other is
	// sibling block results. Neither is this task's own variable.
	VarScopeTop = "top"
)

// nestedVarScopes maps a top-level scope key in the in-memory map to its row
// scope. The engine's map keeps its original shape — {"inputs": {...},
// "block": {...}} — so every reader is unchanged; only storage moved.
var nestedVarScopes = map[string]string{
	"inputs":  VarScopeInput,
	"locals":  VarScopeLocal,
	"outputs": VarScopeOutput,
	"block":   VarScopeBlock,
}

// varScopeKeys is the inverse: the map key a row scope assembles into.
var varScopeKeys = map[string]string{
	VarScopeInput:  "inputs",
	VarScopeLocal:  "locals",
	VarScopeOutput: "outputs",
	VarScopeBlock:  "block",
}

type varRowKey struct {
	Scope string
	Name  string
}

// TaskVars is a task's variable scope, plus the rows it was loaded from.
// Save writes only the values that changed, so two writers touching
// different names never overwrite each other — the whole reason the scope is
// no longer one document.
type TaskVars struct {
	TaskID string
	Values map[string]any

	loaded map[varRowKey]string
}

// LoadTaskVars reads a task's scope as the map the engine expects.
func LoadTaskVars(ctx context.Context, q scriptQuerier, taskID string) (*TaskVars, error) {
	// The three scope names every task has are seeded as empty objects, not
	// left absent: a spawn used to store {"inputs": {}} and a definition
	// script reads input.variables.inputs.foo without a guard. An empty map
	// stores no rows, so this costs nothing on disk.
	return loadTaskVarsInto(ctx, q, taskID, map[string]any{
		"inputs":  map[string]any{},
		"locals":  map[string]any{},
		"outputs": map[string]any{},
	})
}

func loadTaskVarsInto(ctx context.Context, q scriptQuerier, taskID string, values map[string]any) (*TaskVars, error) {
	v := &TaskVars{TaskID: taskID, Values: values, loaded: map[varRowKey]string{}}
	rows, err := q.Query(ctx, `
		SELECT scope, name, COALESCE(value::text, 'null')
		FROM _task_variables
		WHERE task_id = $1`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var scope, name, raw string
		if err := rows.Scan(&scope, &name, &raw); err != nil {
			return nil, err
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("task variables: %s.%s is not valid JSON: %w", scope, name, err)
		}
		key := varRowKey{Scope: scope, Name: name}
		// Re-encode so the comparison in Save uses one encoder for both
		// sides: encoding/json sorts map keys, so equal values compare equal.
		canonical, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		v.loaded[key] = string(canonical)
		if mapKey, nested := varScopeKeys[scope]; nested {
			current, _ := v.Values[mapKey].(map[string]any)
			if current == nil {
				current = map[string]any{}
				v.Values[mapKey] = current
			}
			current[name] = value
			continue
		}
		v.Values[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return v, nil
}

// Save writes the names whose value changed since the scope was loaded, and
// deletes the rows of names that are gone. Unchanged names are not written,
// so a concurrent writer's fresh value survives.
func (v *TaskVars) Save(ctx context.Context, q scriptQuerier) error {
	current, err := flattenTaskVars(v.Values)
	if err != nil {
		return err
	}
	for key, value := range current {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("task variables: encode %s.%s: %w", key.Scope, key.Name, err)
		}
		if v.loaded[key] == string(encoded) {
			continue
		}
		if err := upsertTaskVariable(ctx, q, v.TaskID, key, value); err != nil {
			return err
		}
		v.loaded[key] = string(encoded)
	}
	for key := range v.loaded {
		if _, present := current[key]; present {
			continue
		}
		if _, err := q.Exec(ctx, `
			DELETE FROM _task_variables WHERE task_id = $1 AND scope = $2 AND name = $3`,
			v.TaskID, key.Scope, key.Name); err != nil {
			return err
		}
		delete(v.loaded, key)
	}
	return nil
}

// WriteTaskVariables stores a whole scope for a task that has none yet (a
// spawn). Everything is written; nothing is compared.
func WriteTaskVariables(ctx context.Context, q scriptQuerier, taskID string, values map[string]any) error {
	current, err := flattenTaskVars(values)
	if err != nil {
		return err
	}
	for key, value := range current {
		if err := upsertTaskVariable(ctx, q, taskID, key, value); err != nil {
			return err
		}
	}
	return nil
}

// UpdateTaskVariables loads a scope, lets fn mutate it, and saves only what
// fn changed. Every read-modify-write in the engine goes through here.
func UpdateTaskVariables(ctx context.Context, q scriptQuerier, taskID string, fn func(vars map[string]any) error) error {
	v, err := LoadTaskVars(ctx, q, taskID)
	if err != nil {
		return err
	}
	if err := fn(v.Values); err != nil {
		return err
	}
	return v.Save(ctx, q)
}

// SetTaskVariableScope replaces every name in one scope, leaving the others
// alone. Used where the engine knows what a scope should now hold rather than
// what changed inside it.
func SetTaskVariableScope(ctx context.Context, q scriptQuerier, taskID, scope string, values map[string]any) error {
	if _, ok := varScopeKeys[scope]; !ok {
		return fmt.Errorf("task variables: %q is not a nested scope", scope)
	}
	if _, err := q.Exec(ctx, `
		DELETE FROM _task_variables WHERE task_id = $1 AND scope = $2`, taskID, scope); err != nil {
		return err
	}
	for name, value := range values {
		if err := upsertTaskVariable(ctx, q, taskID, varRowKey{Scope: scope, Name: name}, value); err != nil {
			return err
		}
	}
	return nil
}

// loadTaskVarsFor reads several tasks' scopes in one query, for the paths
// that work over a run's children at once.
func loadTaskVarsFor(ctx context.Context, q scriptQuerier, taskIDs []string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT task_id::text, scope, name, COALESCE(value::text, 'null')
		FROM _task_variables
		WHERE task_id = ANY($1::uuid[])`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, scope, name, raw string
		if err := rows.Scan(&taskID, &scope, &name, &raw); err != nil {
			return nil, err
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		values := out[taskID]
		if values == nil {
			values = map[string]any{}
			out[taskID] = values
		}
		if mapKey, nested := varScopeKeys[scope]; nested {
			current, _ := values[mapKey].(map[string]any)
			if current == nil {
				current = map[string]any{}
				values[mapKey] = current
			}
			current[name] = value
			continue
		}
		values[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A task with no rows still has an empty scope, not a missing one, and the
	// three scope names a task always has are objects rather than absent.
	for _, id := range taskIDs {
		values, present := out[id]
		if !present {
			values = map[string]any{}
			out[id] = values
		}
		for _, scope := range []string{VarScopeInput, VarScopeLocal, VarScopeOutput} {
			if _, ok := values[varScopeKeys[scope]]; !ok {
				values[varScopeKeys[scope]] = map[string]any{}
			}
		}
	}
	return out, nil
}

// flattenTaskVars turns the engine's scope map into rows. An object-valued
// nested scope becomes one row per key; anything else becomes a `top` row
// under its own name, including a nested scope that is not an object.
func flattenTaskVars(values map[string]any) (map[varRowKey]any, error) {
	out := map[varRowKey]any{}
	for name, value := range values {
		if scope, nested := nestedVarScopes[name]; nested {
			if inner, ok := value.(map[string]any); ok {
				for innerName, innerValue := range inner {
					out[varRowKey{Scope: scope, Name: innerName}] = innerValue
				}
				continue
			}
		}
		out[varRowKey{Scope: VarScopeTop, Name: name}] = value
	}
	return out, nil
}

func upsertTaskVariable(ctx context.Context, q scriptQuerier, taskID string, key varRowKey, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("task variables: encode %s.%s: %w", key.Scope, key.Name, err)
	}
	text, number, boolean := variableProjections(value)
	if _, err := q.Exec(ctx, `
		INSERT INTO _task_variables (task_id, scope, name, value, value_text, value_number, value_bool)
		VALUES ($1, $2, $3, $4::jsonb, $5, NULLIF($6, '')::numeric, $7)
		ON CONFLICT (task_id, scope, name) DO UPDATE
		SET value = EXCLUDED.value,
			value_text = EXCLUDED.value_text,
			value_number = EXCLUDED.value_number,
			value_bool = EXCLUDED.value_bool,
			_updated_at = NOW()`,
		taskID, key.Scope, key.Name, string(encoded), text, number, boolean); err != nil {
		return fmt.Errorf("task variables: write %s.%s: %w", key.Scope, key.Name, err)
	}
	return nil
}

// variableProjections picks the typed columns a scalar belongs in. A
// document projects to nothing: it is readable, not filterable, and that is
// the honest boundary until a definition declares the shape.
func variableProjections(value any) (text *string, number string, boolean *bool) {
	switch typed := value.(type) {
	case string:
		trimmed := typed
		return &trimmed, "", nil
	case bool:
		flag := typed
		return nil, "", &flag
	case float64:
		return nil, strconv.FormatFloat(typed, 'f', -1, 64), nil
	case json.Number:
		if _, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			return nil, typed.String(), nil
		}
		return nil, "", nil
	case nil:
		return nil, "", nil
	}
	return nil, "", nil
}

// TaskVariableRow is one stored name, for a surface that lists them rather
// than evaluating them.
type TaskVariableRow struct {
	Scope string
	Name  string
	Value any
}

// TaskVariablesInScope lists one scope's names in the order they were
// declared, when a caller has an order to apply.
func TaskVariablesInScope(ctx context.Context, q scriptQuerier, taskID, scope string) (map[string]any, error) {
	vars, err := LoadTaskVars(ctx, q, taskID)
	if err != nil {
		return nil, err
	}
	mapKey, nested := varScopeKeys[scope]
	if !nested {
		return vars.Values, nil
	}
	inner, _ := vars.Values[mapKey].(map[string]any)
	if inner == nil {
		return map[string]any{}, nil
	}
	return inner, nil
}
