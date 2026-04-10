package main

import (
	"strings"
	"testing"
	"time"

	"velm/internal/db"
)

func TestBuildFormTimelineItemsMergesAndSortsEntries(t *testing.T) {
	changeTime := time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)
	commentTime := changeTime.Add(5 * time.Minute)

	items := buildFormTimelineItems(
		[]db.RecordChangeEntry{{
			CreatedAt: changeTime,
			UserName:  "Andy Doyle",
			UserEmail: "test@andydoyle.ie",
			Operation: "update",
			FieldDiff: map[string]db.RecordFieldDiff{
				"email": {Old: "old@example.com", New: "new@example.com"},
			},
		}},
		[]db.RecordCommentEntry{{
			CreatedAt: commentTime,
			UserName:  "Alejandra Bolanos",
			Body:      "Looks good to me.",
		}},
		map[string]string{"email": "Email"},
	)

	if len(items) != 2 {
		t.Fatalf("expected 2 timeline items, got %d", len(items))
	}

	if items[0].Kind != "comment" {
		t.Fatalf("expected newest item to be comment, got %q", items[0].Kind)
	}
	if items[0].CommentBody != "Looks good to me." {
		t.Fatalf("unexpected comment body %q", items[0].CommentBody)
	}

	if items[1].Kind != "update" {
		t.Fatalf("expected second item to be update, got %q", items[1].Kind)
	}
	if items[1].Summary != "updated 1 field" {
		t.Fatalf("unexpected update summary %q", items[1].Summary)
	}
	if len(items[1].Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(items[1].Changes))
	}
	if items[1].Changes[0].Label != "Email" {
		t.Fatalf("expected change label Email, got %q", items[1].Changes[0].Label)
	}
}

func TestSummarizeTimelineValue(t *testing.T) {
	if got := summarizeTimelineValue(""); got != "Empty" {
		t.Fatalf("expected Empty for blank value, got %q", got)
	}

	got := summarizeTimelineValue("line one\nline two")
	if got != "line one line two" {
		t.Fatalf("expected newlines collapsed, got %q", got)
	}

	longValue := strings.Repeat("a", 130)
	got = summarizeTimelineValue(longValue)
	if len(got) != 120 {
		t.Fatalf("expected truncated value length 120, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncated value to end with ellipsis, got %q", got)
	}
}
