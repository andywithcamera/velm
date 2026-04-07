package main

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"velm/internal/db"
)

func TestBuildExpectedRecordVersionValueUsesUpdateCountWhenPresent(t *testing.T) {
	got := buildExpectedRecordVersionValue(map[string]any{
		"_update_count": int64(7),
	})
	if got != "7" {
		t.Fatalf("buildExpectedRecordVersionValue() = %q, want %q", got, "7")
	}
}

func TestBuildExpectedRecordVersionValueFallsBackToTimestampPrecision(t *testing.T) {
	value := time.Date(2026, time.March, 18, 22, 31, 14, 123456000, time.UTC)

	got := buildExpectedRecordVersionValue(map[string]any{
		"_updated_at": value,
	})

	want := value.Format(time.RFC3339Nano)
	if got != want {
		t.Fatalf("buildExpectedRecordVersionValue() = %q, want %q", got, want)
	}
}

func TestBuildExpectedRecordVersionValueHandlesMissingValue(t *testing.T) {
	got := buildExpectedRecordVersionValue(map[string]any{})
	if got != "" {
		t.Fatalf("buildExpectedRecordVersionValue() = %q, want empty string", got)
	}
}

func TestInferReferenceTableUsesBuiltinAdminTables(t *testing.T) {
	tests := []struct {
		columnName string
		want       string
	}{
		{columnName: "user_id", want: "_user"},
		{columnName: "group_id", want: "_group"},
		{columnName: "role_id", want: "_role"},
		{columnName: "permission_id", want: "_permission"},
	}

	for _, tt := range tests {
		got := inferReferenceTable("_group_role", tt.columnName)
		if got != tt.want {
			t.Fatalf("inferReferenceTable(%q) = %q, want %q", tt.columnName, got, tt.want)
		}
	}
}

func TestInferReferenceTableUsesBaseAliasesAndPrefixes(t *testing.T) {
	tests := []struct {
		columnName string
		want       string
	}{
		{columnName: "entity_id", want: "base_entity"},
		{columnName: "source_entity_id", want: "base_entity"},
		{columnName: "target_entity_id", want: "base_entity"},
		{columnName: "owner_entity_id", want: "base_entity"},
		{columnName: "responsible_user_id", want: "_user"},
	}

	for _, tt := range tests {
		got := inferReferenceTable("base_entity_relationship", tt.columnName)
		if got != tt.want {
			t.Fatalf("inferReferenceTable(%q) = %q, want %q", tt.columnName, got, tt.want)
		}
	}
}

func TestPreferredDisplayColumnUsesExplicitDisplayField(t *testing.T) {
	view := db.View{
		Table: &db.Table{
			NAME:          "_user",
			DISPLAY_FIELD: "email",
		},
		Columns: []db.Column{
			{NAME: "_id"},
			{NAME: "name"},
			{NAME: "email"},
		},
	}

	if got := preferredDisplayColumn(view); got != "email" {
		t.Fatalf("preferredDisplayColumn() = %q, want %q", got, "email")
	}
}

func TestPreferredDisplayColumnFallsBackToInferredField(t *testing.T) {
	view := db.View{
		Table: &db.Table{
			NAME: "_group",
		},
		Columns: []db.Column{
			{NAME: "_id"},
			{NAME: "name"},
			{NAME: "description"},
		},
	}

	if got := preferredDisplayColumn(view); got != "name" {
		t.Fatalf("preferredDisplayColumn() = %q, want %q", got, "name")
	}
}

func TestBuildFormFieldConfigsMarksBaseTaskLifecycleFieldsReadOnly(t *testing.T) {
	columns := []db.Column{
		{NAME: "work_type", DATA_TYPE: "text", IS_NULLABLE: false, LABEL: "Task Type"},
		{NAME: "state_changed_at", DATA_TYPE: "timestamp", IS_NULLABLE: false, LABEL: "State Last Changed"},
		{NAME: "started_at", DATA_TYPE: "timestamp", IS_NULLABLE: true, LABEL: "Started At"},
		{NAME: "closed_at", DATA_TYPE: "timestamp", IS_NULLABLE: true, LABEL: "Closed At"},
	}

	configs := buildFormFieldConfigs(context.Background(), "base_task", columns)
	for _, field := range []string{"work_type", "state_changed_at", "started_at", "closed_at"} {
		cfg, ok := configs[field]
		if !ok {
			t.Fatalf("expected %s config", field)
		}
		if !cfg.ReadOnly {
			t.Fatalf("expected base_task.%s to be read-only", field)
		}
	}
}

func TestApplySpecialFormDefaultsSetsBaseTaskStateChangedAt(t *testing.T) {
	values := map[string]string{}

	applySpecialFormDefaults("base_task", values)

	if got := values["work_type"]; got != "TASK" {
		t.Fatalf("expected work_type default TASK, got %q", got)
	}
	raw := values["state_changed_at"]
	if raw == "" {
		t.Fatal("expected state_changed_at default")
	}
	if _, err := time.Parse("2006-01-02T15:04", raw); err != nil {
		t.Fatalf("expected datetime-local formatted default, got %q: %v", raw, err)
	}
}

func TestBuildAppEditorRedirectTargetUsesSavedActiveObject(t *testing.T) {
	form := map[string][]string{
		"return_to": {"/admin/app-editor?app=base&active=new%3Acolumn"},
	}

	target := buildAppEditorRedirectTarget(form, "base", "column:base_task:number")
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse redirect target: %v", err)
	}
	if got := parsed.Query().Get("active"); got != "column:base_task:number" {
		t.Fatalf("expected saved active object, got %q", got)
	}
	if got := parsed.Query().Get("app"); got != "base" {
		t.Fatalf("expected app to remain base, got %q", got)
	}
}

func TestFormReferenceOptionMarshalsLowercaseKeys(t *testing.T) {
	raw, err := json.Marshal(formReferenceOption{
		Value: "abc123",
		Label: "Example",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(raw) != `{"value":"abc123","label":"Example"}` {
		t.Fatalf("unexpected reference option json: %s", string(raw))
	}
}

func TestBuildFormSecurityPreviewResponseForDeniedExistingRecord(t *testing.T) {
	response := buildFormSecurityPreviewResponse(db.TableSecuritySavePreview{
		SaveAllowed:   false,
		RecordAllowed: false,
	}, false)

	if response.SaveAllowed {
		t.Fatal("expected save to remain denied")
	}
	if response.Message != "You can no longer save this record with the current values." {
		t.Fatalf("message = %q", response.Message)
	}
}

func TestBuildFormSecurityPreviewResponseForSingleBlockedField(t *testing.T) {
	response := buildFormSecurityPreviewResponse(db.TableSecuritySavePreview{
		SaveAllowed:   false,
		RecordAllowed: true,
		BlockedFields: []string{"state"},
	}, false)

	if response.Message != "You can no longer save changes to state." {
		t.Fatalf("message = %q", response.Message)
	}
	if len(response.BlockedFields) != 1 || response.BlockedFields[0] != "state" {
		t.Fatalf("blocked fields = %#v", response.BlockedFields)
	}
}

func TestParseFormSecurityPreviewValuesSkipsTransportFields(t *testing.T) {
	formData, nullColumns, err := parseFormSecurityPreviewValues(context.Background(), []db.Column{
		{NAME: "name", DATA_TYPE: "text"},
		{NAME: "active", DATA_TYPE: "bool"},
		{NAME: "due_at", DATA_TYPE: "timestamp", IS_NULLABLE: true},
	}, "base_task", "", false, url.Values{
		"table_name":       []string{"base_task"},
		"record_id":        []string{"123"},
		"form_name":        []string{"manager"},
		"_id":              []string{"123"},
		"csrf_token":       []string{"token"},
		"expected_version": []string{"7"},
		"name":             []string{"Example"},
		"active":           []string{"on"},
		"due_at":           []string{""},
	})
	if err != nil {
		t.Fatalf("parseFormSecurityPreviewValues() error = %v", err)
	}

	if got := len(formData); got != 3 {
		t.Fatalf("len(formData) = %d, want 3", got)
	}
	if formData["name"] != "Example" {
		t.Fatalf("name = %q", formData["name"])
	}
	if formData["active"] != "true" {
		t.Fatalf("active = %q, want true", formData["active"])
	}
	if formData["due_at"] != "" {
		t.Fatalf("due_at = %q, want empty string", formData["due_at"])
	}
	if !nullColumns["due_at"] {
		t.Fatalf("expected due_at to be tracked as null")
	}
	if _, ok := formData["csrf_token"]; ok {
		t.Fatalf("unexpected transport field in form data: %#v", formData)
	}
	if _, ok := formData["form_name"]; ok {
		t.Fatalf("unexpected form transport field in form data: %#v", formData)
	}
}

func TestBuildFormVariantOptionsMarksSelectedVariant(t *testing.T) {
	options := buildFormVariantOptions("base_task", "record-1", db.AppDefinitionForm{Name: "manager"}, []db.AppDefinitionForm{
		{Name: "default", Label: "Default Form"},
		{Name: "manager", Label: "Manager Review"},
	})

	if len(options) != 2 {
		t.Fatalf("len(options) = %d, want 2", len(options))
	}
	if options[0].Selected {
		t.Fatalf("expected default form to be unselected: %#v", options)
	}
	if !options[1].Selected {
		t.Fatalf("expected manager form to be selected: %#v", options)
	}
	if options[1].Href != "/f/base_task/record-1?form=manager" {
		t.Fatalf("manager href = %q", options[1].Href)
	}
}

func TestRecordFormHrefOmitsDefaultQuery(t *testing.T) {
	if got := recordFormHref("base_task", "record-1", "default"); got != "/f/base_task/record-1" {
		t.Fatalf("recordFormHref() = %q, want default form omitted", got)
	}
}
