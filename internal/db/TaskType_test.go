package db

import (
	"context"
	"testing"
)

func TestTaskTypeValueForBaseTask(t *testing.T) {
	if got := TaskTypeValueForTable(context.Background(), "base_task"); got != "TASK" {
		t.Fatalf("TaskTypeValueForTable(base_task) = %q, want %q", got, "TASK")
	}
}

func TestNormalizeTaskTypeValue(t *testing.T) {
	if got := normalizeTaskTypeValue("change_request"); got != "CHANGE REQUEST" {
		t.Fatalf("normalizeTaskTypeValue(change_request) = %q", got)
	}
}
