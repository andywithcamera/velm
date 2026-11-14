package db

import (
	"fmt"
	"testing"
	"time"
)

// TestSpawnTaskNotifiesAssignee is issue #332: a task spawned by the engine
// with an assignee must notify that person, exactly as a task assigned through
// a form save does.
//
// The engine writes base_task with its own INSERT (spawnTaskWithRoute) instead
// of going through HandleSave, so the EmitTaskNotifications call that covers
// form saves never ran for spawned work. The result was silent: the task
// appeared on the assignee's board and no notification was ever created.
func TestSpawnTaskNotifiesAssignee(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	assigneeID, assigneeGroupID := seedSecondActor(ctx, t, "assignee")

	slug := "spawn-notify-" + suffix
	def := seedDefinition(ctx, t, slug, "Notify On Spawn", false, "")
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition
		SET default_group_id = $2, default_assignee_id = $3
		WHERE _id = $1`, def, assigneeGroupID, assigneeID); err != nil {
		t.Fatalf("configure definition: %v", err)
	}

	taskID, err := SpawnTaskInstanceBySlug(ctx, Pool, slug, "", "", map[string]any{})
	if err != nil {
		t.Fatalf("spawn task: %v", err)
	}

	var (
		title string
		href  string
	)
	err = Pool.QueryRow(ctx, `
		SELECT title, href
		FROM _user_notification
		WHERE user_id = $1::uuid
		  AND _deleted_at IS NULL
		ORDER BY _created_at DESC
		LIMIT 1`, assigneeID).Scan(&title, &href)
	if err != nil {
		t.Fatalf("spawned task %s did not notify the assignee: %v", taskID, err)
	}

	if title != "Task assigned to you" {
		t.Fatalf("notification title = %q, want %q", title, "Task assigned to you")
	}
	if want := "/f/base_task/" + taskID; href != want {
		t.Fatalf("notification href = %q, want %q", href, want)
	}

	var number, taskTitle string
	if err := Pool.QueryRow(ctx, `SELECT number, title FROM base_task WHERE _id = $1::uuid`, taskID).
		Scan(&number, &taskTitle); err != nil {
		t.Fatalf("read task: %v", err)
	}
	var body string
	if err := Pool.QueryRow(ctx, `
		SELECT COALESCE(body, '')
		FROM _user_notification
		WHERE user_id = $1::uuid AND _deleted_at IS NULL
		ORDER BY _created_at DESC
		LIMIT 1`, assigneeID).Scan(&body); err != nil {
		t.Fatalf("read notification body: %v", err)
	}
	if want := number + " " + taskTitle; body != want {
		t.Fatalf("notification body = %q, want %q", body, want)
	}
}

// A root landing in a shared queue is news to that queue; a container's eight
// stage children are not. resolveSpawnAssignee only names a person when the
// group has exactly one member, so a two-member group here stays unassigned and
// exercises the group-queue path.
func TestSpawnTaskNotifiesGroupQueueForRootOnly(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	firstID, groupID := seedSecondActor(ctx, t, "queue-a")
	secondID, _ := seedSecondActor(ctx, t, "queue-b")
	if _, err := Pool.Exec(ctx, `INSERT INTO _group_membership (group_id, user_id) VALUES ($1, $2)`, groupID, secondID); err != nil {
		t.Fatalf("add second member: %v", err)
	}

	parentSlug := "queue-parent-" + suffix
	parentDef := seedDefinition(ctx, t, parentSlug, "Parent", true, "")
	childSlug := "queue-child-" + suffix
	childDef := seedDefinition(ctx, t, childSlug, "Child", false, "")
	for _, def := range []string{parentDef, childDef} {
		if _, err := Pool.Exec(ctx, `
			UPDATE base_task_definition
			SET default_group_id = $2, default_assignee_id = NULL
			WHERE _id = $1`, def, groupID); err != nil {
			t.Fatalf("configure definition: %v", err)
		}
	}
	if _, err := Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child (container_definition_id, child_definition_id, sort_order)
		VALUES ($1, $2, 100)`, parentDef, childDef); err != nil {
		t.Fatalf("link child: %v", err)
	}

	if _, err := SpawnTaskInstanceBySlug(ctx, Pool, parentSlug, "", "", map[string]any{}); err != nil {
		t.Fatalf("spawn parent: %v", err)
	}

	countFor := func(userID, titlePrefix string) int {
		var count int
		if err := Pool.QueryRow(ctx, `
			SELECT COUNT(1) FROM _user_notification
			WHERE user_id = $1::uuid AND _deleted_at IS NULL AND title LIKE $2 || '%'`,
			userID, titlePrefix).Scan(&count); err != nil {
			t.Fatalf("count %q notifications: %v", titlePrefix, err)
		}
		return count
	}

	for _, userID := range []string{firstID, secondID} {
		if got := countFor(userID, "Unassigned task in your group"); got != 1 {
			t.Fatalf("root spawn into a shared queue notified %d times, want 1", got)
		}
	}

	// Now the children: a container opening spawns each stage into the same
	// queue. Those must stay silent.
	before := countFor(firstID, "Unassigned task in your group")
	if _, err := SpawnTaskInstanceBySlug(ctx, Pool, parentSlug, "", "", map[string]any{}); err != nil {
		t.Fatalf("spawn second parent: %v", err)
	}
	if after := countFor(firstID, "Unassigned task in your group"); after != before+1 {
		t.Fatalf("second root added %d queue notifications, want 1", after-before)
	}
}
