package db

// The `retry` control block (Design Vision D17; Requirements slice 5,
// AC-75 to AC-92).
//
// A retry block re-runs its scope as a unit: attempt 1 runs every child from
// the first, and if one of them fails the block starts a new attempt from the
// first child again, up to `attempts` (1-10). Every attempt writes one row to
// base_task_block_attempt, so the run says how many attempts it took and why
// the failed ones failed.
//
// Two bounds end the loop: `attempts`, and a wall-clock ceiling that lives
// here in the runtime and is never read from block config (D17). The ceiling
// is a package variable so the test can inject 50 ms instead of waiting half
// an hour; production never writes it.
//
// This file owns the attempt lifecycle only. The block's own save-time rules
// (bounds, vocabulary, nesting) are in cmd/server, where every other refusal
// is written.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// retryMaxAttempts is the documented bound: attempts outside 1-10 are
// refused at save, and the runtime never starts an eleventh.
const retryMaxAttempts = 10

// retryWallClockCeiling bounds the whole loop, not one attempt (D17). It is
// a variable so a test can shorten it; nothing in a definition can change it.
var retryWallClockCeiling = 1800 * time.Second

// retryCeilingMessage is the text AC-91 asserts. It names the ceiling, not
// the injected test value: the message an author sees must say what the real
// limit is.
const retryCeilingMessage = "retry exceeded its 1800s wall-clock ceiling"

// retryBlockConfig reads the retry block's settings with the documented
// defaults: one attempt, no delay, no backoff, fail on exhaustion.
func retryAttemptCount(b DefinitionBlock) int {
	n := 1
	if raw, present := b.Config["attempts"]; present && raw != nil {
		if value, ok := numberValue(raw); ok {
			n = int(value)
		}
	}
	if n < 1 {
		n = 1
	}
	if n > retryMaxAttempts {
		n = retryMaxAttempts
	}
	return n
}

// retryOnExhausted is "fail" unless the block says "continue"; the save gate
// refuses every other word, so this is a default, not a second validation.
func retryOnExhausted(b DefinitionBlock) string {
	if b.blockConfigString("on_exhausted") == "continue" {
		return "continue"
	}
	return "fail"
}

// loadRetryBlock reads the DefinitionBlock behind a retry CONTROL run row.
// It answers ok=false for a task that is not a retry block, which is how the
// close path decides whether it has any business here.
func loadRetryBlock(ctx context.Context, q scriptQuerier, taskID string) (DefinitionBlock, string, bool, error) {
	vars, err := readTaskVariables(ctx, q, taskID)
	if err != nil {
		return DefinitionBlock{}, "", false, err
	}
	blockID, containerDefID, kind, typ, _, _, ok := blockFromVars(vars)
	if !ok || kind != "control" || typ != "retry" {
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
	// The block was edited away between spawn and execution: the run row
	// remembers it was a retry, so treat it as a bare retry rather than
	// failing the whole close.
	return DefinitionBlock{ID: blockID, Kind: "control", Type: "retry"}, containerDefID, true, nil
}

// retryAttemptsUsed counts the attempt rows for one retry block: AC-90's
// attempts_used is this number, read from the table, never a counter.
func retryAttemptsUsed(ctx context.Context, q scriptQuerier, retryTaskID string) (int, error) {
	var used int
	err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM base_task_block_attempt WHERE block_task_id = $1`,
		retryTaskID).Scan(&used)
	return used, err
}

// startRetryAttempt opens one attempt: record it, create the scope's children
// afresh from the first, stamp them with the attempt number so a later
// failure can drop the ones that never ran, and promote them.
func startRetryAttempt(ctx context.Context, q scriptQuerier, retryTaskID string, b DefinitionBlock, containerDefID, actorUserID string, runInputs, scope map[string]any, depth int) error {
	used, err := retryAttemptsUsed(ctx, q, retryTaskID)
	if err != nil {
		return err
	}
	attemptNo := used + 1
	if _, err := q.Exec(ctx, `
		INSERT INTO base_task_block_attempt (block_task_id, attempt_no, started_at)
		VALUES ($1, $2, now())`, retryTaskID, attemptNo); err != nil {
		return fmt.Errorf("block %q: record attempt %d: %w", b.Label, attemptNo, err)
	}
	if err := spawnDefinitionBranchScope(ctx, q, containerDefID, retryTaskID, actorUserID, b, runInputs, scope, false, ""); err != nil {
		return err
	}
	// Children of this attempt carry its number. They are the rows AC-84
	// counts, and the ones a first failure removes before they run.
	if _, err := q.Exec(ctx, `
		UPDATE base_task
		SET variables = jsonb_set(variables, '{attempt_no}', to_jsonb($2::int))
		WHERE parent_task_id = $1 AND _deleted_at IS NULL`, retryTaskID, attemptNo); err != nil {
		return err
	}
	return promoteChildrenDepth(ctx, q, retryTaskID, actorUserID, depth+1)
}

// retryChildClosed is the close-path hook. When the task that just closed is
// the child of a retry block it decides what the attempt did:
//
//   - one of its children failed: end the attempt, drop the children that have
//     not run, then start the next attempt or exhaust;
//   - every child is terminal without a failure: the attempt succeeded and the
//     retry block closes.
//
// handled is true whenever the parent is a retry block, so the caller must not
// also promote or roll up that parent: the retry block owns its own closure.
func retryChildClosed(ctx context.Context, q scriptQuerier, closedTaskID, actorUserID string) (bool, error) {
	var parentIDPtr *string
	if err := q.QueryRow(ctx, `SELECT parent_task_id::text FROM base_task WHERE _id = $1`,
		closedTaskID).Scan(&parentIDPtr); err != nil {
		return false, err
	}
	parentID := ""
	if parentIDPtr != nil {
		parentID = *parentIDPtr
	}
	if parentID == "" {
		return false, nil
	}
	b, containerDefID, ok, err := loadRetryBlock(ctx, q, parentID)
	if err != nil || !ok {
		return false, err
	}
	if err := settleRetryAttempt(ctx, q, parentID, b, containerDefID, actorUserID); err != nil {
		return true, err
	}
	return true, nil
}

// settleRetryAttempt inspects a retry block's live children and either ends
// the attempt, starts the next one, or leaves it running.
func settleRetryAttempt(ctx context.Context, q scriptQuerier, retryTaskID string, b DefinitionBlock, containerDefID, actorUserID string) error {
	type attemptChild struct {
		id     string
		state  string
		reason string
		error  string
	}
	rows, err := q.Query(ctx, `
		SELECT _id::text, state, COALESCE(closure_reason, ''),
			COALESCE(variables->'block'->>'error', '')
		FROM base_task
		WHERE parent_task_id = $1 AND _deleted_at IS NULL
		ORDER BY _created_at, number`, retryTaskID)
	if err != nil {
		return err
	}
	var children []attemptChild
	for rows.Next() {
		var c attemptChild
		if err := rows.Scan(&c.id, &c.state, &c.reason, &c.error); err != nil {
			rows.Close()
			return err
		}
		children = append(children, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	failedAt := -1
	allTerminal := len(children) > 0
	for index, child := range children {
		if !isTerminalTaskState(child.state) {
			allTerminal = false
			continue
		}
		if failedAt < 0 && child.reason == "failed" {
			failedAt = index
		}
	}

	if failedAt < 0 {
		if !allTerminal {
			return nil
		}
		return finishRetrySuccess(ctx, q, retryTaskID, b, actorUserID)
	}

	// AC-84: the attempt stops at its first failure. Anything of this attempt
	// that has not run is removed, so the board shows the attempt as it was
	// attempted rather than a row of work nothing will start.
	attemptNo := currentAttemptNo(ctx, q, retryTaskID)
	if err := dropUnrunAttemptChildren(ctx, q, retryTaskID, attemptNo); err != nil {
		return err
	}
	lastError := strings.TrimSpace(children[failedAt].error)
	if lastError == "" {
		lastError = "the block failed"
	}
	if err := finishAttemptRow(ctx, q, retryTaskID, attemptNo, lastError); err != nil {
		return err
	}

	used, err := retryAttemptsUsed(ctx, q, retryTaskID)
	if err != nil {
		return err
	}
	ceilingHit, err := retryCeilingExceeded(ctx, q, retryTaskID)
	if err != nil {
		return err
	}
	if used < retryAttemptCount(b) && !ceilingHit {
		vars, err := readTaskVariables(ctx, q, retryTaskID)
		if err != nil {
			return err
		}
		scope := retryScopeFromVars(vars)
		runInputs, _ := scope["inputs"].(map[string]any)
		return startRetryAttempt(ctx, q, retryTaskID, b, containerDefID, actorUserID, runInputs, scope, 0)
	}
	if !ceilingHit {
		return exhaustRetryBlock(ctx, q, retryTaskID, b, actorUserID, used, lastError)
	}
	return exhaustRetryBlock(ctx, q, retryTaskID, b, actorUserID, used, retryCeilingMessage)
}

// retryScopeFromVars rebuilds the scope an attempt's children run against.
// A retry block shapes the flow, so its children read the enclosing task's
// inputs and sibling results; those were copied onto the retry task when it
// was promoted.
func retryScopeFromVars(vars map[string]any) map[string]any {
	scope := map[string]any{}
	for key, value := range vars {
		scope[key] = value
	}
	scope["parent"] = vars["parent"]
	if _, ok := scope["locals"].(map[string]any); !ok {
		scope["locals"] = map[string]any{}
	}
	return scope
}

// currentAttemptNo is the attempt number of the newest attempt row.
func currentAttemptNo(ctx context.Context, q scriptQuerier, retryTaskID string) int {
	var no int
	if err := q.QueryRow(ctx, `
		SELECT COALESCE(MAX(attempt_no), 0) FROM base_task_block_attempt
		WHERE block_task_id = $1`, retryTaskID).Scan(&no); err != nil {
		return 0
	}
	return no
}

// finishAttemptRow records the duration and the reason of a failed attempt.
func finishAttemptRow(ctx context.Context, q scriptQuerier, retryTaskID string, attemptNo int, cause string) error {
	if attemptNo < 1 {
		return nil
	}
	var started time.Time
	if err := q.QueryRow(ctx, `
		SELECT started_at FROM base_task_block_attempt
		WHERE block_task_id = $1 AND attempt_no = $2`, retryTaskID, attemptNo).Scan(&started); err != nil {
		return err
	}
	duration := int(time.Since(started).Milliseconds())
	if duration < 0 {
		duration = 0
	}
	_, err := q.Exec(ctx, `
		UPDATE base_task_block_attempt
		SET duration_ms = $3, error = $4
		WHERE block_task_id = $1 AND attempt_no = $2`, retryTaskID, attemptNo, duration, cause)
	return err
}

// dropUnrunAttemptChildren hard-deletes the current attempt's children that
// have not reached a terminal state, and their subtrees. They were never
// worked on, so no history is lost; a soft delete would still count in AC-84.
func dropUnrunAttemptChildren(ctx context.Context, q scriptQuerier, retryTaskID string, attemptNo int) error {
	if attemptNo < 1 {
		return nil
	}
	rows, err := q.Query(ctx, `
		WITH RECURSIVE doomed AS (
			SELECT _id::text AS id, 0 AS depth
			FROM base_task
			WHERE parent_task_id = $1 AND _deleted_at IS NULL
			  AND state NOT IN ('closed', 'skipped')
			  AND COALESCE(variables->>'attempt_no', '') = $2::text
			UNION ALL
			SELECT c._id::text, d.depth + 1
			FROM base_task c
			JOIN doomed d ON c.parent_task_id::text = d.id
			WHERE c._deleted_at IS NULL
		)
		SELECT id, depth FROM doomed ORDER BY depth DESC`,
		retryTaskID, strconv.Itoa(attemptNo))
	if err != nil {
		return err
	}
	type doomedRow struct {
		id    string
		depth int
	}
	var doomed []doomedRow
	for rows.Next() {
		var r doomedRow
		if err := rows.Scan(&r.id, &r.depth); err != nil {
			rows.Close()
			return err
		}
		doomed = append(doomed, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	sort.Slice(doomed, func(i, j int) bool { return doomed[i].depth > doomed[j].depth })
	for _, r := range doomed {
		if _, err := q.Exec(ctx, `DELETE FROM base_task WHERE _id = $1`, r.id); err != nil {
			return err
		}
	}
	return nil
}

// retryCeilingExceeded reports whether the loop has spent longer than
// retryWallClockCeiling since its first attempt started.
func retryCeilingExceeded(ctx context.Context, q scriptQuerier, retryTaskID string) (bool, error) {
	var first *time.Time
	if err := q.QueryRow(ctx, `
		SELECT MIN(started_at) FROM base_task_block_attempt WHERE block_task_id = $1`,
		retryTaskID).Scan(&first); err != nil {
		return false, err
	}
	if first == nil {
		return false, nil
	}
	return time.Since(*first) > retryWallClockCeiling, nil
}

// writeRetryOutputs sets outputs.attempts_used and, when there is one,
// outputs.last_error on the retry block's own row.
func writeRetryOutputs(ctx context.Context, q scriptQuerier, retryTaskID string, used int, lastError string) error {
	vars, err := readTaskVariables(ctx, q, retryTaskID)
	if err != nil {
		return err
	}
	outputs, _ := vars["outputs"].(map[string]any)
	if outputs == nil {
		outputs = map[string]any{}
	}
	outputs["attempts_used"] = float64(used)
	if lastError != "" {
		outputs["last_error"] = lastError
	}
	vars["outputs"] = outputs
	raw, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`,
		retryTaskID, string(raw))
	return err
}

// finishRetrySuccess closes the retry block after an attempt whose children
// all finished without a failure.
func finishRetrySuccess(ctx context.Context, q scriptQuerier, retryTaskID string, b DefinitionBlock, actorUserID string) error {
	used, err := retryAttemptsUsed(ctx, q, retryTaskID)
	if err != nil {
		return err
	}
	if err := finishSucceededAttempt(ctx, q, retryTaskID); err != nil {
		return err
	}
	if err := writeRetryOutputs(ctx, q, retryTaskID, used, ""); err != nil {
		return err
	}
	return closeRetryBlock(ctx, q, retryTaskID, actorUserID)
}

// finishSucceededAttempt stamps duration on the newest attempt, leaving error
// NULL: AC-87's third attempt must carry no error.
func finishSucceededAttempt(ctx context.Context, q scriptQuerier, retryTaskID string) error {
	attemptNo := currentAttemptNo(ctx, q, retryTaskID)
	if attemptNo < 1 {
		return nil
	}
	var started time.Time
	if err := q.QueryRow(ctx, `
		SELECT started_at FROM base_task_block_attempt
		WHERE block_task_id = $1 AND attempt_no = $2`, retryTaskID, attemptNo).Scan(&started); err != nil {
		return err
	}
	duration := int(time.Since(started).Milliseconds())
	if duration < 0 {
		duration = 0
	}
	_, err := q.Exec(ctx, `
		UPDATE base_task_block_attempt SET duration_ms = $3, error = NULL
		WHERE block_task_id = $1 AND attempt_no = $2`, retryTaskID, attemptNo, duration)
	return err
}

// closeRetryBlock closes the retry CONTROL row as completed and lets its
// parent carry on.
func closeRetryBlock(ctx context.Context, q scriptQuerier, retryTaskID, actorUserID string) error {
	if _, err := q.Exec(ctx, `UPDATE base_task SET state = $2, closure_reason = 'completed' WHERE _id = $1`,
		retryTaskID, taskStateDone); err != nil {
		return err
	}
	if err := onTaskClosed(ctx, q, retryTaskID, "completed", actorUserID); err != nil {
		return err
	}
	return rollUpAncestors(ctx, q, retryTaskID, actorUserID)
}

// exhaustRetryBlock handles the last attempt failing.
//
// on_exhausted "continue": the block closes completed with outputs.last_error,
// and the parent's next sibling spawns through the normal close path.
//
// on_exhausted "fail": the retry block itself is left where it is — it did not
// finish — and the parent is failed with the block named, so the run cannot
// silently continue past work that never succeeded.
func exhaustRetryBlock(ctx context.Context, q scriptQuerier, retryTaskID string, b DefinitionBlock, actorUserID string, used int, lastError string) error {
	if err := writeRetryOutputs(ctx, q, retryTaskID, used, lastError); err != nil {
		return err
	}
	if retryOnExhausted(b) == "continue" {
		return closeRetryBlock(ctx, q, retryTaskID, actorUserID)
	}
	message := fmt.Sprintf("block %q: %s", blockDisplayLabel(b), lastError)
	if lastError != retryCeilingMessage {
		message = fmt.Sprintf("block %q: retry exhausted after %d attempt(s): %s",
			blockDisplayLabel(b), used, lastError)
	}
	// The block's own row records why, so the board shows the failure on the
	// block the author drew.
	vars, err := readTaskVariables(ctx, q, retryTaskID)
	if err == nil {
		block, _ := vars["block"].(map[string]any)
		if block == nil {
			block = map[string]any{}
		}
		block["error"] = message
		block["log"] = []any{message}
		vars["block"] = block
		if raw, err := json.Marshal(vars); err == nil {
			_, _ = q.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`,
				retryTaskID, string(raw))
		}
	}
	var parentIDPtr *string
	if err := q.QueryRow(ctx, `SELECT parent_task_id::text FROM base_task WHERE _id = $1`,
		retryTaskID).Scan(&parentIDPtr); err != nil {
		return err
	}
	if parentIDPtr == nil || *parentIDPtr == "" {
		return nil
	}
	parentID := *parentIDPtr
	if _, err := q.Exec(ctx, `UPDATE base_task SET state = $2, closure_reason = 'failed' WHERE _id = $1`,
		parentID, taskStateDone); err != nil {
		return err
	}
	if err := onTaskClosed(ctx, q, parentID, "failed", actorUserID); err != nil {
		return err
	}
	return rollUpAncestors(ctx, q, parentID, actorUserID)
}
