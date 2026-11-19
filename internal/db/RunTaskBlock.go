package db

// The `run_task` control block (Process Designer, Plan slice 6 /
// Requirements AC-93 to AC-110; Design Vision D4, D8, D9).
//
// A `run_task` block calls another definition: the author picks the target,
// maps each of its inputs to a scope expression or a literal, and ordering
// the definition spawns the target as a child task that the block waits for.
//
// The mapping row is `{from: "expr"|"literal", value: …}`. A literal is passed
// through untouched — including one that begins with `$` (AC-100). An
// expression resolves against the enclosing task's scope, so `$inputs.server`
// and `$outputs.upgrade.success` both work.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// mappingEntry splits a stored input_mapping value into its mode and value.
// A bare string is the pre-slice-6 form: an expression when it starts with
// `$`, a literal otherwise. taskCost blocks keep that form.
func mappingEntry(raw any) (string, any) {
	if row, ok := raw.(map[string]any); ok {
		from, _ := row["from"].(string)
		return strings.TrimSpace(from), row["value"]
	}
	return "", raw
}

// resolveRunTaskInputs turns the block's mapping rows into the child's input
// document. An expression that cannot be resolved against the scope fails the
// block with a message naming the block and the input, rather than spawning a
// child with a missing value.
func resolveRunTaskInputs(b DefinitionBlock, scope map[string]any) (map[string]any, error) {
	inputs := map[string]any{}
	for name, raw := range b.InputMapping {
		from, value := mappingEntry(raw)
		switch from {
		case "literal":
			// AC-100: a literal is never evaluated, even when it begins
			// with `$`.
			inputs[name] = value
		case "expr":
			text, _ := value.(string)
			ref := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "expr:"))
			resolved, ok := resolveScopeRef(scope, ref)
			if !ok {
				return nil, fmt.Errorf("run task %s: input %s cannot be resolved from %q", blockDisplayLabel(b), name, text)
			}
			inputs[name] = resolved
		default:
			// A bare string keeps the engine's existing convention: a `$`
			// reference resolves, anything else is passed as typed.
			if text, isString := value.(string); isString {
				if resolved, ok := resolveScopeRef(scope, strings.TrimSpace(text)); ok {
					inputs[name] = resolved
					continue
				}
			}
			inputs[name] = value
		}
	}
	return inputs, nil
}

// runRunTaskBlock spawns the target definition with the mapped inputs and
// leaves the block's control row open: it closes when the child reaches a
// terminal state and the roll-up sees it (AC-105, AC-106).
func runRunTaskBlock(ctx context.Context, q scriptQuerier, inst childInstanceState, b DefinitionBlock, containerDefID, actorUserID string, scope, runInputs map[string]any) error {
	targetID := strings.TrimSpace(b.ChildDefinitionID)
	if targetID == "" {
		return closeRunTaskBlock(ctx, q, inst.taskID, actorUserID, false,
			fmt.Errorf("run task block %q names no definition", blockDisplayLabel(b)))
	}
	inputs, err := resolveRunTaskInputs(b, scope)
	if err != nil {
		return closeRunTaskBlock(ctx, q, inst.taskID, actorUserID, false, err)
	}
	// Persist what the block was asked to pass, so the run row says what the
	// author configured even before the child closes (AC-105).
	vars, err := readTaskVariables(ctx, q, inst.taskID)
	if err != nil {
		return err
	}
	vars["inputs"] = inputs
	block, _ := vars["block"].(map[string]any)
	if block == nil {
		block = map[string]any{}
	}
	block["input_mapping"] = b.InputMapping
	block["inputs"] = inputs
	vars["block"] = block
	if raw, err := json.Marshal(vars); err == nil {
		if _, err := q.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, inst.taskID, string(raw)); err != nil {
			return err
		}
	}
	route := &taskRoute{Ready: true, Scope: scope, AssignInputs: runInputs}
	if _, err := spawnTaskWithRouteByID(ctx, q, targetID, inst.taskID, actorUserID, inputs, route); err != nil {
		return closeRunTaskBlock(ctx, q, inst.taskID, actorUserID, false, err)
	}
	return nil
}

// closeRunTaskBlock closes the block's control row after a spawn the engine
// could not perform. A spawned child is closed by the roll-up instead.
func closeRunTaskBlock(ctx context.Context, q scriptQuerier, taskID, actorUserID string, ok bool, cause error) error {
	return closeScriptTask(ctx, q, taskID, actorUserID, ok, cause)
}

// loadRunTaskBlock recovers the run_task block behind a control task, the same
// way loadRetryBlock does for a retry.
func loadRunTaskBlock(ctx context.Context, q scriptQuerier, taskID string) (DefinitionBlock, string, bool, error) {
	vars, err := readTaskVariables(ctx, q, taskID)
	if err != nil {
		return DefinitionBlock{}, "", false, err
	}
	blockID, containerDefID, kind, typ, _, _, ok := blockFromVars(vars)
	if !ok || kind != "control" || typ != "run_task" {
		return DefinitionBlock{}, "", false, nil
	}
	blocks, err := getDefinitionBlocks(ctx, q, containerDefID)
	if err != nil {
		return DefinitionBlock{}, "", false, err
	}
	for _, b := range blocks {
		if b.ID == blockID {
			return b, containerDefID, true, nil
		}
	}
	return DefinitionBlock{ID: blockID, Kind: "control", Type: "run_task"}, containerDefID, true, nil
}

// publishRunTaskResult makes the block's outcome readable to a following `if`
// as `$blocks.<key>.output.success` (AC-107). It runs when the block's own
// control row rolls up: completed is success, anything else is failure.
func publishRunTaskResult(ctx context.Context, q scriptQuerier, controlTaskID, reason string) error {
	b, _, isRunTask, err := loadRunTaskBlock(ctx, q, controlTaskID)
	if err != nil || !isRunTask {
		return err
	}
	key := strings.TrimSpace(b.blockConfigString("key"))
	if key == "" {
		return nil
	}
	output := map[string]any{
		"success": reason == "completed" || reason == "cancelled",
		"state":   reason,
	}
	vars, err := readTaskVariables(ctx, q, controlTaskID)
	if err != nil {
		return err
	}
	block, _ := vars["block"].(map[string]any)
	if block == nil {
		block = map[string]any{}
	}
	block["result"] = output
	vars["block"] = block
	if raw, err := json.Marshal(vars); err == nil {
		if _, err := q.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, controlTaskID, string(raw)); err != nil {
			return err
		}
	}
	return publishBlockOutput(ctx, q, controlTaskID, key, output)
}
