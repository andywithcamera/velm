package db

import (
	"database/sql"
	"testing"
)

func TestConditionMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule sql.NullString
		data map[string]string
		want bool
	}{
		{
			name: "no rule",
			rule: sql.NullString{Valid: false},
			data: map[string]string{"status": "open"},
			want: true,
		},
		{
			name: "equals match",
			rule: sql.NullString{Valid: true, String: "status=closed"},
			data: map[string]string{"status": "closed"},
			want: true,
		},
		{
			name: "equals mismatch",
			rule: sql.NullString{Valid: true, String: "status=closed"},
			data: map[string]string{"status": "open"},
			want: false,
		},
		{
			name: "not equals match",
			rule: sql.NullString{Valid: true, String: "priority!=low"},
			data: map[string]string{"priority": "high"},
			want: true,
		},
		{
			name: "not equals mismatch",
			rule: sql.NullString{Valid: true, String: "priority!=low"},
			data: map[string]string{"priority": "low"},
			want: false,
		},
		{
			name: "and expression",
			rule: sql.NullString{Valid: true, String: "status=closed && reason!=empty"},
			data: map[string]string{"status": "closed", "reason": "fixed"},
			want: true,
		},
		{
			name: "empty mismatch",
			rule: sql.NullString{Valid: true, String: "reason=empty"},
			data: map[string]string{"reason": "value"},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := conditionMatches(tt.rule, tt.data)
			if got != tt.want {
				t.Fatalf("conditionMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}
