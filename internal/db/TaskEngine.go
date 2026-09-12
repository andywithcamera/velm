package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Task engine (issue #112, design: Task Composition Model.md).
//
// Uniform invariant: work happens only after a task enters in_progress.
// Container IS a task: all children spawn at parent spawn in the ready
// state. Per-child run_if conditions gate against the parent's variable
// scope. Script tasks run under the assigned Automation principal — that
// user is the audit principal for every CRUD write the script performs
// via ctx.records (captured by _audit_data_change, 009).

// Real state codes per 047: new/triage/ready/in_progress/blocked/done/
// cancelled (+ skipped from 077). The engine's "waiting" state is ready —
// children of a container spawn into ready; run_if gates ready ->
// in_progress, false -> skipped (077 transitions).
const (
	taskStateReady      = "ready"
	taskStateInProgress = "in_progress"
	taskStateSkipped    = "skipped"
	taskStateDone       = "done"
)

type TaskDefinition struct {
	ID             string
	Slug           string
	DisplayName    string
	IsContainer    bool
	ScriptText     string
	RunIfScript    string
	DefaultGroupID string
	SpawnRules     []TaskSpawnRule
	IsActive       bool
}

type TaskSpawnRule struct {
	On             string         `json:"on"`
	DefinitionSlug string         `json:"definition_slug"`
	InputMapping   map[string]any `json:"input_mapping"`
	Assign         string         `json:"assign"` // "same" | "pool"
}

// getTaskDefinition loads a definition by id. Uses scriptQuerier so it
// works with Pool or an open transaction.
func getTaskDefinition(ctx context.Context, q scriptQuerier, id string) (*TaskDefinition, error) {
	var d TaskDefinition
	var groupID *string
	var spawnRulesJSON []byte
	err := q.QueryRow(ctx, `
		SELECT _id::text, slug, display_name, is_container, script_text, run_if_script,
			default_group_id::text, spawn_rules, is_active
		FROM _task_definition WHERE _id = $1 AND _deleted_at IS NULL`, id).Scan(
		&d.ID, &d.Slug, &d.DisplayName, &d.IsContainer, &d.ScriptText, &d.RunIfScript,
		&groupID, &spawnRulesJSON, &d.IsActive)
	if err != nil {
		return nil, fmt.Errorf("load task definition: %w", err)
	}
	if groupID != nil {
		d.DefaultGroupID = *groupID
	}
	d.SpawnRules = parseSpawnRules(spawnRulesJSON)
	return &d, nil
}

func parseSpawnRules(raw []byte) []TaskSpawnRule {
	rules := []TaskSpawnRule{}
	if len(raw) == 0 {
		return rules
	}
	_ = json.Unmarshal(raw, &rules)
	return rules
}

type childSpec struct {
	ChildDefinitionID string
	InputMapping      map[string]any
}

func getTaskDefinitionChildren(ctx context.Context, q scriptQuerier, containerID string) ([]childSpec, error) {
	rows, err := q.Query(ctx, `
		SELECT child_definition_id::text, input_mapping
		FROM _task_definition_child
		WHERE container_definition_id = $1
		ORDER BY sort_order, _created_at`, containerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var specs []childSpec
	for rows.Next() {
		var spec childSpec
		if err := rows.Scan(&spec.ChildDefinitionID, &spec.InputMapping); err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, rows.Err()
}

// SpawnTaskInstance creates a task instance from a definition. Children of
// a container spawn immediately, in ready, under parent_task_id. Inputs
// bind at spawn for root tasks; for container children they bind when the
// parent starts (StartTask applies input_mapping against the parent scope).
func SpawnTaskInstance(ctx context.Context, q scriptQuerier, definitionID, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
	def, err := getTaskDefinition(ctx, q, definitionID)
	if err != nil {
		return "", err
	}
	return spawnTaskForDefinition(ctx, q, def, parentTaskID, requestedBy, inputs)
}

func spawnTaskForDefinition(ctx context.Context, q scriptQuerier, def *TaskDefinition, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
	if !def.IsActive {
		return "", fmt.Errorf("task definition %s is not active", def.Slug)
	}

	vars := map[string]any{"inputs": inputs, "locals": map[string]any{}, "outputs": map[string]any{}}
	varsJSON, err := json.Marshal(vars)
	if err != nil {
		return "", err
	}
	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}

	var taskID string
	err = q.QueryRow(ctx, `
		INSERT INTO base_task (number, title, work_type, state_id, priority, parent_task_id,
			assignment_group_id, requested_by_user_id, definition_id, variables)
		VALUES (
			'T-' || lpad(nextval('base_task_number_seq')::text, 6, '0'),
			$1, 'TASK',
			(SELECT _id FROM base_task_state WHERE code = $2 AND _deleted_at IS NULL),
			'p3', $3, $4, $5, $6, $7::jsonb)
		RETURNING _id`,
		def.DisplayName, taskStateReady, nullable(parentTaskID), nullable(def.DefaultGroupID),
		nullable(requestedBy), def.ID, string(varsJSON)).Scan(&taskID)
	if err != nil {
		return "", fmt.Errorf("spawn task: %w", err)
	}

	if def.IsContainer {
		children, err := getTaskDefinitionChildren(ctx, q, def.ID)
		if err != nil {
			return "", err
		}
		for _, child := range children {
			// Children spawn with empty inputs; StartTask binds them from
			// the parent scope via input_mapping.
			if _, err := spawnTaskForDefinitionByID(ctx, q, child.ChildDefinitionID, taskID, requestedBy, map[string]any{}); err != nil {
				return "", err
			}
		}
	}
	return taskID, nil
}

func spawnTaskForDefinitionByID(ctx context.Context, q scriptQuerier, defID, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
	def, err := getTaskDefinition(ctx, q, defID)
	if err != nil {
		return "", err
	}
	return spawnTaskForDefinition(ctx, q, def, parentTaskID, requestedBy, inputs)
}

// StartTask advances a ready task to in_progress and runs engine hooks:
// run_if evaluation (false -> skipped), then the definition script. The
// Automation principal is the audit principal for script writes.
func StartTask(ctx context.Context, taskID, actorUserID string) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := transitionTaskInTx(ctx, tx, taskID, taskStateInProgress); err != nil {
		return err
	}
	if err := runTaskHooks(ctx, tx, taskID, actorUserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// transitionTaskInTx performs one validated state transition in the
// caller's transaction.
func transitionTaskInTx(ctx context.Context, tx scriptQuerier, taskID, toStateCode string) error {
	var fromState string
	err := tx.QueryRow(ctx, `
		SELECT s.code FROM base_task t
		JOIN base_task_state s ON s._id = t.state_id
		WHERE t._id = $1 AND t._deleted_at IS NULL
		FOR UPDATE`, taskID).Scan(&fromState)
	if err != nil {
		return fmt.Errorf("load task: %w", err)
	}

	var allowed bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM base_task_transition tr
			JOIN base_task_state f ON f._id = tr.from_state_id
			JOIN base_task_state t2 ON t2._id = tr.to_state_id
			WHERE f.code = $1 AND t2.code = $2)`, fromState, toStateCode).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("invalid task transition %s -> %s", fromState, toStateCode)
	}

	_, err = tx.Exec(ctx, `
		UPDATE base_task
		SET state_id = (SELECT _id FROM base_task_state WHERE code = $2 AND _deleted_at IS NULL)
		WHERE _id = $1`, taskID, toStateCode)
	return err
}

// runTaskHooks: on entering in_progress — evaluate run_if (false -> skip),
// then run the definition script for script tasks.
func runTaskHooks(ctx context.Context, tx pgx.Tx, taskID, actorUserID string) error {
	defID, err := getDefinitionIDByTaskID(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if defID == "" {
		return nil // hand-made task: no engine hooks
	}
	def, err := getTaskDefinition(ctx, tx, defID)
	if err != nil {
		return err
	}
	vars, err := readTaskVariables(ctx, tx, taskID)
	if err != nil {
		return err
	}

	if strings.TrimSpace(def.RunIfScript) != "" {
		allowed, evalErr := evalTaskCondition(ctx, tx, taskID, def.RunIfScript, vars, actorUserID)
		if evalErr != nil {
			return fmt.Errorf("run_if evaluation failed for task %s: %w", taskID, evalErr)
		}
		if !allowed {
			return transitionTaskInTx(ctx, tx, taskID, taskStateSkipped)
		}
	}

	if strings.TrimSpace(def.ScriptText) != "" {
		if err := runTaskScript(ctx, tx, taskID, def.ScriptText, vars, actorUserID); err != nil {
			return fmt.Errorf("task script failed for task %s: %w", taskID, err)
		}
	}
	return nil
}

func evalTaskCondition(ctx context.Context, tx pgx.Tx, taskID, code string, vars map[string]any, actorUserID string) (bool, error) {
	result, err := executeTaskGoja(ctx, tx, taskID, code, vars, actorUserID)
	if err != nil {
		return false, err
	}
	b, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf("run_if script must return a boolean, got %T", result)
	}
	return b, nil
}

func runTaskScript(ctx context.Context, tx pgx.Tx, taskID, code string, vars map[string]any, actorUserID string) error {
	_, err := executeTaskGoja(ctx, tx, taskID, code, vars, actorUserID)
	return err
}

// executeTaskGoja runs Goja code with the task scope in Input. Writes the
// script performs via ctx.records are audited to actorUserID.
func executeTaskGoja(ctx context.Context, tx pgx.Tx, taskID, code string, vars map[string]any, actorUserID string) (any, error) {
	opts := ScriptExecutionOptions{
		Code:        code,
		TableName:   "base_task",
		TriggerType: "task_engine",
		EventName:   "task_run",
		UserID:      actorUserID,
		Input: map[string]any{
			"task_id":   taskID,
			"variables": vars,
		},
	}
	result, err := executeJavaScriptWithQuerier(ctx, tx, opts)
	if err != nil {
		return nil, err
	}
	return result.Result, nil
}

// CompleteTask closes a task as done, publishing its declared outputs to
// the parent scope and firing the definition's spawn rules.
func CompleteTask(ctx context.Context, taskID, actorUserID string) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := transitionTaskInTx(ctx, tx, taskID, taskStateDone); err != nil {
		return err
	}
	if err := onTaskClosed(ctx, tx, taskID, actorUserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// onTaskClosed publishes the task's outputs into the parent's variables
// (namespaced by child slug) and fires spawn rules for the done reason.
func onTaskClosed(ctx context.Context, tx pgx.Tx, taskID, actorUserID string) error {
	var parentID *string
	var varsJSON []byte
	err := tx.QueryRow(ctx,
		`SELECT parent_task_id::text, variables FROM base_task WHERE _id = $1`, taskID).Scan(&parentID, &varsJSON)
	if err != nil {
		return err
	}

	vars := map[string]any{}
	if len(varsJSON) > 0 {
		_ = json.Unmarshal(varsJSON, &vars)
	}
	outputs, _ := vars["outputs"].(map[string]any)

	// Publish up: parent's variables.outputs.<child slug> = this outputs.
	if parentID != nil && *parentID != "" && outputs != nil {
		var defID *string
		_ = tx.QueryRow(ctx, `SELECT definition_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&defID)
		if defID != nil && *defID != "" {
			var slug string
			if err := tx.QueryRow(ctx, `SELECT slug FROM _task_definition WHERE _id = $1`, *defID).Scan(&slug); err == nil {
				var parentVars map[string]any
				var parentJSON []byte
				if err := tx.QueryRow(ctx, `SELECT variables FROM base_task WHERE _id = $1`, *parentID).Scan(&parentJSON); err == nil {
					parentVars = map[string]any{}
					_ = json.Unmarshal(parentJSON, &parentVars)
					parentOutputs, _ := parentVars["outputs"].(map[string]any)
					if parentOutputs == nil {
						parentOutputs = map[string]any{}
					}
					parentOutputs[slug] = outputs
					parentVars["outputs"] = parentOutputs
					updated, _ := json.Marshal(parentVars)
					_, _ = tx.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, *parentID, string(updated))
				}
			}
		}
	}

	return fireSpawnRules(ctx, tx, taskID, taskStateDone, actorUserID)
}

// fireSpawnRules spawns follow-on tasks per the definition's spawn_rules
// for the given closure reason. assign "same" copies this task's group;
// "pool" leaves the follow-on ungrouped for manual routing.
func fireSpawnRules(ctx context.Context, tx pgx.Tx, taskID, closureReason, actorUserID string) error {
	defID, err := getDefinitionIDByTaskID(ctx, tx, taskID)
	if err != nil || defID == "" {
		return err
	}
	def, err := getTaskDefinition(ctx, tx, defID)
	if err != nil {
		return err
	}

	var groupID *string
	_ = tx.QueryRow(ctx, `SELECT assignment_group_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&groupID)

	vars, err := readTaskVariables(ctx, tx, taskID)
	if err != nil {
		return err
	}

	for _, rule := range def.SpawnRules {
		if rule.On != closureReason {
			continue
		}
		inputs := map[string]any{}
		for childInput, source := range rule.InputMapping {
			if val, ok := resolveScopeRef(vars, source); ok {
				inputs[childInput] = val
			}
		}
		if rule.Assign == "pool" {
			groupID = nil
		}
		if err := spawnFollowOn(ctx, tx, rule.DefinitionSlug, groupID, actorUserID, inputs); err != nil {
			return err
		}
	}
	return nil
}

func spawnFollowOn(ctx context.Context, tx pgx.Tx, slug string, groupID *string, requestedBy string, inputs map[string]any) error {
	var defID string
	err := tx.QueryRow(ctx,
		`SELECT _id::text FROM _task_definition WHERE slug = $1 AND is_active AND _deleted_at IS NULL`, slug).Scan(&defID)
	if err != nil {
		return fmt.Errorf("spawn rule references unknown definition %q: %w", slug, err)
	}
	def, err := getTaskDefinition(ctx, tx, defID)
	if err != nil {
		return err
	}
	// Follow-on takes the rule's group when given, else the definition's default.
	if groupID != nil {
		def.DefaultGroupID = *groupID
	}
	_, err = spawnTaskForDefinition(ctx, tx, def, "", requestedBy, inputs)
	return err
}

func readTaskVariables(ctx context.Context, q scriptQuerier, taskID string) (map[string]any, error) {
	var varsJSON []byte
	if err := q.QueryRow(ctx, `SELECT variables FROM base_task WHERE _id = $1`, taskID).Scan(&varsJSON); err != nil {
		return nil, err
	}
	vars := map[string]any{}
	if len(varsJSON) > 0 {
		_ = json.Unmarshal(varsJSON, &vars)
	}
	return vars, nil
}

func getDefinitionIDByTaskID(ctx context.Context, q scriptQuerier, taskID string) (string, error) {
	var defID *string
	if err := q.QueryRow(ctx, `SELECT definition_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&defID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if defID == nil {
		return "", nil
	}
	return *defID, nil
}

// resolveScopeRef resolves a scope reference "$outputs.x" (or a plain key
// lookup) against a variables map. Non-$ values are literals and pass
// through unchanged.
func resolveScopeRef(scope map[string]any, ref any) (any, bool) {
	s, ok := ref.(string)
	if !ok {
		return ref, true
	}
	if !strings.HasPrefix(s, "$") {
		return ref, true
	}
	parts := strings.SplitN(strings.TrimPrefix(s, "$"), ".", 2)
	if len(parts) != 2 {
		return nil, false
	}
	sectionMap, ok := scope[parts[0]].(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := sectionMap[parts[1]]
	return v, ok
}
