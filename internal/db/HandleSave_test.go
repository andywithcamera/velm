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

func TestSnapshotState(t *testing.T) {
	if got := snapshotState(map[string]any{"state": " in_progress "}); got != "in_progress" {
		t.Fatalf("snapshotState trimmed = %q", got)
	}
	if got := snapshotState(map[string]any{"state": nil}); got != "" {
		t.Fatalf("nil state = %q", got)
	}
	if got := snapshotState(nil); got != "" {
		t.Fatalf("nil snapshot = %q", got)
	}
}

// A timestamp read back from Postgres arrives as time.Time. The trigger-record
// round trip walks every non-underscore column of the row, so one non-null
// timestamptz column used to reject the whole save with
// "unsupported script value type time.Time" — every update to an existing task
// failed on velm.dev (issue #314).
func TestApplyRuntimeTriggerRecordAcceptsTimestampFromSnapshot(t *testing.T) {
	view := View{Columns: []Column{
		{NAME: "title", DATA_TYPE: "text"},
		{NAME: "state", DATA_TYPE: "text"},
		{NAME: "state_changed_at", DATA_TYPE: "timestamptz", IS_NULLABLE: true},
	}}
	stateChangedAt := time.Date(2026, 11, 13, 18, 1, 0, 0, time.UTC)
	previous := map[string]any{
		"title":            "Replace Web Node",
		"state":            "ready",
		"state_changed_at": stateChangedAt,
	}
	current := map[string]any{
		"title":            "Replace Web Node",
		"state":            "in_progress",
		"state_changed_at": stateChangedAt,
	}
	formData := map[string]string{}
	nullColumns := map[string]bool{}

	if err := applyRuntimeTriggerRecordToFormData(view, current, previous, false, formData, nullColumns); err != nil {
		t.Fatalf("applyRuntimeTriggerRecordToFormData() error = %v, want nil", err)
	}
	if _, present := formData["state_changed_at"]; present {
		t.Fatalf("unchanged timestamp was submitted for write: %q", formData["state_changed_at"])
	}
	if got := formData["state"]; got != "in_progress" {
		t.Fatalf("state = %q, want in_progress", got)
	}
}

func TestApplyRuntimeTriggerRecordSubmitsChangedTimestamp(t *testing.T) {
	view := View{Columns: []Column{
		{NAME: "closed_at", DATA_TYPE: "timestamptz", IS_NULLABLE: true},
	}}
	previous := map[string]any{"closed_at": time.Date(2026, 11, 13, 18, 1, 0, 0, time.UTC)}
	current := map[string]any{"closed_at": time.Date(2026, 11, 13, 18, 42, 7, 0, time.UTC)}
	formData := map[string]string{}
	nullColumns := map[string]bool{}

	if err := applyRuntimeTriggerRecordToFormData(view, current, previous, false, formData, nullColumns); err != nil {
		t.Fatalf("applyRuntimeTriggerRecordToFormData() error = %v, want nil", err)
	}
	if got, want := formData["closed_at"], "2026-11-13T18:42"; got != want {
		t.Fatalf("closed_at = %q, want %q", got, want)
	}
}

// A date column keeps day precision, so a same-day value is not an edit.
func TestApplyRuntimeTriggerRecordFormatsDateColumnsByDay(t *testing.T) {
	view := View{Columns: []Column{{NAME: "due_on", DATA_TYPE: "date", IS_NULLABLE: true}}}
	previous := map[string]any{"due_on": time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)}
	current := map[string]any{"due_on": time.Date(2026, 11, 13, 17, 30, 0, 0, time.UTC)}
	formData := map[string]string{}

	if err := applyRuntimeTriggerRecordToFormData(view, current, previous, false, formData, map[string]bool{}); err != nil {
		t.Fatalf("applyRuntimeTriggerRecordToFormData() error = %v, want nil", err)
	}
	if _, present := formData["due_on"]; present {
		t.Fatalf("same-day date was treated as changed: %q", formData["due_on"])
	}
}
