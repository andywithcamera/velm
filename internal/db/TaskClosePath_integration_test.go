package db

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Integration coverage for the close path: a close from a form (state set
// directly, then RunEngineHooksAfterClose) must behave like the engine's own
// close — publish outputs, promote dependants, and roll a container up once
// its last child is terminal (#264, #265, #272).

// TestSpawnComposesInstanceIdentity is the #272 acceptance test: two runs of
// the same container produce distinct, readable titles.
func TestSpawnComposesInstanceIdentity(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, _ := seedActorAndGroup(ctx, t)

	stageDef := seedDefinition(ctx, t, "id-stage-"+suffix, "Deploy", false, "")
	containerDef := seedDefinition(ctx, t, "id-container-"+suffix, "Feature Request", true, "")
	seedChildLinkWithDeps(ctx, t, containerDef, stageDef, 0, nil, "")

	runA, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("spawn run A: %v", err)
	}
	runB, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("spawn run B: %v", err)
	}

	titleA, numberA := taskTitleAndNumber(ctx, t, runA)
	titleB, _ := taskTitleAndNumber(ctx, t, runB)
	if want := "Feature Request (" + numberA + ")"; titleA != want {
		t.Fatalf("root title = %q, want %q", titleA, want)
	}
	if titleA == titleB {
		t.Fatalf("two runs share the title %q", titleA)
	}

	childA := childByDef(ctx, t, runA, stageDef)
	childTitle, childNumber := taskTitleAndNumber(ctx, t, childA)
	if want := "Feature Request · Deploy (" + childNumber + ")"; childTitle != want {
		t.Fatalf("child title = %q, want %q", childTitle, want)
	}
	// The ancestor's number must not leak into the descendant's title.
	if childTitle == titleA {
		t.Fatalf("child title duplicated the root: %q", childTitle)
	}
}

// TestFormClosePromotesDependantsAndRollsUpContainer drives the form's close
// shape: write state=closed + reason directly (as the record save does), then
// run the post-close hooks.
func TestFormClosePromotesDependantsAndRollsUpContainer(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, groupID := seedActorAndGroup(ctx, t)

	implDef := seedDefinition(ctx, t, "cp-impl-"+suffix, "Impl", false, "")
	reviewDef := seedDefinition(ctx, t, "cp-review-"+suffix, "Review", false, "")
	containerDef := seedDefinition(ctx, t, "cp-container-"+suffix, "Delivery", true, "")
	seedChildLinkWithDeps(ctx, t, containerDef, implDef, 0, nil, "")
	seedChildLinkWithDeps(ctx, t, containerDef, reviewDef, 1, []string{implDef}, "")

	parentID, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	assignAll(ctx, t, parentID, groupID, actorID)
	if err := StartTask(ctx, parentID, actorID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if got := countState(ctx, t, parentID, "pending"); got != 1 {
		t.Fatalf("review should wait on impl, %d pending", got)
	}

	implID := childByDef(ctx, t, parentID, implDef)
	formClose(ctx, t, implID, "completed")

	// Promoting the dependant is the half the form close used to skip.
	if got := countState(ctx, t, parentID, "pending"); got != 0 {
		t.Fatalf("closing impl should promote review, %d still pending", got)
	}

	reviewID := childByDef(ctx, t, parentID, reviewDef)
	if err := StartTask(ctx, reviewID, actorID); err != nil {
		t.Fatalf("start review: %v", err)
	}
	formClose(ctx, t, reviewID, "completed")

	// Last child terminal -> the container rolls up on its own.
	state, reason := taskStateAndReason(ctx, t, parentID)
	if state != taskStateDone {
		t.Fatalf("container state = %q, want %q", state, taskStateDone)
	}
	if reason != "completed" {
		t.Fatalf("container closure reason = %q, want completed", reason)
	}
}

// TestContainerRollUpInheritsRejection: a rejected child makes the container
// rejected, not completed, so a failed pipeline never reports success.
func TestContainerRollUpInheritsRejection(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, groupID := seedActorAndGroup(ctx, t)

	okDef := seedDefinition(ctx, t, "rr-ok-"+suffix, "Ok", false, "")
	rejectDef := seedDefinition(ctx, t, "rr-reject-"+suffix, "Reject", false, "")
	containerDef := seedDefinition(ctx, t, "rr-container-"+suffix, "Loop", true, "")
	seedChildLinkWithDeps(ctx, t, containerDef, okDef, 0, nil, "")
	seedChildLinkWithDeps(ctx, t, containerDef, rejectDef, 1, nil, "")

	parentID, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	assignAll(ctx, t, parentID, groupID, actorID)
	if err := StartTask(ctx, parentID, actorID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	okID := childByDef(ctx, t, parentID, okDef)
	rejectID := childByDef(ctx, t, parentID, rejectDef)
	formClose(ctx, t, okID, "completed")
	if state, _ := taskStateAndReason(ctx, t, parentID); state == taskStateDone {
		t.Fatalf("container closed with a child still open")
	}
	formClose(ctx, t, rejectID, "rejected")

	state, reason := taskStateAndReason(ctx, t, parentID)
	if state != taskStateDone || reason != "rejected" {
		t.Fatalf("container = (%q, %q), want (closed, rejected)", state, reason)
	}
}

// TestClosePublishesOutputsToParent: the form close must publish declared
// outputs, which is what $outputs references downstream depend on (#217).
func TestClosePublishesOutputsToParent(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, groupID := seedActorAndGroup(ctx, t)

	childSlug := "out-child-" + suffix
	childDef := seedDefinition(ctx, t, childSlug, "Producer", false, "")
	containerDef := seedDefinition(ctx, t, "out-container-"+suffix, "Pipeline", true, "")
	seedChildLinkWithDeps(ctx, t, containerDef, childDef, 0, nil, "")

	parentID, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	assignAll(ctx, t, parentID, groupID, actorID)
	if err := StartTask(ctx, parentID, actorID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	childID := childByDef(ctx, t, parentID, childDef)
	vars := taskVars(ctx, t, childID)
	vars["outputs"] = map[string]any{"ticket": "VELM-1"}
	setTaskVars(ctx, t, childID, vars)

	formClose(ctx, t, childID, "completed")

	parentVars := taskVars(ctx, t, parentID)
	outputs, _ := parentVars["outputs"].(map[string]any)
	// Outputs are namespaced by definition slug, not display name.
	producer, _ := outputs[childSlug].(map[string]any)
	if producer == nil || producer["ticket"] != "VELM-1" {
		t.Fatalf("child outputs not published to parent scope: %v", parentVars["outputs"])
	}
}

// formClose reproduces what the record form does on save: the state and
// closure_reason columns are written directly, then the post-close hooks run.
func formClose(ctx context.Context, t *testing.T, taskID, reason string) {
	t.Helper()
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET state = 'closed', closure_reason = $2 WHERE _id = $1`,
		taskID, reason); err != nil {
		t.Fatalf("form close %s: %v", taskID, err)
	}
	if err := RunEngineHooksAfterClose(ctx, taskID, actorIDForClose(t), reason); err != nil {
		t.Fatalf("close hooks %s: %v", taskID, err)
	}
}

func taskTitleAndNumber(ctx context.Context, t *testing.T, taskID string) (string, string) {
	t.Helper()
	var title, number string
	if err := Pool.QueryRow(ctx, `SELECT title, number FROM base_task WHERE _id = $1`, taskID).Scan(&title, &number); err != nil {
		t.Fatalf("read title %s: %v", taskID, err)
	}
	return title, number
}

func taskStateAndReason(ctx context.Context, t *testing.T, taskID string) (string, string) {
	t.Helper()
	var state string
	var reason *string
	if err := Pool.QueryRow(ctx, `SELECT state, closure_reason FROM base_task WHERE _id = $1`, taskID).Scan(&state, &reason); err != nil {
		t.Fatalf("read state %s: %v", taskID, err)
	}
	if reason == nil {
		return state, ""
	}
	return state, *reason
}

func setTaskVars(ctx context.Context, t *testing.T, taskID string, vars map[string]any) {
	t.Helper()
	vj, err := json.Marshal(vars)
	if err != nil {
		t.Fatalf("marshal vars: %v", err)
	}
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, taskID, string(vj)); err != nil {
		t.Fatalf("set vars %s: %v", taskID, err)
	}
}

// bulkClose reproduces what POST /api/bulk/update/base_task does on a close:
// a single-column UPDATE of state, leaving closure_reason to the 085 trigger,
// then the post-write hook dispatch (#274). Bulk update could close a task
// without publishing outputs, promoting dependants, or rolling up containers.
func bulkClose(ctx context.Context, t *testing.T, taskID string) {
	t.Helper()
	oldSnapshot := loadRecordSnapshot("base_task", taskID)
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET state = 'closed' WHERE _id = $1`, taskID); err != nil {
		t.Fatalf("bulk close %s: %v", taskID, err)
	}
	newSnapshot := loadRecordSnapshot("base_task", taskID)
	if err := RunEngineHooksAfterStateWrite(ctx, "base_task", taskID, oldSnapshot, newSnapshot, actorIDForClose(t)); err != nil {
		t.Fatalf("bulk close hooks %s: %v", taskID, err)
	}
}

// TestBulkCloseRunsCloseHooks is the #274 acceptance test: closing through the
// bulk endpoint's write shape must behave like a form close.
func TestBulkCloseRunsCloseHooks(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, groupID := seedActorAndGroup(ctx, t)

	childSlug := "bulk-impl-" + suffix
	implDef := seedDefinition(ctx, t, childSlug, "Impl", false, "")
	reviewDef := seedDefinition(ctx, t, "bulk-review-"+suffix, "Review", false, "")
	containerDef := seedDefinition(ctx, t, "bulk-container-"+suffix, "Delivery", true, "")
	seedChildLinkWithDeps(ctx, t, containerDef, implDef, 0, nil, "")
	seedChildLinkWithDeps(ctx, t, containerDef, reviewDef, 1, []string{implDef}, "")

	parentID, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	assignAll(ctx, t, parentID, groupID, actorID)
	if err := StartTask(ctx, parentID, actorID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	implID := childByDef(ctx, t, parentID, implDef)
	vars := taskVars(ctx, t, implID)
	vars["outputs"] = map[string]any{"ticket": "VELM-274"}
	setTaskVars(ctx, t, implID, vars)

	bulkClose(ctx, t, implID)

	// (a) outputs published into the parent's scope.
	parentVars := taskVars(ctx, t, parentID)
	outputs, _ := parentVars["outputs"].(map[string]any)
	producer, _ := outputs[childSlug].(map[string]any)
	if producer == nil || producer["ticket"] != "VELM-274" {
		t.Fatalf("bulk close did not publish child outputs: %v", parentVars["outputs"])
	}
	// (b) the dependant was promoted out of pending.
	if got := countState(ctx, t, parentID, "pending"); got != 0 {
		t.Fatalf("bulk close left %d task(s) pending; the dependant was not promoted", got)
	}
	// The trigger, not the payload, supplied the reason.
	if state, reason := taskStateAndReason(ctx, t, implID); state != taskStateDone || reason != "completed" {
		t.Fatalf("bulk-closed task = (%q, %q), want (closed, completed)", state, reason)
	}

	reviewID := childByDef(ctx, t, parentID, reviewDef)
	if err := StartTask(ctx, reviewID, actorID); err != nil {
		t.Fatalf("start review: %v", err)
	}
	bulkClose(ctx, t, reviewID)

	// (c) the container rolled up once its last child closed.
	state, reason := taskStateAndReason(ctx, t, parentID)
	if state != taskStateDone || reason != "completed" {
		t.Fatalf("container = (%q, %q), want (closed, completed)", state, reason)
	}
}

// TestBulkCloseAgainIsNoOp: duplicate ids in one batch, or a second bulk edit
// of an already-closed row, must not re-run the close engine.
func TestBulkCloseAgainIsNoOp(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, groupID := seedActorAndGroup(ctx, t)

	spawnDef := seedDefinition(ctx, t, "again-spawn-"+suffix, "Again", false, "")
	containerDef := seedDefinition(ctx, t, "again-container-"+suffix, "Once", true, "")
	seedChildLinkWithDeps(ctx, t, containerDef, spawnDef, 0, nil, "")

	parentID, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	assignAll(ctx, t, parentID, groupID, actorID)
	if err := StartTask(ctx, parentID, actorID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	childID := childByDef(ctx, t, parentID, spawnDef)
	bulkClose(ctx, t, childID)
	before := len(taskVars(ctx, t, parentID))

	// Second bulk edit of the same id: the old snapshot is already closed, so
	// the gate must short-circuit without error.
	oldSnapshot := loadRecordSnapshot("base_task", childID)
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET state = 'closed' WHERE _id = $1`, childID); err != nil {
		t.Fatalf("re-close %s: %v", childID, err)
	}
	newSnapshot := loadRecordSnapshot("base_task", childID)
	if err := RunEngineHooksAfterStateWrite(ctx, "base_task", childID, oldSnapshot, newSnapshot, actorIDForClose(t)); err != nil {
		t.Fatalf("re-close hooks should be a no-op, got %v", err)
	}
	if after := len(taskVars(ctx, t, parentID)); after != before {
		t.Fatalf("re-close changed the parent scope: %d keys, was %d", after, before)
	}
}
