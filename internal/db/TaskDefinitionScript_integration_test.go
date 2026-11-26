package db

// Slice 4 of the designer rework: run_script at run time (Design Vision D4/D5,
// Requirements AC-55, AC-60 to AC-69, AC-71). Real Postgres: a skip is a
// broken run, not a pass.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func s4User(t *testing.T, ctx context.Context, admin bool) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email, password_hash)
		VALUES ($1, $2, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
		RETURNING _id::text`, "slice4", fmt.Sprintf("slice4-%d@example.test", time.Now().UnixNano())).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM _group_membership WHERE user_id = $1`, id)
		_, _ = Pool.Exec(context.Background(), `DELETE FROM base_task WHERE requested_by_user_id = $1`, id)
		_, _ = Pool.Exec(context.Background(), `DELETE FROM _user WHERE _id = $1`, id)
	})
	var groupID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _group (name, description) VALUES ($1, 'slice 4') RETURNING _id::text`,
		fmt.Sprintf("slice4-group-%d", time.Now().UnixNano())).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if _, err := Pool.Exec(ctx, `INSERT INTO _group_membership (group_id, user_id) VALUES ($1, $2)`, groupID, id); err != nil {
		t.Fatalf("join group: %v", err)
	}
	if admin {
		if err := EnsureAdministratorsGroup(ctx); err != nil {
			t.Fatalf("ensure admin group: %v", err)
		}
		var groupID string
		if err := Pool.QueryRow(ctx, `SELECT _id::text FROM _group WHERE name = $1`, AdministratorsGroupName).Scan(&groupID); err != nil {
			t.Fatalf("read admin group: %v", err)
		}
		if _, err := Pool.Exec(ctx, `INSERT INTO _group_membership (group_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, groupID, id); err != nil {
			t.Fatalf("join admin group: %v", err)
		}
		InvalidateAuthzCacheForUser(id)
	}
	return id
}

// s4AssignDefinition routes a definition to the actor's group and the actor,
// so the root is assigned when StartTask runs.
func s4AssignDefinition(ctx context.Context, defID, actor string) error {
	_, err := Pool.Exec(ctx, `
		UPDATE base_task_definition d
		SET default_group_id = gm.group_id, default_assignee_id = gm.user_id::uuid
		FROM _group_membership gm
		WHERE d._id = $1 AND gm.user_id = $2`, defID, actor)
	return err
}

// s4Run saves a one-script definition and starts it, returning the root and
// the script's control task id.
func s4Run(t *testing.T, ctx context.Context, script string, config map[string]any, inputs map[string]any, actor string) (string, string) {
	t.Helper()
	defID := schemaTestDefinition(t, ctx, fmt.Sprintf("slice4-%d", time.Now().UnixNano()), true)
	if err := s4AssignDefinition(ctx, defID, actor); err != nil {
		t.Fatalf("assign definition: %v", err)
	}
	cfg := map[string]any{"script": script, "label": "Upgrade"}
	for key, value := range config {
		cfg[key] = value
	}
	if err := SaveDefinitionStructure(ctx, defID, []StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "s", Kind: "control", Type: "run_script", Label: "Upgrade", Position: 10, Config: cfg},
	}); err != nil {
		t.Fatalf("save definition: %v", err)
	}
	root, err := SpawnTaskInstance(ctx, Pool, defID, "", actor, inputs)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// A ready container opens its children in the spawn transaction, so the
	// script has already run by the time spawn returns.
	var control string
	if err := Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task
		WHERE parent_task_id = $1 AND work_type = 'CONTROL'
		ORDER BY _created_at LIMIT 1`, root).Scan(&control); err != nil {
		t.Fatalf("read control task: %v", err)
	}
	return root, control
}

func s4Block(t *testing.T, ctx context.Context, taskID string) map[string]any {
	t.Helper()
	vars, err := readTaskVariables(ctx, Pool, taskID)
	if err != nil {
		t.Fatalf("read variables: %v", err)
	}
	block, _ := vars["block"].(map[string]any)
	return block
}

// AC-55: a pre-102 if with two children gains branch='then' on both, and no
// child under an if is left NULL.
func TestBranchBackfillOnExistingIf(t *testing.T) {
	ctx := schemaTestDB(t)
	defID := schemaTestDefinition(t, ctx, fmt.Sprintf("slice4-backfill-%d", time.Now().UnixNano()), true)
	childID := schemaTestDefinition(t, ctx, fmt.Sprintf("slice4-backfill-child-%d", time.Now().UnixNano()), false)
	var ifID, firstID, secondID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, block_kind, block_type, position, sort_order, config)
		VALUES ($1, 'control', 'if', 10, 10, '{"label":"Gate","condition":"expr: true"}'::jsonb)
		RETURNING _id::text`, defID).Scan(&ifID); err != nil {
		t.Fatalf("seed if: %v", err)
	}
	for index, order := range []int{10, 20} {
		var id string
		if err := Pool.QueryRow(ctx, `
			INSERT INTO base_task_definition_child
				(container_definition_id, child_definition_id, parent_block_id, block_kind, position, sort_order, branch)
			VALUES ($1, $2, $3, 'task', $4, $4, NULL)
			RETURNING _id::text`, defID, childID, ifID, order).Scan(&id); err != nil {
			t.Fatalf("seed child %d: %v", index, err)
		}
		if index == 0 {
			firstID = id
		} else {
			secondID = id
		}
	}
	// The pre-upgrade state is represented by clearing the branch, then the
	// migration's own statement runs.
	if _, err := Pool.Exec(ctx, `UPDATE base_task_definition_child SET branch = NULL WHERE parent_block_id = $1`, ifID); err != nil {
		t.Fatalf("clear branch: %v", err)
	}
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition_child c
		SET branch = 'then'
		FROM base_task_definition_child p
		WHERE c.parent_block_id = p._id
		  AND p.block_kind = 'control' AND p.block_type = 'if'
		  AND c.branch IS NULL`); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	for _, id := range []string{firstID, secondID} {
		var branch string
		if err := Pool.QueryRow(ctx, `SELECT COALESCE(branch, '') FROM base_task_definition_child WHERE _id = $1`, id).Scan(&branch); err != nil {
			t.Fatalf("read child: %v", err)
		}
		if branch != "then" {
			t.Errorf("child %s branch = %q, want then", id, branch)
		}
	}
	var nulls int
	_ = Pool.QueryRow(ctx, `
		SELECT count(*) FROM base_task_definition_child c
		JOIN base_task_definition_child p ON p._id = c.parent_block_id
		WHERE p.block_kind = 'control' AND p.block_type = 'if' AND c.branch IS NULL`).Scan(&nulls)
	if nulls != 0 {
		t.Errorf("%d children of an if are still NULL", nulls)
	}
}

// AC-60: a run_script row seeded straight into the table is refused at run
// time for a non-admin, and the failure names the gate.
func TestScriptBlockRefusedAtRun(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, false)
	_, control := s4Run(t, ctx, "function run(ctx) { return { ok: true } }", nil, map[string]any{}, actor)
	var state, reason string
	if err := Pool.QueryRow(ctx, `SELECT state, COALESCE(closure_reason, '') FROM base_task WHERE _id = $1`, control).Scan(&state, &reason); err != nil {
		t.Fatalf("read control: %v", err)
	}
	if state != "closed" || reason != "failed" {
		t.Fatalf("non-admin script = %s/%s, want closed/failed", state, reason)
	}
	block := s4Block(t, ctx, control)
	if block["error"] != "run script blocks require the admin role" {
		t.Fatalf("failure message = %v", block["error"])
	}
}

// AC-61: timeout_ms bounds the body; a loop past it fails with the named error.
func TestScriptBlockTimeout(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	_, control := s4Run(t, ctx, "function run(ctx) { while (true) {} }",
		map[string]any{"timeout_ms": 100}, map[string]any{}, actor)
	block := s4Block(t, ctx, control)
	message, _ := block["error"].(string)
	if !strings.Contains(message, "script execution timed out") {
		t.Fatalf("timeout error = %q", message)
	}
}

// AC-62: the body sees console, input, app, user, trigger, ctx and task.
func TestScriptBlockGlobalsAndTask(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	body := `function run(ctx) {
		console.log("hi");
		return { has: [typeof console, typeof input, typeof app, typeof user, typeof trigger, typeof ctx, typeof task].join(","),
		         number: task.number };
	}`
	_, control := s4Run(t, ctx, body, nil, map[string]any{"server": "a"}, actor)
	block := s4Block(t, ctx, control)
	result, _ := block["result"].(map[string]any)
	jsonValue, _ := result["json"].(map[string]any)
	if jsonValue["has"] != "object,object,object,object,object,object,object" {
		t.Fatalf("globals = %v", jsonValue["has"])
	}
	if text, _ := jsonValue["number"].(string); text == "" {
		t.Fatalf("task.number is empty")
	}
}

// AC-63: a scalar input and a member of an object-valued input both resolve.
func TestScriptBlockInputsResolveObjects(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	body := `function run(ctx) { return { ip: inputs.server.ip, host: inputs.host }; }`
	_, control := s4Run(t, ctx, body, nil, map[string]any{
		"server": map[string]any{"ip": "10.0.0.5"}, "host": "web-1",
	}, actor)
	result, _ := s4Block(t, ctx, control)["result"].(map[string]any)
	jsonValue, _ := result["json"].(map[string]any)
	if jsonValue["ip"] != "10.0.0.5" || jsonValue["host"] != "web-1" {
		t.Fatalf("inputs resolved to %v", jsonValue)
	}
}

// AC-64: values assigned to outputs are stored at variables.block.outputs.
func TestScriptOutputsStored(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	_, control := s4Run(t, ctx, `function run(ctx) { outputs.exit_code = 0; return { ok: true }; }`, nil, map[string]any{}, actor)
	outputs, _ := s4Block(t, ctx, control)["outputs"].(map[string]any)
	if outputs["exit_code"] == nil {
		t.Fatalf("outputs = %v, want exit_code", outputs)
	}
}

// AC-65 to AC-67: the four keys, the success flag, and parsed JSON.
func TestScriptResultShape(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	_, control := s4Run(t, ctx, `function run(ctx) { return { ok: true }; }`, nil, map[string]any{}, actor)
	result, _ := s4Block(t, ctx, control)["result"].(map[string]any)
	for _, key := range []string{"success", "stdout", "stderr", "json"} {
		if _, present := result[key]; !present {
			t.Errorf("result object has no %q: %v", key, result)
		}
	}
	if result["success"] != true {
		t.Errorf("success = %v", result["success"])
	}
}

func TestScriptSuccessFlag(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	_, control := s4Run(t, ctx, `function run(ctx) { throw new Error("boom"); }`, nil, map[string]any{}, actor)
	result, _ := s4Block(t, ctx, control)["result"].(map[string]any)
	if result["success"] != false {
		t.Fatalf("a thrown body reported success = %v", result["success"])
	}
	if text, _ := result["stderr"].(string); text == "" {
		t.Fatalf("stderr is empty for a thrown body")
	}
}

func TestScriptParsedJSON(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	_, control := s4Run(t, ctx, `function run(ctx) { result = JSON.parse('{"exit":0}'); return 1; }`, nil, map[string]any{}, actor)
	result, _ := s4Block(t, ctx, control)["result"].(map[string]any)
	jsonValue, _ := result["json"].(map[string]any)
	if jsonValue["exit"] != float64(0) {
		t.Fatalf("json = %v", result["json"])
	}
	_, plain := s4Run(t, ctx, `function run(ctx) { result = "not json"; return 1; }`, nil, map[string]any{}, actor)
	plainResult, _ := s4Block(t, ctx, plain)["result"].(map[string]any)
	if plainResult["json"] != nil || plainResult["success"] != true {
		t.Fatalf("non-JSON result = %v (%T)", plainResult, plainResult["json"])
	}
}

// AC-68: a following if selects THEN on success and ELSE on failure.
func TestIfSelectsThenAndElse(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	leafSlug := fmt.Sprintf("slice4-leaf-%d", time.Now().UnixNano())
	schemaTestDefinition(t, ctx, leafSlug, false)
	for _, tc := range []struct {
		name   string
		script string
		taken  string
	}{
		{"success", `function run(ctx) { return { ok: true }; }`, "Then work"},
		{"failure", `function run(ctx) { throw new Error("boom"); }`, "Else work"},
	} {
		defID := schemaTestDefinition(t, ctx, fmt.Sprintf("slice4-if-%s-%d", tc.name, time.Now().UnixNano()), true)
		if err := SaveDefinitionStructure(ctx, defID, []StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
			{Key: "s", Kind: "control", Type: "run_script", Label: "Upgrade", Position: 10, Config: map[string]any{"script": tc.script}},
			{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 20, Config: map[string]any{"condition": "expr: $blocks.s.output.success == true"}},
			{Key: "then", Parent: "i", Kind: "task", Slug: leafSlug, Label: "Then work", Branch: "then", Position: 10},
			{Key: "else", Parent: "i", Kind: "task", Slug: leafSlug, Label: "Else work", Branch: "else", Position: 20},
			{Key: "e", Parent: "i", Kind: "control", Type: "end", Label: "End", Position: 30},
		}); err != nil {
			t.Fatalf("%s: save: %v", tc.name, err)
		}
		root, err := SpawnTaskInstance(ctx, Pool, defID, "", actor, map[string]any{})
		if err != nil {
			t.Fatalf("%s: spawn: %v", tc.name, err)
		}
		_ = root
		// The branch that did not run is closed, not a state of its own
		// (104): state closed, closure_reason skipped.
		type branchOutcome struct{ state, reason string }
		states := map[string]branchOutcome{}
		rows, err := Pool.Query(ctx, `
			SELECT COALESCE(v.variables->'block'->>'label', ''), t.state,
			       COALESCE(t.closure_reason, '')
			FROM base_task t
			LEFT JOIN base_task_variables v ON v.task_id = t._id
			WHERE t.parent_task_id IN
				(SELECT _id FROM base_task WHERE parent_task_id = $1)`, root)
		if err != nil {
			t.Fatalf("%s: read branches: %v", tc.name, err)
		}
		for rows.Next() {
			var label string
			var outcome branchOutcome
			if err := rows.Scan(&label, &outcome.state, &outcome.reason); err != nil {
				t.Fatalf("scan: %v", err)
			}
			states[label] = outcome
		}
		rows.Close()
		taken := states[tc.taken]
		if taken.state != "ready" && taken.state != "in_progress" && taken.state != "closed" || taken.reason == "skipped" {
			t.Errorf("%s: %s = %+v, want it to run (all %v)", tc.name, tc.taken, taken, states)
		}
		other := "Then work"
		if tc.taken == "Then work" {
			other = "Else work"
		}
		if got := states[other]; got.state != "closed" || got.reason != "skipped" {
			t.Errorf("%s: %s = %+v, want closed/skipped", tc.name, other, got)
		}
	}
}

// AC-69: a script assigning nothing yields outputs = {} and no error.
func TestEmptyOutputsIsFalsy(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	_, control := s4Run(t, ctx, `function run(ctx) { return 1; }`, nil, map[string]any{}, actor)
	block := s4Block(t, ctx, control)
	outputs, ok := block["outputs"].(map[string]any)
	if !ok || len(outputs) != 0 {
		t.Fatalf("outputs = %v, want {}", block["outputs"])
	}
	if block["error"] != nil {
		t.Fatalf("an empty script recorded an error: %v", block["error"])
	}
}

// AC-71: fetch is undefined inside the block and never reaches the network.
func TestScriptHasNoNetwork(t *testing.T) {
	ctx := schemaTestDB(t)
	actor := s4User(t, ctx, true)
	_, control := s4Run(t, ctx, `function run(ctx) { return fetch("https://example.com"); }`, nil, map[string]any{}, actor)
	block := s4Block(t, ctx, control)
	message, _ := block["error"].(string)
	if !strings.Contains(strings.ToLower(message), "fetch") {
		t.Fatalf("fetch error = %q", message)
	}
}

var _ = pgx.ErrNoRows
