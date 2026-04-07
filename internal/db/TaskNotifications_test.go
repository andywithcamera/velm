package db

import "testing"

const (
	taskNotifyActor    = "550e8400-e29b-41d4-a716-446655440000"
	taskNotifyAssignee = "550e8400-e29b-41d4-a716-446655440001"
	taskNotifyUser2    = "550e8400-e29b-41d4-a716-446655440002"
	taskNotifyUser3    = "550e8400-e29b-41d4-a716-446655440003"
)

func TestPlanBaseTaskNotificationsAssignment(t *testing.T) {
	inputs := planBaseTaskNotifications(
		"89ba09f0-4296-4d79-8c94-0128b2cfc3f4",
		"update",
		taskNotifyActor,
		map[string]string{
			"number": "TASK-000005",
			"title":  "Reset laptop",
		},
		map[string]string{
			"number":           "TASK-000005",
			"title":            "Reset laptop",
			"assignment_group": "IT",
			"assigned_user_id": taskNotifyAssignee,
		},
		nil,
	)

	if len(inputs) != 1 {
		t.Fatalf("len(inputs) = %d, want 1", len(inputs))
	}
	if inputs[0].UserID != taskNotifyAssignee {
		t.Fatalf("UserID = %q, want %q", inputs[0].UserID, taskNotifyAssignee)
	}
	if inputs[0].Title != "Task assigned to you" {
		t.Fatalf("Title = %q", inputs[0].Title)
	}
}

func TestPlanBaseTaskNotificationsGroupQueue(t *testing.T) {
	inputs := planBaseTaskNotifications(
		"89ba09f0-4296-4d79-8c94-0128b2cfc3f4",
		"insert",
		taskNotifyActor,
		nil,
		map[string]string{
			"number":              "TASK-000006",
			"title":               "Replace monitor",
			"assignment_group_id": "123",
		},
		[]string{taskNotifyActor, taskNotifyUser2, taskNotifyUser3},
	)

	if len(inputs) != 2 {
		t.Fatalf("len(inputs) = %d, want 2", len(inputs))
	}
	if inputs[0].Title != "Unassigned task in your group" {
		t.Fatalf("Title = %q", inputs[0].Title)
	}
	if inputs[0].UserID != taskNotifyUser2 || inputs[1].UserID != taskNotifyUser3 {
		t.Fatalf("unexpected recipients: %#v", inputs)
	}
}

func TestPlanBaseTaskNotificationsAssignedTaskChanged(t *testing.T) {
	inputs := planBaseTaskNotifications(
		"89ba09f0-4296-4d79-8c94-0128b2cfc3f4",
		"bulk_update",
		taskNotifyActor,
		map[string]string{
			"number":           "TASK-000007",
			"title":            "Provision access",
			"assigned_user_id": taskNotifyAssignee,
			"state":            "new",
		},
		map[string]string{
			"number":           "TASK-000007",
			"title":            "Provision access",
			"assigned_user_id": taskNotifyAssignee,
			"state":            "in_progress",
		},
		nil,
	)

	if len(inputs) != 1 {
		t.Fatalf("len(inputs) = %d, want 1", len(inputs))
	}
	if inputs[0].Title != "Task updated" {
		t.Fatalf("Title = %q", inputs[0].Title)
	}
	if inputs[0].Body != "TASK-000007 Provision access changed: state" {
		t.Fatalf("Body = %q", inputs[0].Body)
	}
}

func TestPlanBaseTaskNotificationsSkipsSelfNotification(t *testing.T) {
	inputs := planBaseTaskNotifications(
		"89ba09f0-4296-4d79-8c94-0128b2cfc3f4",
		"update",
		taskNotifyAssignee,
		map[string]string{
			"number": "TASK-000008",
			"title":  "Review incident",
		},
		map[string]string{
			"number":           "TASK-000008",
			"title":            "Review incident",
			"assigned_user_id": taskNotifyAssignee,
		},
		nil,
	)

	if len(inputs) != 0 {
		t.Fatalf("len(inputs) = %d, want 0", len(inputs))
	}
}

func TestPlanTaskNotificationsUsesTaskTableHrefForDerivedTasks(t *testing.T) {
	inputs := planTaskNotifications(
		"dw_story",
		"89ba09f0-4296-4d79-8c94-0128b2cfc3f4",
		"update",
		taskNotifyActor,
		map[string]string{
			"number": "STORY-000001",
			"title":  "Ship notification fix",
		},
		map[string]string{
			"number":           "STORY-000001",
			"title":            "Ship notification fix",
			"assigned_user_id": taskNotifyAssignee,
		},
		nil,
	)

	if len(inputs) != 1 {
		t.Fatalf("len(inputs) = %d, want 1", len(inputs))
	}
	if got := inputs[0].Href; got != "/f/dw_story/89ba09f0-4296-4d79-8c94-0128b2cfc3f4" {
		t.Fatalf("Href = %q, want %q", got, "/f/dw_story/89ba09f0-4296-4d79-8c94-0128b2cfc3f4")
	}
}
