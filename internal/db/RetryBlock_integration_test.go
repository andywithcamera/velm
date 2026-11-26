package db

// Slice 5 of the designer rework: the `retry` control block's runtime
// (Design Vision D17; Requirements AC-84 to AC-91). Every test seeds a tree
// with raw SQL, spawns it through the engine and drives the attempt loop by
// closing the scope's children, exactly as a person doing the work would.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// retryChild is one run row inside a retry block's scope.
type retryChild struct {
	ID      string
	Title   string
	State   string
	Reason  string
	Attempt int
}

func retryChildren(t *testing.T, ctx context.Context, retryTaskID string) []retryChild {
	t.Helper()
	rows, err := Pool.Query(ctx, `
		SELECT t._id::text, t.title, t.state, COALESCE(t.closure_reason, ''),
			COALESCE(attempt.value_number, 0)::int
		FROM base_task t
		LEFT JOIN _task_variables attempt
			ON attempt.task_id = t._id AND attempt.scope = 'top' AND attempt.name = 'attempt_no'
		WHERE t.parent_task_id = $1 AND t._deleted_at IS NULL
		  AND COALESCE(t.work_type, 'TASK') <> 'CONTROL'
		ORDER BY t._created_at, t.number`, retryTaskID)
	if err != nil {
		t.Fatalf("read retry children: %v", err)
	}
	defer rows.Close()
	var out []retryChild
	for rows.Next() {
		var c retryChild
		if err := rows.Scan(&c.ID, &c.Title, &c.State, &c.Reason, &c.Attempt); err != nil {
			t.Fatalf("scan retry child: %v", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read retry children: %v", err)
	}
	return out
}

// retryTaskID is the CONTROL run row of the definition's retry block.
func retryTaskID(t *testing.T, ctx context.Context, rootID string) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		SELECT t._id::text FROM base_task t
		JOIN base_task_variables v ON v.task_id = t._id
		WHERE t.parent_task_id = $1 AND v.variables->'block'->>'type' = 'retry'`,
		rootID).Scan(&id); err != nil {
		t.Fatalf("read retry control task: %v", err)
	}
	return id
}

// seedRetryTree builds trigger, retry, then one leaf per label inside the
// retry's scope, plus optional root-level siblings after the retry. The retry
// is seeded at root with raw SQL: the root refusal is a save-time rule
// (AC-83), and the runtime must still execute a row written by SQL.
func seedRetryTree(t *testing.T, ctx context.Context, slug string, config map[string]any, leafLabels []string, rootSiblings []string) string {
	t.Helper()
	container := controlTestDefinition(t, ctx, slug, true)
	controlTestBlock(t, ctx, container, "", "", "trigger", "", "Ordered", 10, nil, nil)
	retryID := controlTestBlock(t, ctx, container, "", "", "control", "retry", "Flaky", 20, config, nil)
	for index, label := range leafLabels {
		leaf := controlSeedLeaf(t, ctx, fmt.Sprintf("%s-%s", slug, strings.ToLower(label)))
		controlTestBlock(t, ctx, container, retryID, leaf, "task", "", label, (index+1)*10, nil, nil)
	}
	for index, label := range rootSiblings {
		leaf := controlSeedLeaf(t, ctx, fmt.Sprintf("%s-%s", slug, strings.ToLower(label)))
		controlTestBlock(t, ctx, container, "", leaf, "task", "", label, 30+index*10, nil, nil)
	}
	return container
}

func failRetryChild(t *testing.T, ctx context.Context, childID string) {
	t.Helper()
	if err := CloseTaskWithReason(ctx, childID, "", "failed"); err != nil {
		t.Fatalf("close %s as failed: %v", childID, err)
	}
}

func completeRetryChild(t *testing.T, ctx context.Context, childID string) {
	t.Helper()
	if err := CloseTaskWithReason(ctx, childID, "", "completed"); err != nil {
		t.Fatalf("close %s as completed: %v", childID, err)
	}
}

func attemptRows(t *testing.T, ctx context.Context, retryID string) int {
	t.Helper()
	var count int
	if err := Pool.QueryRow(ctx, `
		SELECT count(*) FROM base_task_block_attempt WHERE block_task_id = $1`,
		retryID).Scan(&count); err != nil {
		t.Fatalf("count attempt rows: %v", err)
	}
	return count
}

func retryOutputs(t *testing.T, vars map[string]any) map[string]any {
	t.Helper()
	outputs, _ := vars["outputs"].(map[string]any)
	return outputs
}

// AC-86: every attempt writes exactly one row, with a start time, a duration
// and the error that ended it.
func TestRetryRecordsEveryAttempt(t *testing.T) {
	ctx := schemaTestDB(t)
	container := seedRetryTree(t, ctx, "retry-attempts", map[string]any{"attempts": 3}, []string{"A"}, nil)
	rootID := controlSpawn(t, ctx, container, nil)
	retryID := retryTaskID(t, ctx, rootID)

	for attempt := 1; attempt <= 3; attempt++ {
		children := retryChildren(t, ctx, retryID)
		if len(children) != 1 {
			t.Fatalf("attempt %d has %d children, want 1", attempt, len(children))
		}
		failRetryChild(t, ctx, children[0].ID)
	}

	if got := attemptRows(t, ctx, retryID); got != 3 {
		t.Fatalf("attempt rows = %d, want 3", got)
	}
	rows, err := Pool.Query(ctx, `
		SELECT attempt_no, duration_ms, error FROM base_task_block_attempt
		WHERE block_task_id = $1 ORDER BY attempt_no`, retryID)
	if err != nil {
		t.Fatalf("read attempt rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var no int
		var duration *int
		var cause *string
		if err := rows.Scan(&no, &duration, &cause); err != nil {
			t.Fatalf("scan attempt row: %v", err)
		}
		if duration == nil {
			t.Errorf("attempt %d has no duration_ms", no)
		}
		if cause == nil || strings.TrimSpace(*cause) == "" {
			t.Errorf("attempt %d has no error", no)
		}
	}
}

// AC-84: the attempt stops at its first failure; no later child of that
// attempt remains in the run.
func TestRetryStopsAttemptAtFirstFailure(t *testing.T) {
	ctx := schemaTestDB(t)
	container := seedRetryTree(t, ctx, "retry-first-failure", map[string]any{"attempts": 1}, []string{"A", "B"}, nil)
	rootID := controlSpawn(t, ctx, container, nil)
	retryID := retryTaskID(t, ctx, rootID)

	children := retryChildren(t, ctx, retryID)
	if len(children) != 2 {
		t.Fatalf("attempt has %d children, want 2", len(children))
	}
	failRetryChild(t, ctx, children[0].ID)

	after := retryChildren(t, ctx, retryID)
	if len(after) != 1 {
		t.Fatalf("after the first child failed the attempt has %d children, want 1", len(after))
	}
	if !strings.Contains(after[0].Title, "retry-first-failure-a") {
		t.Fatalf("the surviving child is %q, want the failing child A", after[0].Title)
	}
}

// AC-85: the next attempt starts from the first child again, in document
// order, not from the child that failed.
func TestRetryRestartsFromFirstChild(t *testing.T) {
	ctx := schemaTestDB(t)
	container := seedRetryTree(t, ctx, "retry-restart", map[string]any{"attempts": 2}, []string{"A", "B"}, nil)
	rootID := controlSpawn(t, ctx, container, nil)
	retryID := retryTaskID(t, ctx, rootID)

	children := retryChildren(t, ctx, retryID)
	failRetryChild(t, ctx, children[0].ID)

	second := retryChildren(t, ctx, retryID)
	if len(second) != 2 {
		t.Fatalf("attempt 2 has %d children, want 2 (the scope from its first block)", len(second))
	}
	for _, child := range second {
		if child.Attempt != 2 {
			t.Errorf("child %q carries attempt %d, want 2", child.Title, child.Attempt)
		}
	}
	if !strings.Contains(second[0].Title, "retry-restart-a") || !strings.Contains(second[1].Title, "retry-restart-b") {
		t.Fatalf("attempt 2 order = [%s, %s], want [A, B]", second[0].Title, second[1].Title)
	}
}

// AC-87 and AC-90: a block that fails twice and succeeds on the third attempt
// leaves three attempt rows, the first two carrying an error, and reports
// attempts_used equal to the row count.
func TestRetryFlakyBlockSucceedsOnThirdAttempt(t *testing.T) {
	ctx := schemaTestDB(t)
	container := seedRetryTree(t, ctx, "retry-flaky", map[string]any{"attempts": 3}, []string{"A", "B"}, nil)
	rootID := controlSpawn(t, ctx, container, nil)
	retryID := retryTaskID(t, ctx, rootID)

	for attempt := 1; attempt <= 2; attempt++ {
		children := retryChildren(t, ctx, retryID)
		failRetryChild(t, ctx, children[0].ID)
	}
	children := retryChildren(t, ctx, retryID)
	if len(children) != 2 {
		t.Fatalf("third attempt has %d children, want 2", len(children))
	}
	completeRetryChild(t, ctx, children[0].ID)

	// The second child of the successful attempt must run too: the attempt is
	// a unit, and it only closes when every child of it has finished.
	afterFirst := retryChildren(t, ctx, retryID)
	if len(afterFirst) != 2 {
		t.Fatalf("after the first child completed the attempt has %d children, want 2", len(afterFirst))
	}
	if afterFirst[1].State == taskStatePending {
		t.Fatalf("the second child is still pending after the first completed; the attempt cannot finish")
	}
	completeRetryChild(t, ctx, afterFirst[1].ID)

	if got := attemptRows(t, ctx, retryID); got != 3 {
		t.Fatalf("attempt rows = %d, want 3", got)
	}
	rows, err := Pool.Query(ctx, `
		SELECT attempt_no, error FROM base_task_block_attempt
		WHERE block_task_id = $1 ORDER BY attempt_no`, retryID)
	if err != nil {
		t.Fatalf("read attempt rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var no int
		var cause *string
		if err := rows.Scan(&no, &cause); err != nil {
			t.Fatalf("scan attempt row: %v", err)
		}
		if no < 3 && (cause == nil || strings.TrimSpace(*cause) == "") {
			t.Errorf("attempt %d carries no error, want the failure that ended it", no)
		}
		if no == 3 && cause != nil {
			t.Errorf("the successful attempt carries error %q, want NULL", *cause)
		}
	}
	if state := taskState(ctx, t, retryID); state != taskStateDone {
		t.Fatalf("retry block after a successful third attempt = %q, want closed", state)
	}
	outputs := retryOutputs(t, taskVars(ctx, t, retryID))
	if got, _ := outputs["attempts_used"].(float64); int(got) != attemptRows(t, ctx, retryID) {
		t.Errorf("attempts_used = %v, want the attempt row count %d", outputs["attempts_used"], attemptRows(t, ctx, retryID))
	}
}

// AC-88: with on_exhausted "fail" the retry block does not close, the parent
// fails and the message names the block.
func TestRetryExhaustedFails(t *testing.T) {
	ctx := schemaTestDB(t)
	container := seedRetryTree(t, ctx, "retry-exhausted-fail", map[string]any{"attempts": 2, "on_exhausted": "fail"}, []string{"A"}, nil)
	rootID := controlSpawn(t, ctx, container, nil)
	retryID := retryTaskID(t, ctx, rootID)

	for attempt := 1; attempt <= 2; attempt++ {
		children := retryChildren(t, ctx, retryID)
		failRetryChild(t, ctx, children[0].ID)
	}

	if state := taskState(ctx, t, retryID); state == taskStateDone {
		t.Fatalf("the retry block closed after exhausting its attempts; it did not succeed")
	}
	var reason string
	if err := Pool.QueryRow(ctx, `SELECT COALESCE(closure_reason, '') FROM base_task WHERE _id = $1`, rootID).Scan(&reason); err != nil {
		t.Fatalf("read run closure reason: %v", err)
	}
	if reason != "failed" {
		t.Fatalf("run closure reason = %q, want failed", reason)
	}
	vars := taskVars(ctx, t, retryID)
	block, _ := vars["block"].(map[string]any)
	message, _ := block["error"].(string)
	if !strings.Contains(message, "Flaky") {
		t.Fatalf("failure message %q does not name the retry block", message)
	}
	if !strings.Contains(message, "exhausted") {
		t.Errorf("failure message %q does not say the attempts were exhausted", message)
	}
	outputs := retryOutputs(t, vars)
	if got, _ := outputs["attempts_used"].(float64); int(got) != attemptRows(t, ctx, retryID) {
		t.Errorf("attempts_used = %v, want %d", outputs["attempts_used"], attemptRows(t, ctx, retryID))
	}
}

// AC-89: with on_exhausted "continue" the parent carries on, the next sibling
// spawns, and outputs.last_error holds the final error.
func TestRetryExhaustedContinues(t *testing.T) {
	ctx := schemaTestDB(t)
	container := seedRetryTree(t, ctx, "retry-exhausted-continue",
		map[string]any{"attempts": 1, "on_exhausted": "continue"}, []string{"A"}, []string{"Next"})
	rootID := controlSpawn(t, ctx, container, nil)
	retryID := retryTaskID(t, ctx, rootID)

	children := retryChildren(t, ctx, retryID)
	failRetryChild(t, ctx, children[0].ID)

	if state := taskState(ctx, t, retryID); state != taskStateDone {
		t.Fatalf("retry block with on_exhausted continue = %q, want closed", state)
	}
	outputs := retryOutputs(t, taskVars(ctx, t, retryID))
	if lastError, _ := outputs["last_error"].(string); strings.TrimSpace(lastError) == "" {
		t.Errorf("outputs.last_error is empty, want the final attempt's error")
	}

	var nextCount int
	if err := Pool.QueryRow(ctx, `
		SELECT count(*) FROM base_task
		WHERE parent_task_id = $1 AND lower(title) LIKE '%next%' AND _deleted_at IS NULL`,
		rootID).Scan(&nextCount); err != nil {
		t.Fatalf("count the following sibling: %v", err)
	}
	if nextCount != 1 {
		t.Fatalf("the following sibling spawned %d times, want 1: the run must continue", nextCount)
	}
}

// AC-91: the wall-clock ceiling ends the loop with attempts remaining and
// fails the block with the documented message. The ceiling is injected, so
// the test does not wait half an hour.
func TestRetryWallClockCeiling(t *testing.T) {
	previous := retryWallClockCeiling
	retryWallClockCeiling = 50 * time.Millisecond
	t.Cleanup(func() { retryWallClockCeiling = previous })

	ctx := schemaTestDB(t)
	container := seedRetryTree(t, ctx, "retry-ceiling", map[string]any{"attempts": 10}, []string{"A"}, nil)
	rootID := controlSpawn(t, ctx, container, nil)
	retryID := retryTaskID(t, ctx, rootID)

	children := retryChildren(t, ctx, retryID)
	failRetryChild(t, ctx, children[0].ID)

	// Let the injected ceiling pass in wall-clock terms, then fail the next
	// attempt: the guard must end the loop rather than start attempt 3.
	time.Sleep(60 * time.Millisecond)
	children = retryChildren(t, ctx, retryID)
	failRetryChild(t, ctx, children[0].ID)

	if got, want := attemptRows(t, ctx, retryID), 10; got >= want {
		t.Fatalf("attempt rows = %d; the ceiling must end the loop before the %d configured attempts", got, want)
	}
	vars := taskVars(ctx, t, retryID)
	block, _ := vars["block"].(map[string]any)
	message, _ := block["error"].(string)
	if !strings.Contains(message, "retry exceeded its 1800s wall-clock ceiling") {
		t.Fatalf("failure message %q does not report the ceiling", message)
	}
	if state := taskState(ctx, t, retryID); state == taskStateDone {
		t.Fatalf("the retry block closed success on a ceiling failure")
	}
}
