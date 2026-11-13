package main

import (
	"testing"
	"time"
)

// A list cell used to show fmt's rendering of a time.Time —
// "2026-11-13 10:44:29.999357 +0000 UTC" — which wraps in a narrow column and
// carries microseconds and a Go zone name nobody asked for.
func TestFormatListCellTimestamp(t *testing.T) {
	stamp := time.Date(2026, 11, 13, 10, 44, 29, 999357000, time.UTC)
	if got, want := formatListCellTimestamp(stamp), "13 Nov 2026, 10:44"; got != want {
		t.Fatalf("formatListCellTimestamp = %q, want %q", got, want)
	}
	if got := formatListCellTimestamp(time.Time{}); got != "" {
		t.Fatalf("zero time formatted as %q, want empty", got)
	}
}

// The same value already stringified by fmt must be recognised and rewritten,
// which is what the paths that hand over text rather than a time.Time need.
func TestReformatListCellTimestamp(t *testing.T) {
	for _, raw := range []string{
		"2026-11-13 10:44:29.999357 +0000 UTC",
		"2026-11-13 10:44:29.999357 +0000",
		"2026-11-13 10:44:29 +0000 UTC",
		"2026-11-13T10:44:29Z",
	} {
		got, ok := reformatListCellTimestamp(raw)
		if !ok {
			t.Fatalf("reformatListCellTimestamp(%q) did not recognise a timestamp", raw)
		}
		if got != "13 Nov 2026, 10:44" {
			t.Fatalf("reformatListCellTimestamp(%q) = %q, want %q", raw, got, "13 Nov 2026, 10:44")
		}
	}
}

// Ordinary text must be left exactly as it was: a value that merely starts with
// a date is not the same thing as a timestamp column.
func TestReformatListCellTimestampLeavesProseAlone(t *testing.T) {
	for _, raw := range []string{
		"lb-01",
		"",
		"Deployed 2026-11-13 to production",
		"2026-11-13",
		"30 GiB NVMe",
	} {
		got, ok := reformatListCellTimestamp(raw)
		if ok {
			t.Fatalf("reformatListCellTimestamp(%q) claimed a timestamp (%q)", raw, got)
		}
		if got != raw {
			t.Fatalf("reformatListCellTimestamp(%q) rewrote to %q", raw, got)
		}
	}
}
