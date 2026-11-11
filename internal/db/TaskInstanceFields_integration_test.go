package db

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func instanceTestSuffix(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
}

// TestSpawnPopulatesInstanceFields covers the fields a spawned task used to
// leave blank: description, priority from the request, planned start from a
// scheduling input, and inputs that are an object rather than JSON null.
func TestSpawnPopulatesInstanceFields(t *testing.T) {
	ctx := taskEngineTestDB(t)

	suffix := instanceTestSuffix(t)
	defID := seedDefinition(ctx, t, "seed-fields-"+suffix, "Seeded Work", false, "")
	if _, err := Pool.Exec(ctx, `UPDATE base_task_definition SET description = $2 WHERE _id = $1`,
		defID, "Produce a patch artifact on a branch."); err != nil {
		t.Fatalf("set definition description: %v", err)
	}

	rootID, err := SpawnTaskInstance(ctx, Pool, defID, "", "", map[string]any{
		"body":         "Add delta sync to the importer.",
		"priority":     "high",
		"window_start": "2026-11-20T22:00:00Z",
	})
	if err != nil {
		t.Fatalf("spawn with inputs: %v", err)
	}

	var description, priority, state string
	var plannedStart *string
	var vars map[string]any
	if err := Pool.QueryRow(ctx, `
		SELECT COALESCE(description, ''), priority, state, planned_start_at::text, variables
		FROM base_task WHERE _id = $1`, rootID).Scan(&description, &priority, &state, &plannedStart, &vars); err != nil {
		t.Fatalf("read spawned task: %v", err)
	}

	if want := "Add delta sync to the importer.\n\nProduce a patch artifact on a branch."; description != want {
		t.Fatalf("description = %q, want %q", description, want)
	}
	if priority != "high" {
		t.Fatalf("priority = %q, want high (from the request)", priority)
	}
	if plannedStart == nil {
		t.Fatal("planned_start_at was not set from window_start")
	}
	if state != taskStateReady {
		t.Fatalf("state = %q, want ready", state)
	}
	inputs, ok := vars["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("variables.inputs is %T, want an object: %#v", vars["inputs"], vars["inputs"])
	}
	if inputs["body"] != "Add delta sync to the importer." {
		t.Fatalf("inputs were not stored: %#v", inputs)
	}

	// A nil input map must serialise as {} rather than null: the catalog
	// start passes nil, and "inputs": null is a different type from the {}
	// every definition script does `input.variables.inputs || {}` against.
	nilID, err := SpawnTaskInstance(ctx, Pool, defID, "", "", nil)
	if err != nil {
		t.Fatalf("spawn with nil inputs: %v", err)
	}
	var raw json.RawMessage
	if err := Pool.QueryRow(ctx, `SELECT variables FROM base_task WHERE _id = $1`, nilID).Scan(&raw); err != nil {
		t.Fatalf("read variables: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode variables: %v", err)
	}
	if _, ok := decoded["inputs"].(map[string]any); !ok {
		t.Fatalf("nil spawn left inputs as %#v, want {}", decoded["inputs"])
	}
	var nilDescription string
	if err := Pool.QueryRow(ctx, `SELECT COALESCE(description, '') FROM base_task WHERE _id = $1`, nilID).
		Scan(&nilDescription); err != nil {
		t.Fatalf("read description: %v", err)
	}
	if nilDescription != "Produce a patch artifact on a branch." {
		t.Fatalf("description = %q, want the definition's description", nilDescription)
	}
}

// TestPromotionRefreshesDescriptionButKeepsEdits covers the promote-time
// refresh: a child spawns with empty inputs and only learns the requester's
// words when its input_mapping resolves, so the description is rewritten
// then — unless a human has already replaced it.
func TestPromotionRefreshesDescriptionButKeepsEdits(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := instanceTestSuffix(t)

	childDef := seedDefinition(ctx, t, "promo-child-"+suffix, "Promoted Stage", false, "")
	if _, err := Pool.Exec(ctx, `UPDATE base_task_definition SET description = $2 WHERE _id = $1`,
		childDef, "Run the suite against a specific commit."); err != nil {
		t.Fatalf("set child description: %v", err)
	}
	parentDef := seedDefinition(ctx, t, "promo-parent-"+suffix, "Promoting Container", true, "")
	seedChildLink(ctx, t, parentDef, childDef, 1, map[string]any{"body": "$inputs.body"})

	parentID, err := SpawnTaskInstance(ctx, Pool, parentDef, "", "", map[string]any{
		"body": "Tenant onboarding must not drop rows.",
	})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	var childID string
	if err := Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task WHERE parent_task_id = $1 ORDER BY _id LIMIT 1`, parentID).
		Scan(&childID); err != nil {
		t.Fatalf("find child: %v", err)
	}

	// Child spawned with the definition's purpose only.
	var spawned string
	if err := Pool.QueryRow(ctx, `SELECT COALESCE(description,'') FROM base_task WHERE _id = $1`, childID).
		Scan(&spawned); err != nil {
		t.Fatalf("read child description: %v", err)
	}
	if spawned != "Run the suite against a specific commit." {
		t.Fatalf("child description at spawn = %q", spawned)
	}

	// 085's trigger refuses in_progress without an assignee inside the
	// group, so both rows are assigned the way a dispatcher would.
	actorID, groupID := seedActorAndGroup(ctx, t)
	for _, id := range []string{parentID, childID} {
		if _, err := Pool.Exec(ctx, `UPDATE base_task SET assignment_group_id = $2, assigned_user_id = $3 WHERE _id = $1`,
			id, groupID, actorID); err != nil {
			t.Fatalf("assign task: %v", err)
		}
	}

	if err := StartTask(ctx, parentID, actorID); err != nil {
		t.Fatalf("start container: %v", err)
	}

	var promoted string
	if err := Pool.QueryRow(ctx, `SELECT COALESCE(description,'') FROM base_task WHERE _id = $1`, childID).
		Scan(&promoted); err != nil {
		t.Fatalf("read child description after promotion: %v", err)
	}
	want := "Tenant onboarding must not drop rows.\n\nRun the suite against a specific commit."
	if promoted != want {
		t.Fatalf("child description after promotion = %q, want %q", promoted, want)
	}

	// A hand-written description must survive a later re-bind.
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET description = $2 WHERE _id = $1`,
		childID, "Mine: only the tenant suite."); err != nil {
		t.Fatalf("set custom description: %v", err)
	}
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET state = 'pending' WHERE _id = $1`, childID); err != nil {
		t.Fatalf("reset child state: %v", err)
	}
	if err := StartTask(ctx, parentID, actorID); err != nil {
		t.Fatalf("restart container: %v", err)
	}
	var kept string
	if err := Pool.QueryRow(ctx, `SELECT COALESCE(description,'') FROM base_task WHERE _id = $1`, childID).
		Scan(&kept); err != nil {
		t.Fatalf("read child description after re-bind: %v", err)
	}
	if kept != "Mine: only the tenant suite." {
		t.Fatalf("hand-written description was overwritten: %q", kept)
	}
}
