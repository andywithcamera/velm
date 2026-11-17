package db

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"velm/internal/auth"
)

// Plan T2 (Requirements AC-93 to AC-95): on a task, the assignment group
// governs the assignee. These tests drive the real save path rather than the
// helper, so the 303/400 and the stored columns are both asserted.

type assignmentFixture struct {
	groupA, groupB    string
	memberA, memberB  string
	nonMemberOfEither string
	actor             string
}

func seedAssignmentFixture(ctx context.Context, t *testing.T) assignmentFixture {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	newGroup := func(label string) string {
		var id string
		if err := Pool.QueryRow(ctx, `
			INSERT INTO _group (name, description, is_system)
			VALUES ($1, 'assignment guard test', FALSE)
			RETURNING _id::text`, label+"-"+suffix).Scan(&id); err != nil {
			t.Fatalf("seed group %s: %v", label, err)
		}
		return id
	}
	newUser := func(label string) string {
		var id string
		if err := Pool.QueryRow(ctx, `
			INSERT INTO _user (name, email, password_hash)
			VALUES ($1, $2, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
			RETURNING _id::text`, label, label+"-"+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", label, err)
		}
		return id
	}
	join := func(userID, groupID string) {
		if _, err := Pool.Exec(ctx, `INSERT INTO _group_membership (user_id, group_id) VALUES ($1, $2)`, userID, groupID); err != nil {
			t.Fatalf("seed membership: %v", err)
		}
	}

	f := assignmentFixture{
		groupA:            newGroup("assign-a"),
		groupB:            newGroup("assign-b"),
		memberA:           newUser("assign-member-a"),
		memberB:           newUser("assign-member-b"),
		nonMemberOfEither: newUser("assign-outsider"),
	}
	f.actor = newUser("assign-actor")
	join(f.memberA, f.groupA)
	join(f.memberB, f.groupB)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = Pool.Exec(bg, `DELETE FROM base_task WHERE assignment_group_id = ANY($1::uuid[]) OR assigned_user_id = ANY($1::uuid[])`,
			[]string{f.groupA, f.groupB, f.memberA, f.memberB, f.nonMemberOfEither, f.actor})
		for _, u := range []string{f.memberA, f.memberB, f.nonMemberOfEither, f.actor} {
			_, _ = Pool.Exec(bg, `DELETE FROM _group_membership WHERE user_id = $1`, u)
			_, _ = Pool.Exec(bg, `DELETE FROM _user WHERE _id = $1`, u)
		}
		for _, g := range []string{f.groupA, f.groupB} {
			_, _ = Pool.Exec(bg, `DELETE FROM _group WHERE _id = $1`, g)
		}
	})
	return f
}

func seedTaskWithAssignment(ctx context.Context, t *testing.T, state, groupID, userID string) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task (title, state, assignment_group_id, assigned_user_id)
		VALUES ('Assignment guard probe', $1, $2, $3)
		RETURNING _id::text`, state, groupID, userID).Scan(&id); err != nil {
		t.Fatalf("seed task in %s: %v", state, err)
	}
	return id
}

func postBaseTaskForm(t *testing.T, actor, taskID, title, groupID, userID string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	form.Set("_id", taskID)
	form.Set("title", title)
	form.Set("assignment_group_id", groupID)
	form.Set("assigned_user_id", userID)
	req := httptest.NewRequest(http.MethodPost, "/api/save/base_task", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithUserContext(req.Context(), actor, "actor@example.test", "Actor", "admin"))
	rec := httptest.NewRecorder()
	HandleSave(rec, req)
	return rec
}

func readTaskAssignment(ctx context.Context, t *testing.T, taskID string) (groupID, userID string) {
	t.Helper()
	if err := Pool.QueryRow(ctx, `
		SELECT COALESCE(assignment_group_id::text, ''), COALESCE(assigned_user_id::text, '')
		FROM base_task WHERE _id = $1`, taskID).Scan(&groupID, &userID); err != nil {
		t.Fatalf("read task assignment: %v", err)
	}
	return groupID, userID
}

// AC-94: a new group plus an assignee who is not a member of it stores NULL,
// instead of failing on the 084 membership trigger.
func TestSaveRecordClearsAssigneeOnGroupChange(t *testing.T) {
	ctx := taskEngineTestDB(t)
	f := seedAssignmentFixture(ctx, t)
	taskID := seedTaskWithAssignment(ctx, t, "pending", f.groupA, f.memberA)

	rec := postBaseTaskForm(t, f.actor, taskID, "Assignment guard probe", f.groupB, f.memberA)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}

	groupID, userID := readTaskAssignment(ctx, t, taskID)
	if groupID != strings.ToLower(f.groupB) {
		t.Fatalf("stored group = %q, want %q", groupID, f.groupB)
	}
	if userID != "" {
		t.Fatalf("stored assignee = %q, want NULL after a group change", userID)
	}
}

// AC-95: the group is writable while pending and read-only afterwards, on the
// server, one case per state in chk_base_task_state (085).
func TestAssignmentGroupLockedPastPending(t *testing.T) {
	ctx := taskEngineTestDB(t)
	f := seedAssignmentFixture(ctx, t)

	for _, state := range []string{"ready", "in_progress", "closed", "skipped"} {
		t.Run(state, func(t *testing.T) {
			taskID := seedTaskWithAssignment(ctx, t, state, f.groupA, f.memberA)
			rec := postBaseTaskForm(t, f.actor, taskID, "Assignment guard probe", f.groupB, f.memberB)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(strings.ToLower(rec.Body.String()), "read-only") {
				t.Fatalf("refusal body %q does not say read-only", rec.Body.String())
			}
			groupID, userID := readTaskAssignment(ctx, t, taskID)
			if groupID != f.groupA {
				t.Fatalf("group changed on a %s task: got %q, want %q", state, groupID, f.groupA)
			}
			if userID != f.memberA {
				t.Fatalf("assignee changed on a %s task: got %q, want %q", state, userID, f.memberA)
			}
		})
	}

	t.Run("pending", func(t *testing.T) {
		taskID := seedTaskWithAssignment(ctx, t, "pending", f.groupA, f.memberA)
		rec := postBaseTaskForm(t, f.actor, taskID, "Assignment guard probe", f.groupB, f.memberB)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST status = %d, want 303; body=%s", rec.Code, rec.Body.String())
		}
		groupID, userID := readTaskAssignment(ctx, t, taskID)
		if groupID != strings.ToLower(f.groupB) {
			t.Fatalf("stored group = %q, want %q", groupID, f.groupB)
		}
		if userID != "" {
			t.Fatalf("stored assignee = %q, want NULL because the group changed", userID)
		}
	})
}
