package db

import (
	"testing"
	"time"
)

// The save response and the rendered form must agree on what a record's
// version looks like. These cases pin the precedence and the rendering, since
// a disagreement is exactly what makes a page announce "updates are available"
// after the reader's own save.
func TestRecordVersionValue(t *testing.T) {
	stamp := time.Date(2026, 11, 11, 21, 3, 33, 127531000, time.UTC)

	cases := []struct {
		name     string
		snapshot map[string]any
		want     string
	}{
		{"counter wins over timestamp", map[string]any{"_update_count": int64(7), "_updated_at": stamp}, "7"},
		{"counter as numeric string", map[string]any{"_update_count": "12"}, "12"},
		{"timestamp when no counter", map[string]any{"_updated_at": stamp}, "2026-11-11T21:03:33.127531Z"},
		{"timestamp string round-trips", map[string]any{"_updated_at": "2026-11-11T21:03:33.127531Z"}, "2026-11-11T21:03:33.127531Z"},
		{"timestamp without fraction", map[string]any{"_updated_at": "2026-11-11T21:03:33Z"}, "2026-11-11T21:03:33Z"},
		{"nil counter falls through", map[string]any{"_update_count": nil, "_updated_at": stamp}, "2026-11-11T21:03:33.127531Z"},
		{"empty counter falls through", map[string]any{"_update_count": "  ", "_updated_at": stamp}, "2026-11-11T21:03:33.127531Z"},
		{"nil snapshot", nil, ""},
		{"no version columns", map[string]any{"name": "x"}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RecordVersionValue(tc.snapshot); got != tc.want {
				t.Fatalf("RecordVersionValue() = %q, want %q", got, tc.want)
			}
		})
	}
}
