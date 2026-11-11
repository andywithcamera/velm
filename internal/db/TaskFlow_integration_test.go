package db

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// seedSecondActor creates a user and group for the human side of a flow, so a
// test can prove a gate lands on a different person from the producer.
func seedSecondActor(ctx context.Context, t *testing.T, label string) (string, string) {
	t.Helper()
	suffix := fmt.Sprintf("%s-%s-%d", t.Name(), label, time.Now().UnixNano())
	var userID, groupID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email, password_hash)
		VALUES ($1, $2, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
		RETURNING _id::text`, "gatekeeper", suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("seed second user: %v", err)
	}
	if err := Pool.QueryRow(ctx, `INSERT INTO _group (name) VALUES ($1) RETURNING _id::text`, "GK "+suffix).Scan(&groupID); err != nil {
		t.Fatalf("seed second group: %v", err)
	}
	if _, err := Pool.Exec(ctx, `INSERT INTO _group_membership (group_id, user_id) VALUES ($1, $2)`, groupID, userID); err != nil {
		t.Fatalf("seed second membership: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = Pool.Exec(bg, `DELETE FROM _group_membership WHERE group_id = $1`, groupID)
		_, _ = Pool.Exec(bg, `DELETE FROM _group WHERE _id = $1`, groupID)
		_, _ = Pool.Exec(bg, `DELETE FROM _user WHERE _id = $1`, userID)
	})
	return userID, groupID
}

// TestSequentialFlowThroughGate is the 090 acceptance test: a request opens
// its own chain, each task lands on a named person, a rejection returns the
// work to its author, and approval closes the whole tree.
//
// It mirrors the live Feature Request shape — request container, stage
// container, producer leaf, human gate — without depending on live data.
func TestSequentialFlowThroughGate(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	machineID, machineGroup := seedActorAndGroup(ctx, t)
	humanID, humanGroup := seedSecondActor(ctx, t, "human")

	gateSlug := "flow-gate-" + suffix
	producerSlug := "flow-producer-" + suffix

	// Producer: a script task that publishes a version, and hands off to the
	// gate on completion.
	producerDef := seedDefinition(ctx, t, producerSlug, "Write Requirement", false, "")
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition
		SET default_group_id = $2, default_assignee_id = $3,
			variables_yaml = $4,
			script_text = $5,
			spawn_rules = $6::jsonb
		WHERE _id = $1`,
		producerDef, machineGroup, machineID,
		"inputs: []\nlocals: []\noutputs:\n  - artifact_version",
		"function run(ctx) { return { outputs: { artifact_version: 3 } }; }",
		`[{"on":"completed","definition_slug":"`+gateSlug+`","assign":"definition",
		   "input_mapping":{"subject_task_id":"$task.id","producer_slug":"$task.definition_slug",
		                    "revision":"$outputs.artifact_version"}}]`); err != nil {
		t.Fatalf("configure producer: %v", err)
	}

	// Gate: a human leaf that rejects back to whatever produced the artefact.
	gateDef := seedDefinition(ctx, t, gateSlug, "Business Approval", false, "")
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition
		SET default_group_id = $2, default_assignee_id = $3, spawn_rules = $4::jsonb,
			variables_yaml = $5
		WHERE _id = $1`,
		gateDef, humanGroup, humanID,
		`[{"on":"rejected","definition_slug":"$inputs.producer_slug","assign":"definition",
		   "input_mapping":{"feedback":"$outputs.comment","revision":"$inputs.revision"}}]`,
		"inputs: []\nlocals: []\noutputs:\n  - verdict\n  - comment"); err != nil {
		t.Fatalf("configure gate: %v", err)
	}

	stageDef := seedDefinition(ctx, t, "flow-stage-"+suffix, "Stage: Requirements", true, "")
	seedChildLink(ctx, t, stageDef, producerDef, 10, map[string]any{"request_title": "$inputs.request_title"})
	rootDef := seedDefinition(ctx, t, "flow-root-"+suffix, "Feature Request", true, "")
	seedChildLink(ctx, t, rootDef, stageDef, 10, map[string]any{"request_title": "$inputs.title"})

	// One action: the requester opens the request. Nothing is started by hand.
	rootID, err := SpawnTaskInstance(ctx, Pool, rootDef, "", humanID, map[string]any{"title": "Tenant export"})
	if err != nil {
		t.Fatalf("spawn request: %v", err)
	}

	stageID := childByDef(ctx, t, rootID, stageDef)
	if got := taskState(ctx, t, stageID); got != taskStateReady {
		t.Fatalf("stage state = %s, want ready (the request opens its own chain)", got)
	}
	producerTaskID := childByDef(ctx, t, stageID, producerDef)
	if got := taskState(ctx, t, producerTaskID); got != taskStateReady {
		t.Fatalf("producer state = %s, want ready without a wrapper click", got)
	}

	// Routing: the producer landed on the machine that does the work.
	var group, assignee string
	if err := Pool.QueryRow(ctx,
		`SELECT COALESCE(assignment_group_id::text,''), COALESCE(assigned_user_id::text,'') FROM base_task WHERE _id = $1`,
		producerTaskID).Scan(&group, &assignee); err != nil {
		t.Fatalf("read routing: %v", err)
	}
	if assignee != machineID {
		t.Fatalf("producer assignee = %q, want the definition's default %q", assignee, machineID)
	}
	if group != machineGroup {
		t.Fatalf("producer group = %q, want %q", group, machineGroup)
	}

	// The machine does the work and closes it: the gate appears, for the human.
	if err := CloseTaskWithReason(ctx, producerTaskID, machineID, "completed"); err != nil {
		t.Fatalf("close producer: %v", err)
	}
	gateTaskID := childByDef(ctx, t, stageID, gateDef)
	if got := taskState(ctx, t, gateTaskID); got != taskStateReady {
		t.Fatalf("gate state = %s, want ready after the producer closed", got)
	}
	var gateAssignee string
	if err := Pool.QueryRow(ctx, `SELECT COALESCE(assigned_user_id::text,'') FROM base_task WHERE _id = $1`, gateTaskID).
		Scan(&gateAssignee); err != nil {
		t.Fatalf("read gate assignee: %v", err)
	}
	if gateAssignee != humanID {
		t.Fatalf("gate assignee = %q, want the human %q", gateAssignee, humanID)
	}
	gateInputs, _ := taskVars(ctx, t, gateTaskID)["inputs"].(map[string]any)
	if gateInputs["subject_task_id"] != producerTaskID {
		t.Fatalf("gate did not record the task it judges: %v", gateInputs)
	}
	if gateInputs["producer_slug"] != producerSlug {
		t.Fatalf("gate did not record its producer: %v", gateInputs)
	}
	// The producer's script output reached the gate: script outputs are
	// published on in_progress and $outputs resolves against the closing
	// task when a spawn rule fires.
	if got, _ := json.Marshal(gateInputs["revision"]); string(got) != "3" {
		t.Fatalf("gate revision = %v, want the producer's published 3", gateInputs["revision"])
	}

	// The human rejects: the work comes back to the machine, ready, with the
	// verdict as feedback.
	if err := CloseTaskWithReason(ctx, gateTaskID, humanID, "rejected"); err != nil {
		t.Fatalf("reject at gate: %v", err)
	}
	producers := childrenOfDef(ctx, t, stageID, producerDef)
	if len(producers) != 2 {
		t.Fatalf("rejection did not re-spawn the producer: %d instances", len(producers))
	}
	revisionID := producers[1]
	if got := taskState(ctx, t, revisionID); got != taskStateReady {
		t.Fatalf("revision state = %s, want ready", got)
	}
	var revisionAssignee string
	if err := Pool.QueryRow(ctx, `SELECT COALESCE(assigned_user_id::text,'') FROM base_task WHERE _id = $1`, revisionID).
		Scan(&revisionAssignee); err != nil {
		t.Fatalf("read revision assignee: %v", err)
	}
	if revisionAssignee != machineID {
		t.Fatalf("revision assignee = %q, want the producer definition's default %q", revisionAssignee, machineID)
	}

	// The revision completes and a fresh gate opens; approving it closes the
	// stage and then the request.
	if err := CloseTaskWithReason(ctx, revisionID, machineID, "completed"); err != nil {
		t.Fatalf("close revision: %v", err)
	}
	gates := childrenOfDef(ctx, t, stageID, gateDef)
	if len(gates) != 2 {
		t.Fatalf("second gate not spawned after the revision: %d gates", len(gates))
	}
	if err := CloseTaskWithReason(ctx, gates[1], humanID, "completed"); err != nil {
		t.Fatalf("approve at gate: %v", err)
	}

	if got := taskState(ctx, t, stageID); got != taskStateDone {
		t.Fatalf("stage state = %s, want closed once every child is terminal", got)
	}
	if got := taskState(ctx, t, rootID); got != taskStateDone {
		t.Fatalf("request state = %s, want closed", got)
	}
	// The rejected attempt outranks success: a pipeline that needed a
	// revision does not report as a clean completion.
	var reason string
	if err := Pool.QueryRow(ctx, `SELECT COALESCE(closure_reason,'') FROM base_task WHERE _id = $1`, rootID).
		Scan(&reason); err != nil {
		t.Fatalf("read request reason: %v", err)
	}
	if reason != "rejected" {
		t.Fatalf("request closure reason = %q, want rejected (worst child wins)", reason)
	}
}

// TestContainerOutputsBubble covers the stage hand-off: a child's output
// answers the container's declared output, and is published to the container's
// own parent under the container's slug.
func TestContainerOutputsBubble(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, groupID := seedActorAndGroup(ctx, t)

	leafDef := seedDefinition(ctx, t, "bubble-leaf-"+suffix, "Leaf", false, "")
	if _, err := Pool.Exec(ctx, `UPDATE base_task_definition SET script_text = $2 WHERE _id = $1`,
		leafDef, "function run(ctx) { return { outputs: { design_version: 7 } }; }"); err != nil {
		t.Fatalf("set leaf script: %v", err)
	}
	stageDef := seedDefinition(ctx, t, "bubble-stage-"+suffix, "Stage", true, "")
	if _, err := Pool.Exec(ctx, `UPDATE base_task_definition SET variables_yaml = $2 WHERE _id = $1`,
		stageDef, "inputs: []\nlocals: []\noutputs:\n  - design_version"); err != nil {
		t.Fatalf("set stage outputs: %v", err)
	}
	seedChildLink(ctx, t, stageDef, leafDef, 10, map[string]any{})
	rootDef := seedDefinition(ctx, t, "bubble-root-"+suffix, "Root", true, "")
	seedChildLink(ctx, t, rootDef, stageDef, 10, map[string]any{})

	rootID, err := SpawnTaskInstance(ctx, Pool, rootDef, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("spawn root: %v", err)
	}
	stageID := childByDef(ctx, t, rootID, stageDef)
	leafID := childByDef(ctx, t, stageID, leafDef)

	if _, err := Pool.Exec(ctx, `UPDATE base_task SET assignment_group_id = $2, assigned_user_id = $3 WHERE _id = $1`,
		leafID, groupID, actorID); err != nil {
		t.Fatalf("assign leaf: %v", err)
	}
	if err := CloseTaskWithReason(ctx, leafID, actorID, "completed"); err != nil {
		t.Fatalf("close leaf: %v", err)
	}

	stageOutputs, _ := taskVars(ctx, t, stageID)["outputs"].(map[string]any)
	version, _ := json.Marshal(stageOutputs["design_version"])
	if string(version) != "7" {
		t.Fatalf("stage declared output not answered by its child: %v", stageOutputs)
	}

	if got := taskState(ctx, t, stageID); got != taskStateDone {
		t.Fatalf("stage state = %s, want closed", got)
	}
	rootOutputs, _ := taskVars(ctx, t, rootID)["outputs"].(map[string]any)
	stageScope, _ := rootOutputs["bubble-stage-"+suffix].(map[string]any)
	rootVersion, _ := json.Marshal(stageScope["design_version"])
	if string(rootVersion) != "7" {
		t.Fatalf("stage outputs not published to the request scope: %v", rootOutputs)
	}
}
