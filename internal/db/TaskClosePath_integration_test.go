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
