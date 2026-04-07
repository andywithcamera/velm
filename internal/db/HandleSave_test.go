package db

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestSubmittedFormHasChangesReturnsFalseWhenValuesMatchSnapshot(t *testing.T) {
	columns := []Column{
		{NAME: "status", DATA_TYPE: "text"},
		{NAME: "due_at", DATA_TYPE: "timestamp", IS_NULLABLE: true},
		{NAME: "effort", DATA_TYPE: "int"},
	}
	snapshot := map[string]any{
		"status": "open",
		"due_at": time.Date(2026, 3, 16, 14, 30, 0, 0, time.UTC),
		"effort": int64(3),
	}
	formData := map[string]string{
		"status": "open",
		"due_at": "2026-03-16T14:30",
		"effort": "3",
	}

	if submittedFormHasChanges(columns, formData, map[string]bool{}, snapshot) {
		t.Fatal("expected unchanged submission to be treated as no-op")
	}
}

func TestSubmittedFormHasChangesReturnsFalseForBlankNullableTypedField(t *testing.T) {
	columns := []Column{
		{NAME: "due_at", DATA_TYPE: "timestamp", IS_NULLABLE: true},
		{NAME: "assigned_to", DATA_TYPE: "uuid", IS_NULLABLE: true},
		{NAME: "state", DATA_TYPE: "choice", IS_NULLABLE: true},
	}
	snapshot := map[string]any{
		"due_at":      nil,
		"assigned_to": nil,
		"state":       nil,
	}
	formData := map[string]string{
		"due_at":      "",
		"assigned_to": "",
		"state":       "",
	}
	nullColumns := map[string]bool{
		"due_at":      true,
		"assigned_to": true,
		"state":       true,
	}

	if submittedFormHasChanges(columns, formData, nullColumns, snapshot) {
		t.Fatal("expected blank nullable typed fields to be treated as unchanged")
	}
}

func TestSubmittedFormHasChangesReturnsTrueWhenAFieldDiffers(t *testing.T) {
	columns := []Column{
		{NAME: "status", DATA_TYPE: "text"},
		{NAME: "priority", DATA_TYPE: "text"},
	}
	snapshot := map[string]any{
		"status":   "open",
		"priority": "low",
	}
	formData := map[string]string{
		"status":   "open",
		"priority": "high",
	}

	if !submittedFormHasChanges(columns, formData, map[string]bool{}, snapshot) {
		t.Fatal("expected differing submission to require a save")
	}
}

func TestIsSaveTransportField(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  bool
	}{
		{name: "csrf", field: "csrf_token", want: true},
		{name: "expected version", field: "expected_version", want: true},
		{name: "expected updated at", field: "expected_updated_at", want: true},
		{name: "realtime client id", field: "realtime_client_id", want: true},
		{name: "form name", field: "form_name", want: true},
		{name: "normal field", field: "name", want: false},
		{name: "blank", field: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSaveTransportField(tt.field); got != tt.want {
				t.Fatalf("isSaveTransportField(%q) = %v, want %v", tt.field, got, tt.want)
			}
		})
	}
}

func TestClassifySaveDatabaseErrorReturnsBadRequestForPgExceptions(t *testing.T) {
	message, status, ok := classifySaveDatabaseError(&pgconn.PgError{
		Code:    "P0001",
		Message: "assigned_user_id requires assignment_group_id",
	})
	if !ok {
		t.Fatal("expected pg error to be classified")
	}
	if status != 400 {
		t.Fatalf("expected status 400, got %d", status)
	}
	if message != "assigned_user_id requires assignment_group_id" {
		t.Fatalf("unexpected message %q", message)
	}
}

func TestRecordFormRedirectTargetPreservesNamedForm(t *testing.T) {
	if got := recordFormRedirectTarget("base_task", "record-1", "manager"); got != "/f/base_task/record-1?form=manager" {
		t.Fatalf("recordFormRedirectTarget() = %q, want named form query", got)
	}
}

func TestRecordFormRedirectTargetOmitsDefaultForm(t *testing.T) {
	if got := recordFormRedirectTarget("base_task", "record-1", "default"); got != "/f/base_task/record-1" {
		t.Fatalf("recordFormRedirectTarget() = %q, want default form omitted", got)
	}
}
