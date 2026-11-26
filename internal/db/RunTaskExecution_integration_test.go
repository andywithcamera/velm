package db

// Slice 7 of the designer rework: executing a `run_task` block (Design Vision
// D4, D9; Requirements AC-105 to AC-109). Real Postgres: a skip is a broken
// run, not a pass.
//
// The definition is the input, the run tree in base_task is the output. Every
// test seeds a definition tree, starts it through the engine, and reads the
// rows back.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// rtDefinition seeds a definition for a run_task test and removes its blocks
// when the test ends.
func rtDefinition(t *testing.T, ctx context.Context, slug string, container bool) string {
	t.Helper()
	id := schemaTestDefinition(t, ctx, slug, container)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = Pool.Exec(bg, `DELETE FROM base_task_definition_child WHERE container_definition_id = $1`, id)
	})
	return id
}

// rtControl reads the id and stored block record of the only control task of
// the given type under parent.
func rtControl(t *testing.T, ctx context.Context, parent, blockType string) (string, map[string]any) {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		SELECT t._id::text FROM base_task t
		JOIN base_task_variables v ON v.task_id = t._id
		WHERE t.parent_task_id = $1 AND t.work_type = 'CONTROL'
		  AND v.variables->'block'->>'type' = $2`, parent, blockType).Scan(&id); err != nil {
		t.Fatalf("read %s control task: %v", blockType, err)
	}
	vars := taskVars(ctx, t, id)
	block, _ := vars["block"].(map[string]any)
	return id, block
}

// rtChild reads the single spawned run of a child definition under parent.
func rtChild(t *testing.T, ctx context.Context, parent, defID string) (string, map[string]any) {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task
		WHERE parent_task_id = $1 AND definition_id = $2 AND _deleted_at IS NULL`,
		parent, defID).Scan(&id); err != nil {
		t.Fatalf("read spawned child: %v", err)
	}
	return id, taskVars(ctx, t, id)
}

func rtState(t *testing.T, ctx context.Context, taskID string) string {
	t.Helper()
	state, _ := rtStateReason(t, ctx, taskID)
	return state
}

// rtStateReason reads state and closure_reason together: since 104 a skipped
// block is closed, and only the reason says it did not run.
func rtStateReason(t *testing.T, ctx context.Context, taskID string) (string, string) {
	t.Helper()
	var state, reason string
	if err := Pool.QueryRow(ctx,
		`SELECT state, COALESCE(closure_reason, '') FROM base_task WHERE _id = $1`, taskID).Scan(&state, &reason); err != nil {
		t.Fatalf("read task state: %v", err)
	}
	return state, reason
}

// AC-105: ordering spawns the target as a real child of the block's row with
// the mapped inputs resolved, and the block records the child task number.
func TestRunTaskSpawnsChildWithMappedInputs(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	childSlug := fmt.Sprintf("slice7-mapped-child-%d", time.Now().UnixNano())
	childDef := rtDefinition(t, ctx, childSlug, false)
	container := rtDefinition(t, ctx, fmt.Sprintf("slice7-mapped-%d", time.Now().UnixNano()), true)
	if err := s4AssignDefinition(ctx, container, actor); err != nil {
		t.Fatalf("assign definition: %v", err)
	}
	if err := SaveDefinitionStructure(ctx, container, []StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "rt", Kind: "control", Type: "run_task", Slug: childSlug, Label: "Reboot", Position: 10,
			Config: map[string]any{"key": "rt"},
			Mapping: map[string]any{
				"server": map[string]any{"from": "expr", "value": "$inputs.server_name"},
				"reason": map[string]any{"from": "literal", "value": "post-upgrade"},
			}},
	}); err != nil {
		t.Fatalf("save definition: %v", err)
	}
	root, err := SpawnTaskInstance(ctx, Pool, container, "", actor, map[string]any{"server_name": "web-1"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	controlID, block := rtControl(t, ctx, root, "run_task")

	childID, childVars := rtChild(t, ctx, controlID, childDef)

	inputs, _ := childVars["inputs"].(map[string]any)
	if inputs["server"] != "web-1" {
		t.Errorf("child inputs.server = %v, want the resolved $inputs.server_name (web-1)", inputs["server"])
	}
	if inputs["reason"] != "post-upgrade" {
		t.Errorf("child inputs.reason = %v, want the literal post-upgrade", inputs["reason"])
	}

	var childNumber string
	if err := Pool.QueryRow(ctx, `SELECT number FROM base_task WHERE _id = $1`, childID).Scan(&childNumber); err != nil {
		t.Fatalf("read child number: %v", err)
	}
	if got, _ := block["child_task_id"].(string); got != childID {
		t.Errorf("block records child_task_id = %v, want %s", block["child_task_id"], childID)
	}
	if got, _ := block["child_task_number"].(string); got != childNumber {
		t.Errorf("block records child_task_number = %v, want %s", block["child_task_number"], childNumber)
	}
	if got, _ := block["inputs"].(map[string]any); got["server"] != "web-1" {
		t.Errorf("block inputs = %v, want the mapped values it passed", block["inputs"])
	}
}

// AC-106: the parent does not advance past the block while the child is open.
// The scope's later rows exist as pending placeholders from the spawn, but
// none of them is opened until the run_task block closes.
func TestRunTaskWaitsForChild(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	childSlug := fmt.Sprintf("slice7-wait-child-%d", time.Now().UnixNano())
	childDef := rtDefinition(t, ctx, childSlug, false)
	nextSlug := fmt.Sprintf("slice7-wait-next-%d", time.Now().UnixNano())
	nextDef := rtDefinition(t, ctx, nextSlug, false)
	container := rtDefinition(t, ctx, fmt.Sprintf("slice7-wait-%d", time.Now().UnixNano()), true)
	if err := s4AssignDefinition(ctx, container, actor); err != nil {
		t.Fatalf("assign definition: %v", err)
	}
	if err := SaveDefinitionStructure(ctx, container, []StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "rt", Kind: "control", Type: "run_task", Slug: childSlug, Label: "Reboot", Position: 10,
			Config: map[string]any{"key": "rt"}},
		{Key: "next", Kind: "task", Slug: nextSlug, Label: "Next", Position: 20},
	}); err != nil {
		t.Fatalf("save definition: %v", err)
	}
	root, err := SpawnTaskInstance(ctx, Pool, container, "", actor, map[string]any{})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	controlID, _ := rtControl(t, ctx, root, "run_task")
	childID, _ := rtChild(t, ctx, controlID, childDef)
	if state := rtState(t, ctx, childID); state != taskStateReady {
		t.Fatalf("spawned child state = %q, want %q (open)", state, taskStateReady)
	}
	nextID, _ := rtChild(t, ctx, root, nextDef)
	if state := rtState(t, ctx, nextID); state != taskStatePending {
		t.Fatalf("later sibling state = %q while the child is open, want %q: the parent advanced past the block",
			state, taskStatePending)
	}

	// The child reaching a terminal state closes the block, and only then is
	// the later sibling opened.
	if err := CloseTaskWithReason(ctx, childID, actor, "completed"); err != nil {
		t.Fatalf("close child: %v", err)
	}
	if state := rtState(t, ctx, controlID); state != taskStateDone {
		t.Errorf("run_task block state = %q after the child closed, want %q", state, taskStateDone)
	}
	if state := rtState(t, ctx, nextID); state != taskStateReady {
		t.Errorf("later sibling state = %q after the child closed, want %q", state, taskStateReady)
	}
}

// AC-107: a failing child fails the parent block and publishes success=false,
// so a following `if` on its success takes ELSE rather than the parent
// continuing silently.
func TestRunTaskChildFailurePropagates(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	childSlug := fmt.Sprintf("slice7-fail-child-%d", time.Now().UnixNano())
	childDef := rtDefinition(t, ctx, childSlug, false)
	thenSlug := fmt.Sprintf("slice7-fail-then-%d", time.Now().UnixNano())
	thenDef := rtDefinition(t, ctx, thenSlug, false)
	elseSlug := fmt.Sprintf("slice7-fail-else-%d", time.Now().UnixNano())
	elseDef := rtDefinition(t, ctx, elseSlug, false)
	container := rtDefinition(t, ctx, fmt.Sprintf("slice7-fail-%d", time.Now().UnixNano()), true)
	if err := s4AssignDefinition(ctx, container, actor); err != nil {
		t.Fatalf("assign definition: %v", err)
	}
	if err := SaveDefinitionStructure(ctx, container, []StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "rt", Kind: "control", Type: "run_task", Slug: childSlug, Label: "Reboot", Position: 10,
			Config: map[string]any{"key": "rt"}},
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 20,
			Config: map[string]any{"condition": "expr: $blocks.rt.output.success == true"}},
		{Key: "then", Parent: "i", Kind: "task", Slug: thenSlug, Label: "Then work", Branch: "then", Position: 10},
		{Key: "else", Parent: "i", Kind: "task", Slug: elseSlug, Label: "Else work", Branch: "else", Position: 20},
	}); err != nil {
		t.Fatalf("save definition: %v", err)
	}
	root, err := SpawnTaskInstance(ctx, Pool, container, "", actor, map[string]any{})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	controlID, _ := rtControl(t, ctx, root, "run_task")
	childID, _ := rtChild(t, ctx, controlID, childDef)
	if err := CloseTaskWithReason(ctx, childID, actor, "failed"); err != nil {
		t.Fatalf("fail child: %v", err)
	}

	var state string
	var reason string
	if err := Pool.QueryRow(ctx, `
		SELECT state, COALESCE(closure_reason, '') FROM base_task WHERE _id = $1`,
		controlID).Scan(&state, &reason); err != nil {
		t.Fatalf("read failed block: %v", err)
	}
	if state != taskStateDone || reason != "failed" {
		t.Errorf("run_task block = %s/%s after the child failed, want %s/failed", state, reason, taskStateDone)
	}

	rootVars := taskVars(ctx, t, root)
	blocks, _ := rootVars["blocks"].(map[string]any)
	rt, _ := blocks["rt"].(map[string]any)
	output, _ := rt["output"].(map[string]any)
	if success, ok := output["success"].(bool); !ok || success {
		t.Errorf("$blocks.rt.output.success = %v, want false", output["success"])
	}

	// The following if must run its ELSE branch, not the THEN branch.
	ifID, _ := rtControl(t, ctx, root, "if")
	thenChild, _ := rtChild(t, ctx, ifID, thenDef)
	elseChild, _ := rtChild(t, ctx, ifID, elseDef)
	if state, reason := rtStateReason(t, ctx, thenChild); state != taskStateDone || reason != taskClosureSkipped {
		t.Errorf("THEN branch = %q/%q after child failure, want closed/skipped", state, reason)
	}
	if _, reason := rtStateReason(t, ctx, elseChild); reason == taskClosureSkipped {
		t.Errorf("ELSE branch closure_reason = %q after child failure, want it to run", reason)
	}

	// Closing the ELSE work lets the run roll up: the failure has propagated
	// to the parent rather than being swallowed.
	if err := CloseTaskWithReason(ctx, elseChild, actor, "completed"); err != nil {
		t.Fatalf("close else work: %v", err)
	}
	var rootState, rootReason string
	if err := Pool.QueryRow(ctx, `
		SELECT state, COALESCE(closure_reason, '') FROM base_task WHERE _id = $1`,
		root).Scan(&rootState, &rootReason); err != nil {
		t.Fatalf("read root: %v", err)
	}
	if rootState != taskStateDone || rootReason != "failed" {
		t.Errorf("parent = %s/%s after a failed child, want %s/failed", rootState, rootReason, taskStateDone)
	}
}

// AC-108: the Brief's worked example. upgrade-server runs its run_script,
// gates on that script's success, then spawns reboot-server with server from
// the run's input and reason = post-upgrade.
func TestUpgradeServerEndToEnd(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	rebootSlug := fmt.Sprintf("slice7-reboot-%d", time.Now().UnixNano())
	rebootDef := rtDefinition(t, ctx, rebootSlug, false)
	upgrade := rtDefinition(t, ctx, fmt.Sprintf("slice7-upgrade-%d", time.Now().UnixNano()), true)
	if err := s4AssignDefinition(ctx, upgrade, actor); err != nil {
		t.Fatalf("assign definition: %v", err)
	}
	if err := SaveDefinitionStructure(ctx, upgrade, []StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "s", Kind: "control", Type: "run_script", Label: "Upgrade OS", Position: 10,
			Config: map[string]any{"key": "s", "script": `function run(ctx) { return { package: "kernel" }; }`}},
		{Key: "i", Kind: "control", Type: "if", Label: "Did it work", Position: 20,
			Config: map[string]any{"condition": "expr: $blocks.s.output.success == true"}},
		{Key: "rt", Parent: "i", Kind: "control", Type: "run_task", Slug: rebootSlug, Label: "Reboot", Branch: "then", Position: 10,
			Config: map[string]any{"key": "rt"},
			Mapping: map[string]any{
				"server": map[string]any{"from": "expr", "value": "$inputs.server_name"},
				"reason": map[string]any{"from": "literal", "value": "post-upgrade"},
			}},
		{Key: "e", Parent: "i", Kind: "control", Type: "end", Label: "End", Branch: "then", Position: 20},
	}); err != nil {
		t.Fatalf("save definition: %v", err)
	}
	root, err := SpawnTaskInstance(ctx, Pool, upgrade, "", actor, map[string]any{"server_name": "web-1"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	scriptID, scriptBlock := rtControl(t, ctx, root, "run_script")
	if state := rtState(t, ctx, scriptID); state != taskStateDone {
		t.Errorf("run_script state = %q, want %q before the if is evaluated", state, taskStateDone)
	}
	if scriptBlock["error"] != nil {
		t.Fatalf("script failed: %v", scriptBlock["error"])
	}
	ifID, _ := rtControl(t, ctx, root, "if")

	controlID, block := rtControl(t, ctx, ifID, "run_task")
	childID, childVars := rtChild(t, ctx, controlID, rebootDef)
	inputs, _ := childVars["inputs"].(map[string]any)
	if inputs["server"] != "web-1" {
		t.Errorf("reboot inputs.server = %v, want web-1 from $inputs.server_name", inputs["server"])
	}
	if inputs["reason"] != "post-upgrade" {
		t.Errorf("reboot inputs.reason = %v, want post-upgrade", inputs["reason"])
	}

	// Spawn order: script, then if, then the run_task block, then reboot.
	var scriptNumber, ifNumber, controlNumber, childNumber string
	for id, dest := range map[string]*string{scriptID: &scriptNumber, ifID: &ifNumber, controlID: &controlNumber, childID: &childNumber} {
		if err := Pool.QueryRow(ctx, `SELECT number FROM base_task WHERE _id = $1`, id).Scan(dest); err != nil {
			t.Fatalf("read number: %v", err)
		}
	}
	if !(scriptNumber < ifNumber && ifNumber < controlNumber && controlNumber < childNumber) {
		t.Errorf("spawn order numbers = script %s, if %s, run_task %s, reboot %s; want increasing",
			scriptNumber, ifNumber, controlNumber, childNumber)
	}
	if got, _ := block["child_task_number"].(string); got != childNumber {
		t.Errorf("run_task block records child_task_number = %v, want %s", block["child_task_number"], childNumber)
	}
}

// AC-109: a run_task inside an if THEN scope spawns only when the condition
// holds; with a false condition the child row is never created.
func TestRunTaskInsideIfGated(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	rebootSlug := fmt.Sprintf("slice7-gated-reboot-%d", time.Now().UnixNano())
	rebootDef := rtDefinition(t, ctx, rebootSlug, false)
	upgrade := rtDefinition(t, ctx, fmt.Sprintf("slice7-gated-%d", time.Now().UnixNano()), true)
	if err := s4AssignDefinition(ctx, upgrade, actor); err != nil {
		t.Fatalf("assign definition: %v", err)
	}
	if err := SaveDefinitionStructure(ctx, upgrade, []StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 10,
			Config: map[string]any{"condition": "expr: $inputs.run_reboot == true"}},
		{Key: "rt", Parent: "i", Kind: "control", Type: "run_task", Slug: rebootSlug, Label: "Reboot", Branch: "then", Position: 10,
			Config: map[string]any{"key": "rt"}},
	}); err != nil {
		t.Fatalf("save definition: %v", err)
	}
	root, err := SpawnTaskInstance(ctx, Pool, upgrade, "", actor, map[string]any{"run_reboot": false})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var count int
	if err := Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM base_task
		WHERE definition_id = $1 AND _deleted_at IS NULL
		  AND _id IN (SELECT _id FROM base_task WHERE parent_task_id IN
			(SELECT _id FROM base_task WHERE parent_task_id = $2))`, rebootDef, root).Scan(&count); err != nil {
		t.Fatalf("count gated child: %v", err)
	}
	if count != 0 {
		t.Errorf("a run_task in a skipped THEN scope spawned %d child rows, want 0", count)
	}
}

// The child task record stays readable by number, which is what the block's
// record is for: an operator reading the parent block can find the run.
func TestRunTaskBlockRecordNamesChild(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	childSlug := fmt.Sprintf("slice7-record-child-%d", time.Now().UnixNano())
	childDef := rtDefinition(t, ctx, childSlug, false)
	container := rtDefinition(t, ctx, fmt.Sprintf("slice7-record-%d", time.Now().UnixNano()), true)
	if err := s4AssignDefinition(ctx, container, actor); err != nil {
		t.Fatalf("assign definition: %v", err)
	}
	if err := SaveDefinitionStructure(ctx, container, []StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "rt", Kind: "control", Type: "run_task", Slug: childSlug, Label: "Reboot", Position: 10,
			Config: map[string]any{"key": "rt"}},
	}); err != nil {
		t.Fatalf("save definition: %v", err)
	}
	root, err := SpawnTaskInstance(ctx, Pool, container, "", actor, map[string]any{})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	controlID, block := rtControl(t, ctx, root, "run_task")
	childID, _ := rtChild(t, ctx, controlID, childDef)
	var number, title string
	if err := Pool.QueryRow(ctx, `SELECT number, title FROM base_task WHERE _id = $1`, childID).Scan(&number, &title); err != nil {
		t.Fatalf("read child: %v", err)
	}
	recorded, _ := block["child_task_number"].(string)
	if recorded != number {
		t.Fatalf("block child_task_number = %v, want %s (%s)", block["child_task_number"], number, title)
	}
}
