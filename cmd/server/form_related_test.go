package main

import (
	"testing"

	"velm/internal/db"
)

func TestConfiguredFormRelatedTargetsSortsAndFilters(t *testing.T) {
	targets := configuredFormRelatedTargets(map[string]formRelatedSectionConfig{
		"story:task_id": {
			TableName:      "story",
			ReferenceField: "task_id",
			Label:          "Stories",
		},
		"comment:task_id": {
			TableName:      "comment",
			ReferenceField: "task_id",
			Label:          "Comments",
		},
		"invalid": {
			TableName: "comment",
		},
	})

	if len(targets) != 2 {
		t.Fatalf("len(targets) = %d, want 2", len(targets))
	}
	if targets[0].TableName != "comment" || targets[0].ReferenceField != "task_id" {
		t.Fatalf("first target = %#v, want comment.task_id", targets[0])
	}
	if targets[1].TableName != "story" || targets[1].ReferenceField != "task_id" {
		t.Fatalf("second target = %#v, want story.task_id", targets[1])
	}
}

func TestFindFormRelatedColumnMatchesCaseInsensitiveName(t *testing.T) {
	column, ok := findFormRelatedColumn([]db.Column{
		{NAME: "Task_ID", LABEL: "Task"},
	}, "task_id")
	if !ok {
		t.Fatalf("expected column match")
	}
	if column.NAME != "Task_ID" {
		t.Fatalf("column.NAME = %q, want %q", column.NAME, "Task_ID")
	}
}
