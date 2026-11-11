package db

import "testing"

// containerRollUpReason decides what a container reports when its last child
// reaches a terminal state (#265).
func TestContainerRollUpReason(t *testing.T) {
	closed := func(reason string) childInstanceState {
		r := reason
		return childInstanceState{state: taskStateDone, reason: &r}
	}
	skipped := childInstanceState{state: taskStateSkipped}

	cases := []struct {
		name     string
		children []childInstanceState
		want     string
		wantOK   bool
	}{
		{"no children never completes", nil, "", false},
		{"open child blocks", []childInstanceState{closed("completed"), {state: taskStateReady}}, "", false},
		{"pending child blocks", []childInstanceState{{state: taskStatePending}}, "", false},
		{"all completed", []childInstanceState{closed("completed"), closed("completed")}, "completed", true},
		{"cancelled child is still success", []childInstanceState{closed("completed"), closed("cancelled")}, "completed", true},
		{"rejected outranks completed", []childInstanceState{closed("completed"), closed("rejected")}, "rejected", true},
		{"failed outranks completed", []childInstanceState{closed("completed"), closed("failed")}, "failed", true},
		{"rejected outranks failed", []childInstanceState{closed("failed"), closed("rejected")}, "rejected", true},
		{"all skipped is cancelled", []childInstanceState{skipped, skipped}, "cancelled", true},
		{"skipped plus completed is completed", []childInstanceState{skipped, closed("completed")}, "completed", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := containerRollUpReason(tc.children)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestComposeInstanceTitle(t *testing.T) {
	cases := []struct {
		title, number, want string
	}{
		{"Feature Request", "TASK-000119", "Feature Request (TASK-000119)"},
		{"Feature Request · Deploy", "TASK-000109", "Feature Request · Deploy (TASK-000109)"},
		{"Feature Request", "", "Feature Request"},
		{"Feature Request (TASK-000119)", "TASK-000119", "Feature Request (TASK-000119)"},
		{"  Deploy  ", " TASK-000007 ", "Deploy (TASK-000007)"},
	}
	for _, tc := range cases {
		if got := composeInstanceTitle(tc.title, tc.number); got != tc.want {
			t.Fatalf("composeInstanceTitle(%q, %q) = %q, want %q", tc.title, tc.number, got, tc.want)
		}
	}
}

func TestStripTitleNumber(t *testing.T) {
	cases := map[string]string{
		"Feature Request (TASK-000119)":          "Feature Request",
		"Feature Request · Deploy (TASK-000109)": "Feature Request · Deploy",
		"GH-217 Task scope never resolves":       "GH-217 Task scope never resolves",
		"Postgres Cluster (ENT-000012)":          "Postgres Cluster",
		"Feature Request":                        "Feature Request",
		"(TASK-000001)":                          "",
	}
	for in, want := range cases {
		if got := stripTitleNumber(in); got != want {
			t.Fatalf("stripTitleNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeClosureReason(t *testing.T) {
	cases := map[string]string{
		"":          "completed",
		"completed": "completed",
		"cancelled": "cancelled",
		"rejected":  "rejected",
		"failed":    "failed",
		"nonsense":  "completed",
	}
	for in, want := range cases {
		if got := normalizeClosureReason(in); got != want {
			t.Fatalf("normalizeClosureReason(%q) = %q, want %q", in, got, want)
		}
	}
}
