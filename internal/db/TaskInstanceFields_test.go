package db

import "testing"

func TestInstanceDescription(t *testing.T) {
	def := &TaskDefinition{Description: "Produce a patch artifact on a branch."}

	cases := []struct {
		name   string
		inputs map[string]any
		want   string
	}{
		{"nil inputs falls back to the definition", nil, "Produce a patch artifact on a branch."},
		{"empty body falls back to the definition", map[string]any{"body": "   "}, "Produce a patch artifact on a branch."},
		{"body leads, purpose follows", map[string]any{"body": "Add delta sync to the importer."},
			"Add delta sync to the importer.\n\nProduce a patch artifact on a branch."},
		{"non-string body is ignored", map[string]any{"body": 42}, "Produce a patch artifact on a branch."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := instanceDescription(def, tc.inputs); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}

	if got := instanceDescription(&TaskDefinition{}, map[string]any{"body": "Only the ask."}); got != "Only the ask." {
		t.Fatalf("body without a definition description: got %q", got)
	}
	if got := instanceDescription(nil, map[string]any{"body": "x"}); got != "" {
		t.Fatalf("nil definition: got %q", got)
	}
	if got := instanceDescription(&TaskDefinition{}, nil); got != "" {
		t.Fatalf("nothing to say should stay empty, got %q", got)
	}
}

func TestInstancePriority(t *testing.T) {
	cases := []struct {
		inputs map[string]any
		want   string
	}{
		{nil, "medium"},
		{map[string]any{"priority": "high"}, "high"},
		{map[string]any{"priority": "  HIGH  "}, "high"},
		{map[string]any{"priority": "very_high"}, "very_high"},
		// Unknown values would violate chk_base_task_priority and fail the
		// whole spawn, so they fall back instead.
		{map[string]any{"priority": "urgent"}, "medium"},
		{map[string]any{"priority": 3}, "medium"},
	}
	for _, tc := range cases {
		if got := instancePriority(tc.inputs); got != tc.want {
			t.Fatalf("inputs %v: got %q want %q", tc.inputs, got, tc.want)
		}
	}
}

func TestInstancePlannedStart(t *testing.T) {
	if got := instancePlannedStart(nil); got != nil {
		t.Fatalf("no window_start should leave planned_start_at empty, got %v", got)
	}
	got := instancePlannedStart(map[string]any{"window_start": "2026-11-20T22:00:00Z"})
	if got == nil {
		t.Fatal("RFC3339 window_start was not parsed")
	}
	got = instancePlannedStart(map[string]any{"window_start": "2026-11-20"})
	if got == nil {
		t.Fatal("date-only window_start was not parsed")
	}
	// An unparseable value must not fail the spawn.
	if got := instancePlannedStart(map[string]any{"window_start": "next tuesday"}); got != nil {
		t.Fatalf("garbage window_start should be ignored, got %v", got)
	}
}

func TestDescriptionIsEngineAuthored(t *testing.T) {
	def := &TaskDefinition{Description: "Run the suite against a specific commit."}
	cases := []struct {
		current string
		want    bool
	}{
		{"", true},
		{"   ", true},
		{"Run the suite against a specific commit.", true},
		{"Run the suite against a specific commit.  ", true},
		{"Mine: run only the tenant tests.", false},
	}
	for _, tc := range cases {
		if got := descriptionIsEngineAuthored(tc.current, def); got != tc.want {
			t.Fatalf("current %q: got %v want %v", tc.current, got, tc.want)
		}
	}
	if !descriptionIsEngineAuthored("", nil) {
		t.Fatal("empty description is always fillable")
	}
}

func TestNonNilInputs(t *testing.T) {
	if got := nonNilInputs(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil inputs should normalise to an empty map, got %#v", got)
	}
	in := map[string]any{"a": 1}
	if got := nonNilInputs(in); len(got) != 1 {
		t.Fatalf("existing inputs should pass through, got %#v", got)
	}
}
