package db

import "testing"

// The gate that decides whether a direct state write closed a task. It has to
// hold in both directions: firing on a re-close double-runs spawn rules, and
// firing on any other board move runs the whole close engine on a card drag.
// No database needed (#274).
func TestTaskStateWriteCloses(t *testing.T) {
	cases := []struct {
		name      string
		oldState  string
		newState  string
		wantClose bool
	}{
		{"pending to closed", taskStatePending, taskStateDone, true},
		{"ready to closed", taskStateReady, taskStateDone, true},
		{"skipped to closed", taskStateSkipped, taskStateDone, true},
		{"already closed", taskStateDone, taskStateDone, false},
		{"closed reopening", taskStateDone, taskStatePending, false},
		{"pending to ready", taskStatePending, taskStateReady, false},
		{"ready to in progress", taskStateReady, taskStateInProgress, false},
		{"in progress to closed", taskStateInProgress, taskStateDone, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldSnapshot := map[string]any{"state": tc.oldState}
			newSnapshot := map[string]any{"state": tc.newState}
			if got := taskStateWriteCloses(oldSnapshot, newSnapshot); got != tc.wantClose {
				t.Fatalf("taskStateWriteCloses(%q, %q) = %v, want %v", tc.oldState, tc.newState, got, tc.wantClose)
			}
		})
	}
}

// A missing or nil state column reads as not-closed, so a snapshot that failed
// to load never triggers a close.
func TestTaskStateWriteClosesEmptySnapshots(t *testing.T) {
	if taskStateWriteCloses(nil, map[string]any{"state": taskStateDone}) {
		t.Fatal("nil old snapshot treated as open, but the gate fired")
	}
	if taskStateWriteCloses(map[string]any{"state": taskStatePending}, map[string]any{}) {
		t.Fatal("empty new snapshot treated as closed")
	}
	if taskStateWriteCloses(nil, nil) {
		t.Fatal("two empty snapshots treated as a close")
	}
}
