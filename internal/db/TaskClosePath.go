package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The form sets base_task.state (and closure_reason) directly, which used to
// bypass the engine entirely: outputs never published, dependants never
// promoted, spawn rules never fired, containers never rolled up. Issue #233
// fixed that for start; this file is the close-path mirror (#264, #265).
//
// A close from any surface — form save, board drag, API — reaches the engine
// through RunEngineHooksAfterClose.

// RunEngineHooksAfterClose runs the post-close half of the engine for a task
// that has already been written as closed by the caller.
func RunEngineHooksAfterClose(ctx context.Context, taskID, actorUserID, closureReason string) error {
	var defID *string
	if err := Pool.QueryRow(ctx, `SELECT definition_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&defID); err != nil {
		return err
	}
	reason := normalizeClosureReason(closureReason)

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Hand-made tasks have no definition but can still sit inside a
	// container, so only the publish step is definition-gated (it already
	// no-ops without one); promotion and roll-up always run.
	if err := onTaskClosed(ctx, tx, taskID, reason, actorUserID); err != nil {
		return err
	}
	if err := rollUpAncestors(ctx, tx, taskID, actorUserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// normalizeClosureReason maps an empty or unknown reason to completed, so a
// form close that omits the field behaves like the engine's own close.
func normalizeClosureReason(reason string) string {
	switch reason {
	case "completed", "cancelled", "rejected", "failed":
		return reason
	default:
		return "completed"
	}
}

// containerRollUpReason decides a container's closure reason from its
// children, once every child is terminal. A rejected child outranks failed,
// which outranks success: the parent inherits the worst outcome, so a
// pipeline that hit a rejection does not report completed.
func containerRollUpReason(children []childInstanceState) (string, bool) {
	if len(children) == 0 {
		return "", false
	}
	anyRejected, anyFailed, allSkipped := false, false, true
	for _, child := range children {
		if !isTerminalTaskState(child.state) {
			return "", false
		}
		if child.state != taskStateSkipped {
			allSkipped = false
		}
		if child.state == taskStateDone && child.reason != nil {
			switch *child.reason {
			case "rejected":
				anyRejected = true
			case "failed":
				anyFailed = true
			}
		}
	}
	switch {
	case allSkipped:
		return "cancelled", true
	case anyRejected:
		return "rejected", true
	case anyFailed:
		return "failed", true
	default:
		return "completed", true
	}
}

// isTerminalTaskState reports whether a state ends a task's life.
func isTerminalTaskState(state string) bool {
	return state == taskStateDone || state == taskStateSkipped
}

// rollUpAncestors walks from a task to its root, closing every ancestor
// container whose children are all terminal. Each roll-up runs the parent's
// own close hooks, so outputs publish and its siblings re-evaluate, which is
// what lets a whole pipeline collapse after its last stage.
func rollUpAncestors(ctx context.Context, tx pgx.Tx, taskID, actorUserID string) error {
	current := taskID
	for depth := 0; depth < 32; depth++ {
		parentID, state, err := loadParent(ctx, tx, current)
		if err != nil {
			return err
		}
		if parentID == "" {
			return nil
		}
		if isTerminalTaskState(state) {
			// Already closed: keep walking so a stale parent cannot block
			// the roll-up of its own parent.
			current = parentID
			continue
		}
		children, err := loadChildStates(ctx, tx, parentID)
		if err != nil {
			return err
		}
		reason, complete := containerRollUpReason(children)
		if !complete {
			return nil
		}
		if !allowedTaskTransition(state, taskStateDone) {
			return fmt.Errorf("container %s cannot close from state %s", parentID, state)
		}
		if _, err := tx.Exec(ctx, `UPDATE base_task SET state = $2, closure_reason = $3 WHERE _id = $1`,
			parentID, taskStateDone, reason); err != nil {
			return err
		}
		if err := onTaskClosed(ctx, tx, parentID, reason, actorUserID); err != nil {
			return err
		}
		current = parentID
	}
	return nil
}

// loadParent returns a task's parent id and that parent's state.
func loadParent(ctx context.Context, tx pgx.Tx, taskID string) (string, string, error) {
	var parentID *string
	var state string
	err := tx.QueryRow(ctx, `
		SELECT p._id::text, p.state
		FROM base_task c
		JOIN base_task p ON p._id = c.parent_task_id AND p._deleted_at IS NULL
		WHERE c._id = $1`, taskID).Scan(&parentID, &state)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", "", nil
		}
		return "", "", err
	}
	if parentID == nil {
		return "", "", nil
	}
	return *parentID, state, nil
}

// loadChildStates reads a parent's live children, reusing the shape the
// promotion path already works with.
func loadChildStates(ctx context.Context, tx pgx.Tx, parentTaskID string) ([]childInstanceState, error) {
	rows, err := tx.Query(ctx, `
		SELECT _id::text, COALESCE(definition_id::text, ''), state, closure_reason
		FROM base_task
		WHERE parent_task_id = $1 AND _deleted_at IS NULL`, parentTaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var children []childInstanceState
	for rows.Next() {
		var child childInstanceState
		if err := rows.Scan(&child.taskID, &child.defID, &child.state, &child.reason); err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, rows.Err()
}
