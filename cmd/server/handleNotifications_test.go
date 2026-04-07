package main

import (
	"testing"
	"time"

	"velm/internal/db"
)

func TestBuildNotificationMenuItemsPreservesNotificationIDAndUnreadState(t *testing.T) {
	items := buildNotificationMenuItems([]db.UserNotification{
		{
			ID:        "notif-1",
			Title:     "Assigned",
			Body:      "TASK-000001 needs attention",
			Href:      "/f/base_task/record-1",
			Level:     "info",
			CreatedAt: time.Date(2026, 3, 25, 12, 30, 0, 0, time.UTC),
			IsRead:    false,
		},
		{
			ID:        "notif-2",
			Title:     "Updated",
			CreatedAt: time.Date(2026, 3, 25, 12, 31, 0, 0, time.UTC),
			IsRead:    true,
		},
	})

	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].ID != "notif-1" {
		t.Fatalf("items[0].ID = %q, want %q", items[0].ID, "notif-1")
	}
	if !items[0].IsUnread {
		t.Fatal("expected first item to be unread")
	}
	if items[1].IsUnread {
		t.Fatal("expected second item to be read")
	}
}

func TestNotificationRedirectTargetUsesHrefWhenPresent(t *testing.T) {
	if got := notificationRedirectTarget("/f/base_task/123", "/task"); got != "/f/base_task/123" {
		t.Fatalf("notificationRedirectTarget(href) = %q, want %q", got, "/f/base_task/123")
	}
}

func TestNotificationRedirectTargetFallsBackToReturnTo(t *testing.T) {
	if got := notificationRedirectTarget("", "/task"); got != "/task" {
		t.Fatalf("notificationRedirectTarget(return_to) = %q, want %q", got, "/task")
	}
}
