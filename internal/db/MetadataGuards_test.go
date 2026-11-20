package db

import (
	"context"
	"strings"
	"testing"
)

func TestValidateBuilderColumnDataType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "text", input: "text", wantErr: false},
		{name: "varchar", input: "varchar(255)", wantErr: false},
		{name: "enum", input: "enum:new|in_progress|done", wantErr: false},
		{name: "reference", input: "reference", wantErr: false},
		{name: "choice", input: "choice", wantErr: false},
		{name: "autnumber", input: "autnumber", wantErr: false},
		{name: "autonumber alias", input: "autonumber", wantErr: false},
		{name: "email", input: "email", wantErr: false},
		{name: "url", input: "url", wantErr: false},
		{name: "phone", input: "phone", wantErr: false},
		{name: "jsonb", input: "jsonb", wantErr: false},
		{name: "bad varchar", input: "varchar(0)", wantErr: true},
		{name: "bad enum", input: "enum:open", wantErr: true},
		{name: "unsupported", input: "xml", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateBuilderColumnDataType(tt.input)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for %q", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
		})
	}
}

func TestValidateAutoNumberPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid", input: "TASK", wantErr: false},
		{name: "valid short", input: "ENT", wantErr: false},
		{name: "normalized trim", input: " TASK ", wantErr: false},
		{name: "too short", input: "TS", wantErr: true},
		{name: "lowercase", input: "task", wantErr: false},
		{name: "mixed chars", input: "T4SK", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateAutoNumberPrefix(normalizeAutoNumberPrefix(tt.input))
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for %q", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
		})
	}
}

func TestIsProtectedTableName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "system prefix", input: "_page", want: true},
		{name: "core table", input: "_work", want: true},
		{name: "mfa factor table", input: "_user_auth_factor", want: true},
		{name: "legacy script table name", input: "script_def", want: false},
		{name: "app table", input: "customer_ticket", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isProtectedTableName(tt.input)
			if got != tt.want {
				t.Fatalf("isProtectedTableName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestApplyBaseTaskAssignmentRulesBlanksAssigneeOnGroupChange(t *testing.T) {
	formData := map[string]string{
		"assignment_group_id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		"assigned_user_id":    "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	}
	nullColumns := map[string]bool{}
	old := map[string]any{
		"state":               "pending",
		"assignment_group_id": "cccccccc-cccc-cccc-cccc-cccccccccccc",
		"assigned_user_id":    "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	}
	if err := applyBaseTaskAssignmentRules(formData, nullColumns, old); err != nil {
		t.Fatalf("pending group change refused: %v", err)
	}
	if !nullColumns["assigned_user_id"] {
		t.Fatal("assignee was not blanked when the group changed")
	}
	if formData["assigned_user_id"] != "" {
		t.Fatalf("assignee form value = %q, want empty", formData["assigned_user_id"])
	}
}

func TestApplyBaseTaskAssignmentRulesLeavesUnchangedGroupAlone(t *testing.T) {
	formData := map[string]string{
		"assignment_group_id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		"assigned_user_id":    "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	}
	nullColumns := map[string]bool{}
	old := map[string]any{
		"state":               "pending",
		"assignment_group_id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		"assigned_user_id":    "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	}
	if err := applyBaseTaskAssignmentRules(formData, nullColumns, old); err != nil {
		t.Fatalf("unchanged group refused: %v", err)
	}
	if nullColumns["assigned_user_id"] {
		t.Fatal("assignee was blanked without a group change")
	}
	if formData["assigned_user_id"] == "" {
		t.Fatal("assignee was cleared without a group change")
	}
}

func TestApplyBaseTaskAssignmentRulesLocksGroupPastPending(t *testing.T) {
	for _, state := range []string{"ready", "in_progress", "closed"} {
		t.Run(state, func(t *testing.T) {
			formData := map[string]string{
				"assignment_group_id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
				"assigned_user_id":    "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			}
			nullColumns := map[string]bool{}
			old := map[string]any{
				"state":               state,
				"assignment_group_id": "cccccccc-cccc-cccc-cccc-cccccccccccc",
			}
			err := applyBaseTaskAssignmentRules(formData, nullColumns, old)
			if err == nil {
				t.Fatalf("group change on a %s task was accepted", state)
			}
			if !strings.Contains(err.Error(), "read-only") {
				t.Fatalf("message %q does not say read-only", err)
			}
		})
	}
}

func TestApplyBaseTaskAssignmentRulesIgnoresAbsentControl(t *testing.T) {
	// A read-only group renders as a disabled select, which the browser omits
	// from the POST. That must not read as "cleared", or every other edit on a
	// started task would be refused.
	formData := map[string]string{
		"title":            "Assignment guard probe",
		"assigned_user_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	}
	nullColumns := map[string]bool{}
	old := map[string]any{
		"state":               "in_progress",
		"assignment_group_id": "cccccccc-cccc-cccc-cccc-cccccccccccc",
		"assigned_user_id":    "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	}
	if err := applyBaseTaskAssignmentRules(formData, nullColumns, old); err != nil {
		t.Fatalf("an unrelated edit on a started task was refused: %v", err)
	}
	if nullColumns["assigned_user_id"] {
		t.Fatal("assignee was blanked although no group was submitted")
	}
}

func TestValidateMetadataWriteRejectsTaskAssigneeWithoutGroup(t *testing.T) {
	t.Parallel()

	err := ValidateMetadataWrite(context.Background(), "base_task", "", map[string]string{
		"assigned_user_id": "550e8400-e29b-41d4-a716-446655440000",
	})
	if err == nil {
		t.Fatal("expected assignment validation error")
	}
	if got := err.Error(); got != "assigned_user_id requires assignment_group_id" {
		t.Fatalf("unexpected error %q", got)
	}
}
