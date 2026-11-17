package db

// Assignment strategies, resolved once at spawn (Process Designer, Plan T5 /
// Requirements §3, AC-46 to AC-66).
//
// Every test here runs against real Postgres: the point of the slice is that a
// strategy reaches base_task.assigned_user_id through the spawn transaction,
// and 084's trigger rejects an assignee who is not a member of the group. A
// test that ran without the database would prove nothing.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// assignSeedGroup creates a group with n members and returns its id plus the
// member user ids in the order groupMemberIDs uses (user id ascending).
func assignSeedGroup(t *testing.T, ctx context.Context, n int) (string, []string) {
	t.Helper()
	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	var groupID string
	if err := Pool.QueryRow(ctx, `INSERT INTO _group (name) VALUES ($1) RETURNING _id::text`, "assign-"+suffix).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	members := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var userID string
		if err := Pool.QueryRow(ctx, `
			INSERT INTO _user (name, email)
			VALUES ($1, $2) RETURNING _id::text`,
			fmt.Sprintf("assign-member-%d", i), fmt.Sprintf("assign-%d-%s@example.test", i, suffix)).Scan(&userID); err != nil {
			t.Fatalf("seed member %d: %v", i, err)
		}
		if _, err := Pool.Exec(ctx, `INSERT INTO _group_membership (group_id, user_id) VALUES ($1, $2)`, groupID, userID); err != nil {
			t.Fatalf("seed membership %d: %v", i, err)
		}
		members = append(members, userID)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = Pool.Exec(bg, `DELETE FROM base_assignment_cursor WHERE group_id = $1`, groupID)
		_, _ = Pool.Exec(bg, `DELETE FROM _group_membership WHERE group_id = $1`, groupID)
		_, _ = Pool.Exec(bg, `DELETE FROM _group WHERE _id = $1`, groupID)
		for _, id := range members {
			_, _ = Pool.Exec(bg, `DELETE FROM _user WHERE _id = $1`, id)
		}
	})
	ordered, err := groupMemberIDs(ctx, Pool, groupID)
	if err != nil {
		t.Fatalf("read members: %v", err)
	}
	return groupID, ordered
}

// assignDefinition seeds a leaf definition with an assignment configuration.
func assignDefinition(t *testing.T, ctx context.Context, slug string, groupID, strategy, assigneeID string) string {
	t.Helper()
	defID := seedDefinition(ctx, t, slug, "Assignment "+slug, false, "")
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition
		SET default_group_id = $2, assignment_strategy = $3, default_assignee_id = $4
		WHERE _id = $1`, defID, nullableText(groupID), nullableText(strategy), nullableText(assigneeID)); err != nil {
		t.Fatalf("configure definition %s: %v", slug, err)
	}
	return defID
}

// spawnAssignment spawns a definition as requester and returns the instance's
// group, assignee and state.
func spawnAssignment(t *testing.T, ctx context.Context, defID, requester string, inputs map[string]any) (taskID, groupID, assigneeID, state string) {
	t.Helper()
	taskID, err := SpawnTaskInstance(ctx, Pool, defID, "", requester, inputs)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var group, assignee *string
	if err := Pool.QueryRow(ctx, `
		SELECT assignment_group_id::text, assigned_user_id::text, state
		FROM base_task WHERE _id = $1`, taskID).Scan(&group, &assignee, &state); err != nil {
		t.Fatalf("read spawned task: %v", err)
	}
	if group != nil {
		groupID = *group
	}
	if assignee != nil {
		assigneeID = *assignee
	}
	return taskID, groupID, assigneeID, state
}

// AC-46: both columns accept exactly the six strategies and NULL, and reject a
// seventh.
func TestAssignmentStrategyVocabulary(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, _ := assignSeedGroup(t, ctx, 1)
	defID := assignDefinition(t, ctx, "assign-vocab-"+suffix, groupID, "", "")
	containerID := seedDefinition(ctx, t, "assign-vocab-container-"+suffix, "Vocabulary container", true, "")
	childID := seedDefinition(ctx, t, "assign-vocab-child-"+suffix, "Vocabulary child", false, "")
	seedChildLink(ctx, t, containerID, childID, 0, nil)

	for _, strategy := range AssignmentStrategies {
		if _, err := Pool.Exec(ctx, `UPDATE base_task_definition SET assignment_strategy = $2 WHERE _id = $1`, defID, strategy); err != nil {
			t.Errorf("definition rejected strategy %q: %v", strategy, err)
		}
		if _, err := Pool.Exec(ctx, `UPDATE base_task_definition_child SET assignment_strategy = $2 WHERE container_definition_id = $1`, containerID, strategy); err != nil {
			t.Errorf("block rejected strategy %q: %v", strategy, err)
		}
	}
	if _, err := Pool.Exec(ctx, `UPDATE base_task_definition SET assignment_strategy = NULL WHERE _id = $1`, defID); err != nil {
		t.Errorf("definition rejected NULL strategy: %v", err)
	}
	if _, err := Pool.Exec(ctx, `UPDATE base_task_definition SET assignment_strategy = 'least_loaded' WHERE _id = $1`, defID); err == nil {
		t.Error("assignment_strategy accepted least_loaded; the six-strategy vocabulary is a constraint")
	}
}

// AC-48: the cursor is its own table and _group gained no column for it.
func TestAssignmentCursorTable(t *testing.T) {
	ctx := taskEngineTestDB(t)
	var columns []string
	rows, err := Pool.Query(ctx, `
		SELECT column_name FROM information_schema.columns
		WHERE table_name = 'base_assignment_cursor' ORDER BY column_name`)
	if err != nil {
		t.Fatalf("read cursor columns: %v", err)
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, c)
	}
	rows.Close()
	joined := strings.Join(columns, ",")
	if !strings.Contains(joined, "group_id") || !strings.Contains(joined, "next_index") {
		t.Fatalf("base_assignment_cursor columns = %v, want group_id and next_index", columns)
	}
	var groupExtra int
	if err := Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_name = '_group' AND column_name IN ('next_index', 'rr_cursor')`).Scan(&groupExtra); err != nil {
		t.Fatal(err)
	}
	if groupExtra != 0 {
		t.Fatalf("_group gained %d cursor column(s); the cursor must live in its own table", groupExtra)
	}
}

// AC-49, AC-50: unassigned leaves the assignee NULL even when the group has one
// member, sets the group, and the row still reaches ready.
func TestAssignmentUnassigned(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 1)
	defID := assignDefinition(t, ctx, "assign-unassigned-"+suffix, groupID, AssignmentUnassigned, "")

	_, gotGroup, gotAssignee, state := spawnAssignment(t, ctx, defID, members[0], nil)
	if gotGroup != groupID {
		t.Fatalf("group = %q, want %q", gotGroup, groupID)
	}
	if gotAssignee != "" {
		t.Fatalf("assignee = %q, want NULL: unassigned must not use the sole-member rule", gotAssignee)
	}
	if state != taskStateReady {
		t.Fatalf("state = %q, want %q", state, taskStateReady)
	}
}

// AC-51, AC-52: named_user stamps a member and downgrades a non-member to
// NULL without failing the spawn.
func TestAssignmentNamedUser(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 2)
	defID := assignDefinition(t, ctx, "assign-named-"+suffix, groupID, AssignmentNamedUser, members[1])
	_, _, assignee, _ := spawnAssignment(t, ctx, defID, members[0], nil)
	if assignee != members[1] {
		t.Fatalf("assignee = %q, want the named user %q", assignee, members[1])
	}
}

func TestAssignmentNamedUserNotMember(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 1)
	// A user who exists but is not in this group: the foreign key allows the
	// column, 084 refuses the row, so resolution must downgrade to NULL.
	var outsider string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email) VALUES ($1, $2) RETURNING _id::text`,
		"assign-outsider", "assign-outsider-"+suffix+"@example.test").Scan(&outsider); err != nil {
		t.Fatalf("seed outsider: %v", err)
	}
	t.Cleanup(func() { _, _ = Pool.Exec(context.Background(), `DELETE FROM _user WHERE _id = $1`, outsider) })

	defID := assignDefinition(t, ctx, "assign-nonmember-"+suffix, groupID, AssignmentNamedUser, outsider)
	_, gotGroup, assignee, state := spawnAssignment(t, ctx, defID, members[0], nil)
	if gotGroup != groupID || assignee != "" {
		t.Fatalf("group/assignee = %q/%q, want %q and NULL", gotGroup, assignee, groupID)
	}
	if state != taskStateReady {
		t.Fatalf("state = %q, want %q: a misconfigured assignee must not fail the spawn", state, taskStateReady)
	}
}

// AC-53, AC-54: requester stamps the ordering user when they are a member and
// resolves to NULL without error when they are not.
func TestAssignmentRequester(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 2)
	defID := assignDefinition(t, ctx, "assign-requester-"+suffix, groupID, AssignmentRequester, "")

	_, _, assignee, _ := spawnAssignment(t, ctx, defID, members[0], nil)
	if assignee != members[0] {
		t.Fatalf("assignee = %q, want the requester %q", assignee, members[0])
	}

	var outsider string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email) VALUES ($1, $2) RETURNING _id::text`,
		"assign-outsider-req", "assign-outsider-req-"+suffix+"@example.test").Scan(&outsider); err != nil {
		t.Fatalf("seed outsider: %v", err)
	}
	t.Cleanup(func() { _, _ = Pool.Exec(context.Background(), `DELETE FROM _user WHERE _id = $1`, outsider) })

	_, _, assignee, _ = spawnAssignment(t, ctx, defID, outsider, nil)
	if assignee != "" {
		t.Fatalf("assignee = %q, want NULL for a requester outside the group", assignee)
	}
}

// AC-55, AC-56: from_input reads a named user input, and an unusable value is
// refused at spawn with no row written.
func TestAssignmentFromInput(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 2)
	containerID := seedDefinition(ctx, t, "assign-frominput-"+suffix, "From input container", true, "")
	childID := seedDefinition(ctx, t, "assign-frominput-child-"+suffix, "From input child", false, "")
	blockID := controlTestBlock(t, ctx, containerID, "", childID, "task", "", "Approval", 0,
		map[string]any{"user_input": "approver"}, nil)
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition_child
		SET assignment_group_id = $2, assignment_strategy = 'from_input'
		WHERE _id = $1`, blockID, groupID); err != nil {
		t.Fatalf("configure block: %v", err)
	}

	root, err := SpawnTaskInstance(ctx, Pool, containerID, "", members[0], map[string]any{"approver": members[1]})
	if err != nil {
		t.Fatalf("spawn with a valid input: %v", err)
	}
	var assignee *string
	if err := Pool.QueryRow(ctx, `
		SELECT assigned_user_id::text FROM base_task
		WHERE parent_task_id = $1 AND definition_id = $2`, root, childID).Scan(&assignee); err != nil {
		t.Fatalf("read child: %v", err)
	}
	if assignee == nil || *assignee != members[1] {
		t.Fatalf("assignee = %v, want %q from the approver input", assignee, members[1])
	}

	before := assignmentTaskCount(t, ctx)
	_, err = SpawnTaskInstance(ctx, Pool, containerID, "", members[0], map[string]any{"approver": "not-a-user"})
	if err == nil {
		t.Fatal("spawn accepted a from_input value that is not a user")
	}
	if !strings.Contains(err.Error(), "approver") {
		t.Errorf("refusal does not name the input: %v", err)
	}
	if after := assignmentTaskCount(t, ctx); after != before {
		t.Fatalf("refused spawn wrote %d rows", after-before)
	}
}

func assignmentTaskCount(t *testing.T, ctx context.Context) int {
	t.Helper()
	var n int
	if err := Pool.QueryRow(ctx, `SELECT COUNT(*) FROM base_task`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// AC-57, AC-58, AC-59: random_member picks only members, is deterministic for
// a one-member group, and resolves to NULL for an empty group.
func TestAssignmentRandomMember(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 5)
	defID := assignDefinition(t, ctx, "assign-random-"+suffix, groupID, AssignmentRandomMember, "")

	allowed := map[string]bool{}
	for _, m := range members {
		allowed[m] = true
	}
	for i := 0; i < 50; i++ {
		_, _, assignee, _ := spawnAssignment(t, ctx, defID, members[0], nil)
		if !allowed[assignee] {
			t.Fatalf("spawn %d assigned %q, who is not a member of the group", i, assignee)
		}
	}

	singleGroup, single := assignSeedGroup(t, ctx, 1)
	singleDef := assignDefinition(t, ctx, "assign-random-single-"+suffix, singleGroup, AssignmentRandomMember, "")
	for i := 0; i < 3; i++ {
		if _, _, assignee, _ := spawnAssignment(t, ctx, singleDef, single[0], nil); assignee != single[0] {
			t.Fatalf("one-member random_member picked %q, want %q", assignee, single[0])
		}
	}

	emptyGroup, _ := assignSeedGroup(t, ctx, 0)
	emptyDef := assignDefinition(t, ctx, "assign-random-empty-"+suffix, emptyGroup, AssignmentRandomMember, "")
	if _, gotGroup, assignee, _ := spawnAssignment(t, ctx, emptyDef, "", nil); assignee != "" || gotGroup != emptyGroup {
		t.Fatalf("empty-group random_member = group %q assignee %q, want %q/NULL", gotGroup, assignee, emptyGroup)
	}
}

// AC-60, AC-63: round_robin walks members in a stable order and the cursor is
// monotonic, not reset.
func TestAssignmentRoundRobinSequence(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 3)
	if len(members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(members))
	}
	defID := assignDefinition(t, ctx, "assign-rr-"+suffix, groupID, AssignmentRoundRobin, "")

	got := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		_, _, assignee, _ := spawnAssignment(t, ctx, defID, members[0], nil)
		got = append(got, assignee)
	}
	for i := 0; i < 3; i++ {
		if got[i] != members[i] || got[i+3] != members[i] {
			t.Fatalf("sequence = %v, want %v repeated twice", got, members)
		}
	}
	var next int64
	if err := Pool.QueryRow(ctx, `SELECT next_index FROM base_assignment_cursor WHERE group_id = $1`, groupID).Scan(&next); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if next != 6 {
		t.Fatalf("next_index = %d after 6 spawns, want 6 (monotonic, not reset)", next)
	}
}

// AC-61: concurrent spawns never pick the same member twice.
func TestAssignmentRoundRobinConcurrent(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	const n = 6
	groupID, members := assignSeedGroup(t, ctx, n)
	defID := assignDefinition(t, ctx, "assign-rr-concurrent-"+suffix, groupID, AssignmentRoundRobin, "")

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			taskID, err := SpawnTaskInstance(ctx, Pool, defID, "", members[0], map[string]any{})
			if err != nil {
				errs <- err
				return
			}
			var assignee *string
			if err := Pool.QueryRow(ctx, `SELECT assigned_user_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&assignee); err != nil {
				errs <- err
				return
			}
			mu.Lock()
			if assignee == nil {
				seen[""]++
			} else {
				seen[*assignee]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent spawn: %v", err)
	}
	if len(seen) != n {
		t.Fatalf("assignees = %v, want %d distinct members", seen, n)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("member %q assigned %d times; concurrent spawns must not repeat", id, count)
		}
	}
}

// AC-62: a spawn that fails after the cursor moved leaves it where it was.
func TestAssignmentRoundRobinRollbackRestoresCursor(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 3)

	rrDef := assignDefinition(t, ctx, "assign-rr-rollback-"+suffix, groupID, AssignmentRoundRobin, "")
	// Warm the cursor to a known position so "restored" is distinguishable
	// from "never created".
	spawnAssignment(t, ctx, rrDef, members[0], nil)
	var before int64
	if err := Pool.QueryRow(ctx, `SELECT next_index FROM base_assignment_cursor WHERE group_id = $1`, groupID).Scan(&before); err != nil {
		t.Fatalf("read cursor: %v", err)
	}

	containerID := seedDefinition(ctx, t, "assign-rr-fail-"+suffix, "Rollback container", true, "")
	rrChild := seedDefinition(ctx, t, "assign-rr-fail-child-"+suffix, "Rollback rr child", false, "")
	badChild := seedDefinition(ctx, t, "assign-rr-fail-bad-"+suffix, "Rollback bad child", false, "")
	goodBlock := controlTestBlock(t, ctx, containerID, "", rrChild, "task", "", "First", 0, nil, nil)
	badBlock := controlTestBlock(t, ctx, containerID, "", badChild, "task", "", "Second", 10,
		map[string]any{"user_input": "approver"}, nil)
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition_child SET assignment_group_id = $2, assignment_strategy = 'round_robin' WHERE _id = $1`,
		goodBlock, groupID); err != nil {
		t.Fatalf("configure good block: %v", err)
	}
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition_child SET assignment_group_id = $2, assignment_strategy = 'from_input' WHERE _id = $1`,
		badBlock, groupID); err != nil {
		t.Fatalf("configure bad block: %v", err)
	}
	if _, err := SpawnTaskInstance(ctx, Pool, containerID, "", members[0], map[string]any{}); err == nil {
		t.Fatal("spawn with a broken from_input block succeeded")
	}
	var after int64
	if err := Pool.QueryRow(ctx, `SELECT next_index FROM base_assignment_cursor WHERE group_id = $1`, groupID).Scan(&after); err != nil {
		t.Fatalf("read cursor after rollback: %v", err)
	}
	if after != before {
		t.Fatalf("next_index = %d after a rolled-back spawn, want %d: the cursor must not be consumed", after, before)
	}
	_, _, assignee, _ := spawnAssignment(t, ctx, rrDef, members[0], nil)
	if want := members[int(before)%len(members)]; assignee != want {
		t.Fatalf("next successful spawn assigned %q, want %q: the failed spawn consumed its turn", assignee, want)
	}
}

// AC-64: assignment is resolved once. Reassigning the task afterwards changes
// the task and nothing else.
func TestAssignmentResolvedOnce(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 2)
	defID := assignDefinition(t, ctx, "assign-once-"+suffix, groupID, AssignmentNamedUser, members[0])

	taskID, _, assignee, _ := spawnAssignment(t, ctx, defID, members[0], nil)
	if assignee != members[0] {
		t.Fatalf("assignee = %q, want %q", assignee, members[0])
	}
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET assigned_user_id = $2 WHERE _id = $1`, taskID, members[1]); err != nil {
		t.Fatalf("reassign task: %v", err)
	}
	var got *string
	if err := Pool.QueryRow(ctx, `SELECT assigned_user_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != members[1] {
		t.Fatalf("reassigned task reads %v, want %q", got, members[1])
	}
	var strategy *string
	if err := Pool.QueryRow(ctx, `SELECT assignment_strategy FROM base_task_definition WHERE _id = $1`, defID).Scan(&strategy); err != nil {
		t.Fatal(err)
	}
	if strategy == nil || *strategy != AssignmentNamedUser {
		t.Fatalf("definition strategy = %v; reassigning a task must not change the policy", strategy)
	}
}

// AC-47: a block's assignment overrides the definition's, and a block that
// sets nothing inherits.
func TestBlockAssignmentOverride(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 2)
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	containerID := seedDefinition(ctx, t, "assign-block-override-"+suffix, "Override container", true, "")
	inheritDef := assignDefinition(t, ctx, "assign-block-inherit-"+suffix, groupID, AssignmentNamedUser, members[0])
	overrideDef := assignDefinition(t, ctx, "assign-block-override-child-"+suffix, groupID, AssignmentNamedUser, members[0])

	controlTestBlock(t, ctx, containerID, "", inheritDef, "task", "", "Inherits", 0, nil, nil)
	overrideBlock := controlTestBlock(t, ctx, containerID, "", overrideDef, "task", "", "Overrides", 10, nil, nil)
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition_child
		SET assignment_group_id = $2, assignment_strategy = 'named_user', assigned_user_id = $3
		WHERE _id = $1`, overrideBlock, groupID, members[1]); err != nil {
		t.Fatalf("configure override block: %v", err)
	}

	root, err := SpawnTaskInstance(ctx, Pool, containerID, "", members[0], map[string]any{})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	rows, err := Pool.Query(ctx, `
		SELECT definition_id::text, COALESCE(assigned_user_id::text, '')
		FROM base_task WHERE parent_task_id = $1`, root)
	if err != nil {
		t.Fatalf("read children: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var defID, assignee string
		if err := rows.Scan(&defID, &assignee); err != nil {
			t.Fatal(err)
		}
		got[defID] = assignee
	}
	if got[inheritDef] != members[0] {
		t.Errorf("inheriting block assignee = %q, want the definition's %q", got[inheritDef], members[0])
	}
	if got[overrideDef] != members[1] {
		t.Errorf("overriding block assignee = %q, want the block's %q", got[overrideDef], members[1])
	}
}

// AC-66: a control task inherits the scope's assignment and an unassigned
// control task does not break the run.
func TestControlTaskAssignment(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	groupID, members := assignSeedGroup(t, ctx, 1)

	containerID := seedDefinition(ctx, t, "assign-control-"+suffix, "Control assignment container", true, "")
	childID := seedDefinition(ctx, t, "assign-control-child-"+suffix, "Control assignment child", false, "")
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition SET default_group_id = $2, assignment_strategy = 'unassigned' WHERE _id = $1`,
		containerID, groupID); err != nil {
		t.Fatalf("configure container: %v", err)
	}
	ifBlock := controlTestBlock(t, ctx, containerID, "", "", "control", "if", "Always", 0,
		map[string]any{"condition": "expr:true"}, nil)
	controlTestBlock(t, ctx, containerID, ifBlock, childID, "task", "", "Inside", 0, nil, nil)

	root, err := SpawnTaskInstance(ctx, Pool, containerID, "", members[0], map[string]any{})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	var state string
	var group, assignee *string
	if err := Pool.QueryRow(ctx, `
		SELECT state, assignment_group_id::text, assigned_user_id::text
		FROM base_task WHERE parent_task_id = $1 AND work_type = 'CONTROL'`, root).Scan(&state, &group, &assignee); err != nil {
		t.Fatalf("read control task: %v", err)
	}
	if assignee != nil {
		t.Fatalf("control assignee = %q, want NULL: the scope is unassigned", *assignee)
	}
	if group == nil || *group != groupID {
		t.Fatalf("control group = %v, want %q inherited from the scope", group, groupID)
	}
	if state != taskStateReady {
		t.Fatalf("control state = %q, want %q: an unassigned control task must not break the run", state, taskStateReady)
	}
}
