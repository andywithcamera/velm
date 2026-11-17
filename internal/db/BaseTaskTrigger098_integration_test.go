package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Requirements AC-5 to AC-9: 098 widens base_task.work_type so control blocks
// can materialise as tasks, and rewrites base_task_before_write without
// dropping any invariant it has carried since 084/085.

func triggerTestGroupAndMember(t *testing.T, ctx context.Context) (groupID, memberID, outsiderID string) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _group (name, description, is_system)
		VALUES ($1, 'trigger 098 test', FALSE) RETURNING _id::text`, "trigger-"+suffix).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	newUser := func(label string) string {
		t.Helper()
		var id string
		if err := Pool.QueryRow(ctx, `
			INSERT INTO _user (name, email)
			VALUES ($1, $2) RETURNING _id::text`, label, label+"-"+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", label, err)
		}
		return id
	}
	memberID = newUser("trigger-member")
	outsiderID = newUser("trigger-outsider")
	if _, err := Pool.Exec(ctx, `
		INSERT INTO _group_membership (group_id, user_id) VALUES ($1, $2)`, groupID, memberID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = Pool.Exec(bg, `DELETE FROM base_task WHERE assignment_group_id = $1 OR assigned_user_id = ANY($2::uuid[])`,
			groupID, []string{memberID, outsiderID})
		_, _ = Pool.Exec(bg, `DELETE FROM _group_membership WHERE group_id = $1`, groupID)
		_, _ = Pool.Exec(bg, `DELETE FROM _user WHERE _id = ANY($1::uuid[])`, []string{memberID, outsiderID})
		_, _ = Pool.Exec(bg, `DELETE FROM _group WHERE _id = $1`, groupID)
	})
	return groupID, memberID, outsiderID
}

// AC-5 and AC-6: CONTROL is a legal work_type and the trigger no longer stamps
// it back to TASK on the way through.
func TestBaseTaskWorkTypeAcceptsControl(t *testing.T) {
	ctx := schemaTestDB(t)

	var controlID, inserted string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task (title, state, work_type)
		VALUES ('control work type', 'pending', 'CONTROL')
		RETURNING _id::text, work_type`).Scan(&controlID, &inserted); err != nil {
		t.Fatalf("insert CONTROL: %v (056 refused this; 098 must allow it)", err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM base_task WHERE _id = $1`, controlID)
	})
	if inserted != "CONTROL" {
		t.Fatalf("INSERT stored work_type=%q, want CONTROL", inserted)
	}

	var taskID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task (title, state, work_type)
		VALUES ('control update', 'pending', 'TASK')
		RETURNING _id::text`).Scan(&taskID); err != nil {
		t.Fatalf("insert TASK: %v", err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM base_task WHERE _id = $1`, taskID)
	})
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET work_type = 'CONTROL' WHERE _id = $1`, taskID); err != nil {
		t.Fatalf("update to CONTROL: %v", err)
	}
	var updated string
	if err := Pool.QueryRow(ctx, `SELECT work_type FROM base_task WHERE _id = $1`, taskID).Scan(&updated); err != nil {
		t.Fatalf("read back work_type: %v", err)
	}
	if updated != "CONTROL" {
		t.Fatalf("after UPDATE stored work_type=%q, want CONTROL; the trigger is still overwriting it", updated)
	}

	_, err := Pool.Exec(ctx, `
		INSERT INTO base_task (title, state, work_type)
		VALUES ('work order', 'pending', 'WORK_ORDER')`)
	if err == nil {
		t.Fatal("WORK_ORDER was accepted; chk_base_task_type must allow only TASK and CONTROL")
	}
}

// AC-7, AC-8 and AC-9: the 084/085 invariants survive the trigger rewrite.
func TestBaseTaskTriggerInvariants(t *testing.T) {
	ctx := schemaTestDB(t)
	groupID, memberID, outsiderID := triggerTestGroupAndMember(t, ctx)

	insertTask := func(title, state string, group, assignee any, reason any) (string, error) {
		t.Helper()
		var id string
		err := Pool.QueryRow(ctx, `
			INSERT INTO base_task (title, state, work_type, assignment_group_id, assigned_user_id, closure_reason)
			VALUES ($1, $2, 'TASK', $3, $4, $5)
			RETURNING _id::text`, title, state, group, assignee, reason).Scan(&id)
		return id, err
	}

	// AC-7: an assignee without a group is still refused.
	_, err := insertTask("unanchored assignee", "pending", nil, memberID, nil)
	if err == nil || !strings.Contains(err.Error(), "assigned_user_id requires assignment_group_id") {
		t.Fatalf("assignee without group = %v, want 'assigned_user_id requires assignment_group_id'", err)
	}

	// AC-8: an assignee outside the group is still refused.
	_, err = insertTask("outsider", "pending", groupID, outsiderID, nil)
	if err == nil || !strings.Contains(err.Error(), "assigned_user_id must be a member of assignment_group_id") {
		t.Fatalf("outsider = %v, want the membership refusal", err)
	}

	// A member of the group is accepted (the rule is membership, not refusal).
	memberTask, err := insertTask("member", "pending", groupID, memberID, nil)
	if err != nil {
		t.Fatalf("group member refused: %v", err)
	}

	// AC-9: closed without a reason defaults to completed.
	closedTask, err := insertTask("closes without reason", "closed", nil, nil, nil)
	if err != nil {
		t.Fatalf("closed without reason: %v", err)
	}
	var reason string
	if err := Pool.QueryRow(ctx, `SELECT closure_reason FROM base_task WHERE _id = $1`, closedTask).Scan(&reason); err != nil {
		t.Fatalf("read closure_reason: %v", err)
	}
	if reason != "completed" {
		t.Fatalf("closure_reason = %q, want completed", reason)
	}

	// AC-9: the closure vocabulary is still the four reasons.
	_, err = insertTask("bad reason", "closed", nil, nil, "approved")
	if err == nil {
		t.Fatal("closure_reason='approved' was accepted; 084/085 allow completed|cancelled|rejected|failed")
	}

	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM base_task WHERE _id = ANY($1::uuid[])`,
			[]string{memberTask, closedTask})
	})

	// The constraint that enforces the vocabulary is still on the table, not
	// only in the trigger, so a direct write cannot dodge it.
	var constraint string
	if err := Pool.QueryRow(ctx, `
		SELECT pg_get_constraintdef(c.oid)
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		WHERE t.relname = 'base_task' AND c.conname = 'chk_base_task_closure_reason'`).Scan(&constraint); err != nil {
		t.Fatalf("chk_base_task_closure_reason missing: %v", err)
	}
	for _, want := range []string{"completed", "cancelled", "rejected", "failed"} {
		if !strings.Contains(constraint, want) {
			t.Fatalf("closure constraint %q does not mention %q", constraint, want)
		}
	}
}
