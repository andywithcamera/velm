package db

// Control runtime (Process Designer, Plan T4 / Requirements §2, AC-22 to AC-45).
//
// Every test here seeds a definition tree with raw SQL, spawns it through the
// engine, and reads the run tree back from base_task. The definition is the
// input; the run is the output; nothing here goes through the editor.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// controlTestDefinition seeds a definition and removes it, and every run it
// produced, when the test ends.
func controlTestDefinition(t *testing.T, ctx context.Context, slug string, container bool) string {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition (slug, display_name, is_container)
		VALUES ($1, $1, $2) RETURNING _id::text`, slug+"-"+suffix, container).Scan(&id); err != nil {
		t.Fatalf("seed definition %q: %v", slug, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = Pool.Exec(bg, `DELETE FROM base_task WHERE definition_id = $1 OR parent_task_id IN (SELECT _id FROM base_task WHERE definition_id = $1)`, id)
		_, _ = Pool.Exec(bg, `DELETE FROM base_task WHERE parent_task_id IS NULL AND variables->'inputs'->>'__control_test_definition' = $1`, id)
		_, _ = Pool.Exec(bg, `DELETE FROM base_task WHERE parent_task_id IS NULL AND title LIKE 'control-run-%'`)
		_, _ = Pool.Exec(bg, `DELETE FROM base_task_definition WHERE _id = $1`, id)
	})
	return id
}

// controlTestBlock inserts one block and returns its id.
func controlTestBlock(t *testing.T, ctx context.Context, containerID, parentID, childID, kind, blockType, label string, position int, config, mapping map[string]any) string {
	t.Helper()
	var parent any
	if parentID != "" {
		parent = parentID
	}
	var child any
	if childID != "" {
		child = childID
	}
	var blockTypeArg any
	if blockType != "" {
		blockTypeArg = blockType
	}
	cfg := config
	if cfg == nil {
		cfg = map[string]any{}
	}
	if label != "" {
		cfg["label"] = label
	}
	cfgJSON, _ := json.Marshal(cfg)
	mapJSON, _ := json.Marshal(mapping)
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, parent_block_id, position,
			 sort_order, block_kind, block_type, config, input_mapping)
		VALUES ($1, $2, $3, $4, $4, $5, $6, $7::jsonb, $8::jsonb)
		RETURNING _id::text`,
		containerID, child, parent, position, kind, blockTypeArg,
		string(cfgJSON), string(mapJSON)).Scan(&id); err != nil {
		t.Fatalf("seed block %q: %v", label, err)
	}
	return id
}

// controlRunRows returns every task in a spawned run, keyed by task id, and
// the run's root ids.
func controlRunRows(t *testing.T, ctx context.Context, rootID string) map[string]map[string]any {
	t.Helper()
	rows, err := Pool.Query(ctx, `
		SELECT _id::text, COALESCE(definition_id::text, ''), title,
			COALESCE(work_type, ''), state, COALESCE(closure_reason, ''),
			COALESCE(parent_task_id::text, ''), variables
		FROM base_task
		WHERE _id = $1 OR parent_task_id = $1
		ORDER BY number`, rootID)
	if err != nil {
		t.Fatalf("read run rows: %v", err)
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		var id, defID, title, workType, state, reason, parent string
		var vj []byte
		if err := rows.Scan(&id, &defID, &title, &workType, &state, &reason, &parent, &vj); err != nil {
			t.Fatalf("scan run row: %v", err)
		}
		vars := map[string]any{}
		_ = json.Unmarshal(vj, &vars)
		out[id] = map[string]any{
			"id": id, "definition_id": defID, "title": title, "work_type": workType,
			"state": state, "closure_reason": reason, "parent_task_id": parent, "variables": vars,
		}
	}
	return out
}

// controlSeedLeaf seeds a leaf definition with one input so mapping has
// something to bind.
func controlSeedLeaf(t *testing.T, ctx context.Context, slug string) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition (slug, display_name, is_container)
		VALUES ($1, $1, FALSE)
		RETURNING _id::text`, slug).Scan(&id); err != nil {
		t.Fatalf("seed leaf %q: %v", slug, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = Pool.Exec(bg, `DELETE FROM base_task WHERE definition_id = $1`, id)
		_, _ = Pool.Exec(bg, `DELETE FROM base_task_definition WHERE _id = $1`, id)
	})
	return id
}

func controlSpawn(t *testing.T, ctx context.Context, defID string, inputs map[string]any) string {
	t.Helper()
	taskID, err := SpawnTaskInstance(ctx, Pool, defID, "", "", inputs)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = Pool.Exec(bg, `DELETE FROM base_task WHERE parent_task_id = $1`, taskID)
		_, _ = Pool.Exec(bg, `DELETE FROM base_task WHERE _id = $1`, taskID)
	})
	return taskID
}

// AC-22 and AC-23: trigger, if, end at the top level spawn a root plus two
// CONTROL children, and each control card names its kind and label.
func TestSpawnIfBlockTree(t *testing.T) {
	ctx := schemaTestDB(t)
	leaf := controlSeedLeaf(t, ctx, "control-if-leaf")
	container := controlTestDefinition(t, ctx, "control-if", true)

	controlTestBlock(t, ctx, container, "", "", "trigger", "", "Ordered", 10, nil, nil)
	ifBlock := controlTestBlock(t, ctx, container, "", "", "control", "if", "exit_code == 0", 20,
		map[string]any{"condition": `expr: inputs.exit_code == 0`}, nil)
	controlTestBlock(t, ctx, container, ifBlock, leaf, "task", "", "Reboot", 10,
		nil, map[string]any{"server_name": "$inputs.server_name"})
	controlTestBlock(t, ctx, container, "", "", "control", "end", "", 30, nil, nil)

	rootID := controlSpawn(t, ctx, container, map[string]any{"exit_code": float64(0), "server_name": "web-1"})

	var total, controlCount int
	if err := Pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE work_type = 'CONTROL')
		FROM base_task WHERE parent_task_id = $1 AND _deleted_at IS NULL`, rootID).Scan(&total, &controlCount); err != nil {
		t.Fatalf("count children: %v", err)
	}
	if total != 2 || controlCount != 2 {
		t.Fatalf("root children = %d (%d CONTROL), want 2 and 2", total, controlCount)
	}

	rows, err := Pool.Query(ctx, `
		SELECT title, COALESCE(variables->'block'->>'type', '')
		FROM base_task WHERE parent_task_id = $1 ORDER BY number`, rootID)
	if err != nil {
		t.Fatalf("read control children: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var title, typ string
		if err := rows.Scan(&title, &typ); err != nil {
			t.Fatalf("scan control child: %v", err)
		}
		got[typ] = title
	}
	for _, typ := range []string{"if", "end"} {
		if _, ok := got[typ]; !ok {
			t.Fatalf("no CONTROL child of type %q; got %v", typ, got)
		}
	}
	if !strings.Contains(got["if"], "If exit_code == 0") {
		t.Fatalf("if title = %q, want it to name the kind and the block label", got["if"])
	}
	if !strings.Contains(got["end"], "End") {
		t.Fatalf("end title = %q, want it to name End", got["end"])
	}
}

// AC-25: the condition is CEL behind `expr:`.
func TestEvaluateIfCondition(t *testing.T) {
	cases := []struct {
		name string
		role string
		want bool
	}{
		{"primary is true", "primary", true},
		{"standby is false", "standby", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvaluateIfCondition(`expr: inputs.server.role == "primary"`,
				map[string]any{"server": map[string]any{"role": tc.role}})
			if err != nil {
				t.Fatalf("EvaluateIfCondition: %v", err)
			}
			if got != tc.want {
				t.Fatalf("condition with role %q = %v, want %v", tc.role, got, tc.want)
			}
		})
	}
	if _, err := EvaluateIfCondition("exit_code == 0", nil); err == nil {
		t.Fatal("a condition without the expr: prefix was evaluated; it must be refused")
	}
}

// AC-27 and AC-28: a false `if` spawns its scope skipped rather than omitting
// it, and a skipped scope publishes nothing into the parent.
func TestIfFalseSkipsScope(t *testing.T) {
	ctx := schemaTestDB(t)
	leaf := controlSeedLeaf(t, ctx, "control-if-false-leaf")
	container := controlTestDefinition(t, ctx, "control-if-false", true)

	controlTestBlock(t, ctx, container, "", "", "trigger", "", "Ordered", 10, nil, nil)
	ifBlock := controlTestBlock(t, ctx, container, "", "", "control", "if", "role", 20,
		map[string]any{"condition": `expr: inputs.server.role == "primary"`}, nil)
	controlTestBlock(t, ctx, container, ifBlock, leaf, "task", "", "Reboot", 10,
		nil, map[string]any{"server_name": "$inputs.server_name"})

	rootID := controlSpawn(t, ctx, container, map[string]any{
		"server": map[string]any{"role": "standby"}, "server_name": "web-1",
	})

	var ifState string
	if err := Pool.QueryRow(ctx, `
		SELECT state FROM base_task
		WHERE parent_task_id = $1 AND variables->'block'->>'type' = 'if'`, rootID).Scan(&ifState); err != nil {
		t.Fatalf("read if state: %v", err)
	}
	if ifState != taskStateSkipped {
		t.Fatalf("false if state = %q, want %q", ifState, taskStateSkipped)
	}
	rows, err := Pool.Query(ctx, `
		SELECT title, state FROM base_task
		WHERE parent_task_id IN (SELECT _id FROM base_task WHERE parent_task_id = $1)`, rootID)
	if err != nil {
		t.Fatalf("read skipped scope: %v", err)
	}
	defer rows.Close()
	scoped := 0
	for rows.Next() {
		var title, state string
		if err := rows.Scan(&title, &state); err != nil {
			t.Fatalf("scan skipped scope: %v", err)
		}
		scoped++
		if state != taskStateSkipped {
			t.Fatalf("scoped task %q state = %q, want %q", title, state, taskStateSkipped)
		}
	}
	if scoped != 1 {
		t.Fatalf("skipped scope has %d tasks, want 1 (the definition tree, not a gap)", scoped)
	}
	vars := taskVars(ctx, t, rootID)
	if outputs, _ := vars["outputs"].(map[string]any); len(outputs) != 0 {
		t.Fatalf("root outputs = %v after a skipped scope, want none", outputs)
	}
}

// AC-29 and AC-30: for_each creates one control task and one task per element,
// each carrying $item and $index.
func TestSpawnForEachTree(t *testing.T) {
	ctx := schemaTestDB(t)
	leaf := controlSeedLeaf(t, ctx, "control-foreach-leaf")
	container := controlTestDefinition(t, ctx, "control-foreach", true)

	controlTestBlock(t, ctx, container, "", "", "trigger", "", "Ordered", 10, nil, nil)
	feBlock := controlTestBlock(t, ctx, container, "", "", "control", "for_each", "hosts", 20,
		map[string]any{"collection": "$inputs.hosts"}, nil)
	controlTestBlock(t, ctx, container, feBlock, leaf, "task", "", "Reboot", 10,
		nil, map[string]any{"server_name": "$item"})

	rootID := controlSpawn(t, ctx, container, map[string]any{"hosts": []any{"a", "b", "c"}})

	var controlID string
	if err := Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task
		WHERE parent_task_id = $1 AND work_type = 'CONTROL' AND variables->'block'->>'type' = 'for_each'`,
		rootID).Scan(&controlID); err != nil {
		t.Fatalf("read for_each control task: %v", err)
	}
	var childCount int
	if err := Pool.QueryRow(ctx, `
		SELECT count(*) FROM base_task WHERE parent_task_id = $1`, controlID).Scan(&childCount); err != nil {
		t.Fatalf("count iterations: %v", err)
	}
	if childCount != 3 {
		t.Fatalf("for_each children = %d, want 3", childCount)
	}
	var runTasks int
	if err := Pool.QueryRow(ctx, `
		SELECT count(*) FROM base_task
		WHERE _id = $1 OR parent_task_id = $1 OR parent_task_id IN (SELECT _id FROM base_task WHERE parent_task_id = $1)`,
		rootID).Scan(&runTasks); err != nil {
		t.Fatalf("count run tasks: %v", err)
	}
	if runTasks != 5 {
		t.Fatalf("run has %d rows (root + control + 3 iterations), want 5", runTasks)
	}

	rows, err := Pool.Query(ctx, `
		SELECT variables FROM base_task WHERE parent_task_id = $1 ORDER BY number`, controlID)
	if err != nil {
		t.Fatalf("read iterations: %v", err)
	}
	defer rows.Close()
	var items []string
	var indexes []int
	for rows.Next() {
		var vj []byte
		if err := rows.Scan(&vj); err != nil {
			t.Fatalf("scan iteration: %v", err)
		}
		vars := map[string]any{}
		_ = json.Unmarshal(vj, &vars)
		item, _ := vars["item"].(string)
		index, _ := vars["index"].(float64)
		items = append(items, item)
		indexes = append(indexes, int(index))
		if inputs, _ := vars["inputs"].(map[string]any); inputs["server_name"] != item {
			t.Fatalf("iteration %q input server_name = %v, want the $item value", item, inputs["server_name"])
		}
	}
	if strings.Join(items, ",") != "a,b,c" {
		t.Fatalf("items = %v, want a,b,c", items)
	}
	if fmt.Sprint(indexes) != "[0 1 2]" {
		t.Fatalf("indexes = %v, want 0,1,2", indexes)
	}
}

// controlForEachDefinition seeds a definition whose only child of the root is
// a for_each block over $inputs.hosts, scoping one leaf.
func controlForEachDefinition(t *testing.T, ctx context.Context, slug, leafSlug string) string {
	t.Helper()
	leaf := controlSeedLeaf(t, ctx, leafSlug)
	container := controlTestDefinition(t, ctx, slug, true)
	controlTestBlock(t, ctx, container, "", "", "trigger", "", "Ordered", 10, nil, nil)
	feBlock := controlTestBlock(t, ctx, container, "", "", "control", "for_each", "hosts", 20,
		map[string]any{"collection": "$inputs.hosts"}, nil)
	controlTestBlock(t, ctx, container, feBlock, leaf, "task", "", "Reboot", 10,
		nil, map[string]any{"server_name": "$item"})
	return container
}

// AC-32 to AC-34: exactly 100 items succeed; 101 are refused with a message
// that names the cap and the block, and the refusal writes no row.
func TestForEachCapBoundary(t *testing.T) {
	ctx := schemaTestDB(t)

	hosts := func(n int) []any {
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, fmt.Sprintf("host-%03d", i))
		}
		return out
	}

	t.Run("exactly 100", func(t *testing.T) {
		container := controlForEachDefinition(t, ctx, "control-cap-100", "control-cap-leaf-100")
		rootID := controlSpawn(t, ctx, container, map[string]any{"hosts": hosts(100)})
		var controlID string
		if err := Pool.QueryRow(ctx, `
			SELECT _id::text FROM base_task
			WHERE parent_task_id = $1 AND variables->'block'->>'type' = 'for_each'`, rootID).Scan(&controlID); err != nil {
			t.Fatalf("read for_each control task: %v", err)
		}
		var count int
		if err := Pool.QueryRow(ctx, `SELECT count(*) FROM base_task WHERE parent_task_id = $1`, controlID).Scan(&count); err != nil {
			t.Fatalf("count iterations: %v", err)
		}
		if count != 100 {
			t.Fatalf("for_each over 100 items created %d children, want 100", count)
		}
	})

	t.Run("101", func(t *testing.T) {
		container := controlForEachDefinition(t, ctx, "control-cap-101", "control-cap-leaf-101")
		before := countTasksForDefinition(t, ctx, container)
		_, err := SpawnTaskInstance(ctx, Pool, container, "", "", map[string]any{"hosts": hosts(101)})
		if err == nil {
			t.Fatal("for_each over 101 items was spawned; the cap must refuse it")
		}
		if !strings.Contains(err.Error(), "100") {
			t.Fatalf("cap error %q does not name the cap (100)", err.Error())
		}
		if !strings.Contains(err.Error(), "hosts") {
			t.Fatalf("cap error %q does not name the block", err.Error())
		}
		after := countTasksForDefinition(t, ctx, container)
		if after != before {
			t.Fatalf("refused spawn wrote %d rows (before %d, after %d); the cap must be checked before any write",
				after-before, before, after)
		}
	})
}

func countTasksForDefinition(t *testing.T, ctx context.Context, defID string) int {
	t.Helper()
	var count int
	if err := Pool.QueryRow(ctx, `SELECT count(*) FROM base_task WHERE definition_id = $1`, defID).Scan(&count); err != nil {
		t.Fatalf("count tasks for definition: %v", err)
	}
	return count
}

// AC-36: a collection that resolves to a string is refused at spawn, naming
// the block and the field, and no row is written.
func TestForEachCollectionNotArray(t *testing.T) {
	ctx := schemaTestDB(t)
	container := controlForEachDefinition(t, ctx, "control-foreach-scalar", "control-scalar-leaf")
	_, err := SpawnTaskInstance(ctx, Pool, container, "", "", map[string]any{"hosts": "a,b,c"})
	if err == nil {
		t.Fatal("for_each over a string was spawned; it must be refused")
	}
	if !strings.Contains(err.Error(), "hosts") || !strings.Contains(strings.ToLower(err.Error()), "array") {
		t.Fatalf("error %q does not name the block and the field", err.Error())
	}
	if got := countTasksForDefinition(t, ctx, container); got != 0 {
		t.Fatalf("refused spawn wrote %d rows, want 0", got)
	}
}

// AC-37: an `end` materialises as a control task and closes when the siblings
// it scopes are closed, through the existing promotion pass.
func TestEndClosesOnScopeClose(t *testing.T) {
	ctx := schemaTestDB(t)
	leaf := controlSeedLeaf(t, ctx, "control-end-leaf")
	container := controlTestDefinition(t, ctx, "control-end", true)

	controlTestBlock(t, ctx, container, "", "", "trigger", "", "Ordered", 10, nil, nil)
	controlTestBlock(t, ctx, container, "", leaf, "task", "", "Reboot", 20, nil, nil)
	controlTestBlock(t, ctx, container, "", "", "control", "end", "", 30, nil, nil)

	rootID := controlSpawn(t, ctx, container, nil)

	var workID, endID string
	if err := Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task
		WHERE parent_task_id = $1 AND COALESCE(work_type, 'TASK') <> 'CONTROL'`, rootID).Scan(&workID); err != nil {
		t.Fatalf("read work task: %v", err)
	}
	if err := Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task
		WHERE parent_task_id = $1 AND variables->'block'->>'type' = 'end'`, rootID).Scan(&endID); err != nil {
		t.Fatalf("read end task: %v", err)
	}
	if state := taskState(ctx, t, endID); state != taskStatePending {
		t.Fatalf("end state before its siblings close = %q, want pending", state)
	}

	if err := CloseTaskWithReason(ctx, workID, "", "completed"); err != nil {
		t.Fatalf("close work task: %v", err)
	}
	if state := taskState(ctx, t, endID); state != taskStateDone {
		t.Fatalf("end state after its siblings closed = %q, want closed", state)
	}
	var reason string
	if err := Pool.QueryRow(ctx, `SELECT COALESCE(closure_reason, '') FROM base_task WHERE _id = $1`, endID).Scan(&reason); err != nil {
		t.Fatalf("read end closure reason: %v", err)
	}
	if reason != "completed" {
		t.Fatalf("end closure_reason = %q, want completed", reason)
	}
}

// AC-24: `if` is evaluated once, when reached. Changing the stored condition
// afterwards does not change a run that already exists.
func TestIfNotReevaluated(t *testing.T) {
	ctx := schemaTestDB(t)
	leaf := controlSeedLeaf(t, ctx, "control-reeval-leaf")
	container := controlTestDefinition(t, ctx, "control-reeval", true)

	controlTestBlock(t, ctx, container, "", "", "trigger", "", "Ordered", 10, nil, nil)
	ifBlock := controlTestBlock(t, ctx, container, "", "", "control", "if", "flag", 20,
		map[string]any{"condition": `expr: inputs.flag == true`}, nil)
	controlTestBlock(t, ctx, container, ifBlock, leaf, "task", "", "Reboot", 10, nil, nil)

	rootID := controlSpawn(t, ctx, container, map[string]any{"flag": true})
	var scopedID string
	if err := Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task
		WHERE parent_task_id IN (SELECT _id FROM base_task WHERE parent_task_id = $1)`,
		rootID).Scan(&scopedID); err != nil {
		t.Fatalf("read scoped task: %v", err)
	}
	before := taskState(ctx, t, scopedID)
	if before == taskStateSkipped {
		t.Fatalf("true branch spawned its scope %q", before)
	}

	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition_child SET config = jsonb_set(config, '{condition}', '"expr: inputs.flag == false"')
		WHERE _id = $1`, ifBlock); err != nil {
		t.Fatalf("mutate definition condition: %v", err)
	}
	if after := taskState(ctx, t, scopedID); after != before {
		t.Fatalf("scoped task state changed from %q to %q after the definition was edited", before, after)
	}
}

// AC-31: $parent.* resolves up the chain, so a grandchild binds a value two
// levels above it.
func TestParentScopeRefBinds(t *testing.T) {
	ctx := schemaTestDB(t)
	leaf := controlSeedLeaf(t, ctx, "control-parent-leaf")

	inner := controlTestDefinition(t, ctx, "control-parent-inner", true)
	innerBlock := controlTestBlock(t, ctx, inner, "", leaf, "task", "", "Reboot", 10,
		nil, map[string]any{"server_name": "$parent.inputs.server_name"})

	container := controlTestDefinition(t, ctx, "control-parent-root", true)
	controlTestBlock(t, ctx, container, "", "", "trigger", "", "Ordered", 10, nil, nil)
	ifBlock := controlTestBlock(t, ctx, container, "", "", "control", "if", "always", 20,
		map[string]any{"condition": "expr: true"}, nil)
	controlTestBlock(t, ctx, container, ifBlock, inner, "task", "", "Inner", 10, nil, nil)
	_ = innerBlock

	rootID := controlSpawn(t, ctx, container, map[string]any{"server_name": "web-1"})

	var grandchildID string
	if err := Pool.QueryRow(ctx, `
		SELECT child._id::text FROM base_task child
		WHERE child.parent_task_id IN (
			SELECT mid._id FROM base_task mid
			WHERE mid.parent_task_id IN (SELECT _id FROM base_task WHERE parent_task_id = $1)
			  AND COALESCE(mid.work_type, 'TASK') <> 'CONTROL')`, rootID).Scan(&grandchildID); err != nil {
		t.Fatalf("read grandchild: %v", err)
	}
	vars := taskVars(ctx, t, grandchildID)
	inputs, _ := vars["inputs"].(map[string]any)
	if inputs["server_name"] != "web-1" {
		t.Fatalf("grandchild inputs.server_name = %v, want web-1 from $parent.inputs.server_name", inputs["server_name"])
	}
}

// AC-38: a failure part-way through a control tree writes nothing; the whole
// tree is inside one spawn transaction.
func TestSpawnRollbackLeavesNoRows(t *testing.T) {
	ctx := schemaTestDB(t)
	leaf := controlSeedLeaf(t, ctx, "control-rollback-leaf")
	container := controlTestDefinition(t, ctx, "control-rollback", true)

	controlTestBlock(t, ctx, container, "", "", "trigger", "", "Ordered", 10, nil, nil)
	// The collection is not resolvable until the block is reached, so the
	// pre-flight check cannot refuse it: rows are written and then the
	// promotion pass fails.
	feBlock := controlTestBlock(t, ctx, container, "", "", "control", "for_each", "late hosts", 20,
		map[string]any{"collection": "$outputs.upstream.hosts"}, nil)
	controlTestBlock(t, ctx, container, feBlock, leaf, "task", "", "Reboot", 10, nil, nil)

	if _, err := SpawnTaskInstance(ctx, Pool, container, "", "", nil); err == nil {
		t.Fatal("spawn with an unresolvable collection succeeded; it must fail")
	}
	if got := countTasksForDefinition(t, ctx, container); got != 0 {
		t.Fatalf("failed spawn left %d rows behind, want 0", got)
	}
}

// AC-43: DescribeTaskInstance reports control tasks among the children, each
// carrying work_type and block_type.
func TestDescribeTaskInstanceIncludesControl(t *testing.T) {
	ctx := schemaTestDB(t)
	leaf := controlSeedLeaf(t, ctx, "control-describe-leaf")
	container := controlTestDefinition(t, ctx, "control-describe", true)

	controlTestBlock(t, ctx, container, "", "", "trigger", "", "Ordered", 10, nil, nil)
	ifBlock := controlTestBlock(t, ctx, container, "", "", "control", "if", "always", 20,
		map[string]any{"condition": "expr: true"}, nil)
	controlTestBlock(t, ctx, container, ifBlock, leaf, "task", "", "Reboot", 10, nil, nil)
	controlTestBlock(t, ctx, container, "", "", "control", "end", "", 30, nil, nil)

	rootID := controlSpawn(t, ctx, container, nil)
	_, children, err := DescribeTaskInstance(ctx, Pool, rootID)
	if err != nil {
		t.Fatalf("DescribeTaskInstance: %v", err)
	}
	types := map[string]string{}
	for _, child := range children {
		types[child.BlockType] = child.WorkType
	}
	if types["if"] != "CONTROL" || types["end"] != "CONTROL" {
		t.Fatalf("control children = %v, want if and end reported with work_type CONTROL", types)
	}
}
