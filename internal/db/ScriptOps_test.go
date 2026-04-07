package db

import (
	"reflect"
	"testing"
)

func TestBuildScriptWhereClauseAddsSafeFilters(t *testing.T) {
	view := View{
		Table: &Table{NAME: "itsm_software"},
		Columns: []Column{
			{NAME: "_id", DATA_TYPE: "uuid"},
			{NAME: "computer_id", DATA_TYPE: "uuid"},
			{NAME: "status", DATA_TYPE: "text"},
			{NAME: "_deleted_at", DATA_TYPE: "timestamptz"},
		},
	}

	query := ScriptRecordQuery{
		IDs: []string{"550e8400-e29b-41d4-a716-446655440000"},
		Equals: map[string]any{
			"computer_id": "550e8400-e29b-41d4-a716-446655440001",
			"status":      "retired",
		},
	}

	gotClause, gotArgs, err := buildScriptWhereClause(view, query, 1)
	if err != nil {
		t.Fatalf("buildScriptWhereClause() error = %v", err)
	}

	wantClause := `"_deleted_at" IS NULL AND "_id" IN ($1) AND "computer_id" = $2::uuid AND "status" = $3`
	if gotClause != wantClause {
		t.Fatalf("buildScriptWhereClause() clause = %q, want %q", gotClause, wantClause)
	}

	wantArgs := []any{
		"550e8400-e29b-41d4-a716-446655440000",
		"550e8400-e29b-41d4-a716-446655440001",
		"retired",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("buildScriptWhereClause() args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestBuildScriptWhereClauseSupportsAdvancedFilters(t *testing.T) {
	view := View{
		Table: &Table{NAME: "task_item"},
		Columns: []Column{
			{NAME: "_id", DATA_TYPE: "uuid"},
			{NAME: "state", DATA_TYPE: "text"},
			{NAME: "due_date", DATA_TYPE: "timestamptz"},
			{NAME: "_deleted_at", DATA_TYPE: "timestamptz"},
		},
	}

	query := ScriptRecordQuery{
		Filters: []ScriptRecordFilter{
			{Column: "state", Operator: "in", Value: []any{"open", "pending"}},
			{Column: "due_date", Operator: "<", Value: "2026-03-21T00:00:00Z"},
		},
	}

	gotClause, gotArgs, err := buildScriptWhereClause(view, query, 1)
	if err != nil {
		t.Fatalf("buildScriptWhereClause() error = %v", err)
	}

	wantClause := `"_deleted_at" IS NULL AND "state" IN ($1, $2) AND "due_date" < $3::timestamptz`
	if gotClause != wantClause {
		t.Fatalf("buildScriptWhereClause() clause = %q, want %q", gotClause, wantClause)
	}

	wantArgs := []any{"open", "pending", "2026-03-21T00:00:00Z"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("buildScriptWhereClause() args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestBuildScriptOrderClauseSupportsDirection(t *testing.T) {
	view := View{
		Table: &Table{NAME: "task_item"},
		Columns: []Column{
			{NAME: "_id", DATA_TYPE: "uuid"},
			{NAME: "due_date", DATA_TYPE: "timestamptz"},
			{NAME: "priority", DATA_TYPE: "integer"},
		},
	}

	orderClause, err := buildScriptOrderClause(view, ScriptRecordQuery{
		OrderBy: []ScriptRecordOrder{
			{Column: "due_date", Direction: "asc"},
			{Column: "priority", Direction: "desc"},
		},
	})
	if err != nil {
		t.Fatalf("buildScriptOrderClause() error = %v", err)
	}

	wantClause := `"due_date" ASC, "priority" DESC`
	if orderClause != wantClause {
		t.Fatalf("buildScriptOrderClause() = %q, want %q", orderClause, wantClause)
	}
}

func TestBuildScriptWhereClauseSupportsGroupedOrFilters(t *testing.T) {
	view := View{
		Table: &Table{NAME: "task_item"},
		Columns: []Column{
			{NAME: "_id", DATA_TYPE: "uuid"},
			{NAME: "state", DATA_TYPE: "text"},
			{NAME: "assigned_to", DATA_TYPE: "uuid"},
			{NAME: "active", DATA_TYPE: "boolean"},
			{NAME: "_deleted_at", DATA_TYPE: "timestamptz"},
		},
	}

	query := ScriptRecordQuery{
		Filters: []ScriptRecordFilter{
			{Column: "active", Operator: "=", Value: true},
		},
		Groups: []ScriptRecordFilterGroup{
			{
				Mode: "any",
				Filters: []ScriptRecordFilter{
					{Column: "state", Operator: "in", Value: []any{"open", "pending"}},
					{Column: "assigned_to", Operator: "=", Value: "550e8400-e29b-41d4-a716-446655440001"},
				},
			},
		},
	}

	gotClause, gotArgs, err := buildScriptWhereClause(view, query, 1)
	if err != nil {
		t.Fatalf("buildScriptWhereClause() error = %v", err)
	}

	wantClause := `"_deleted_at" IS NULL AND "active" = $1::boolean AND ("state" IN ($2, $3) OR "assigned_to" = $4::uuid)`
	if gotClause != wantClause {
		t.Fatalf("buildScriptWhereClause() clause = %q, want %q", gotClause, wantClause)
	}

	wantArgs := []any{"true", "open", "pending", "550e8400-e29b-41d4-a716-446655440001"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("buildScriptWhereClause() args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestBuildScriptSetClausesNormalizesMetadata(t *testing.T) {
	view := View{
		Table: &Table{NAME: "itsm_software"},
		Columns: []Column{
			{NAME: "status", DATA_TYPE: "text"},
			{NAME: "retired", DATA_TYPE: "boolean"},
			{NAME: "_updated_at", DATA_TYPE: "timestamptz"},
			{NAME: "_updated_by", DATA_TYPE: "uuid"},
		},
	}

	setParts, args, err := buildScriptSetClauses(
		view,
		map[string]any{
			"status":  "retired",
			"retired": true,
		},
		"550e8400-e29b-41d4-a716-446655440002",
		"2026-03-15T12:00:00Z",
		1,
	)
	if err != nil {
		t.Fatalf("buildScriptSetClauses() error = %v", err)
	}

	wantParts := []string{
		`"_updated_at" = $1::timestamptz`,
		`"_updated_by" = $2::uuid`,
		`"retired" = $3::boolean`,
		`"status" = $4`,
	}
	if !reflect.DeepEqual(setParts, wantParts) {
		t.Fatalf("buildScriptSetClauses() parts = %#v, want %#v", setParts, wantParts)
	}

	wantArgs := []any{
		"2026-03-15T12:00:00Z",
		"550e8400-e29b-41d4-a716-446655440002",
		"true",
		"retired",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("buildScriptSetClauses() args = %#v, want %#v", args, wantArgs)
	}
}

func TestBuildScriptSetClausesAllowsNull(t *testing.T) {
	view := View{
		Table: &Table{NAME: "itsm_software"},
		Columns: []Column{
			{NAME: "assigned_to", DATA_TYPE: "uuid", IS_NULLABLE: true},
			{NAME: "_updated_at", DATA_TYPE: "timestamptz"},
		},
	}

	setParts, args, err := buildScriptSetClauses(
		view,
		map[string]any{"assigned_to": nil},
		"",
		"2026-03-15T12:00:00Z",
		1,
	)
	if err != nil {
		t.Fatalf("buildScriptSetClauses() error = %v", err)
	}

	wantParts := []string{
		`"_updated_at" = $1::timestamptz`,
		`"assigned_to" = NULL`,
	}
	if !reflect.DeepEqual(setParts, wantParts) {
		t.Fatalf("buildScriptSetClauses() parts = %#v, want %#v", setParts, wantParts)
	}

	wantArgs := []any{"2026-03-15T12:00:00Z"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("buildScriptSetClauses() args = %#v, want %#v", args, wantArgs)
	}
}

func TestNormalizeScriptColumnValueSupportsChoiceAndNullableReference(t *testing.T) {
	choiceColumn := Column{
		NAME:      "state",
		DATA_TYPE: "choice",
		CHOICES: []ChoiceOption{
			{Value: "new", Label: "New"},
			{Value: "done", Label: "Done"},
		},
	}
	got, isNull, err := normalizeScriptColumnValue(choiceColumn, "done")
	if err != nil {
		t.Fatalf("normalizeScriptColumnValue(choice) error = %v", err)
	}
	if isNull || got != "done" {
		t.Fatalf("normalizeScriptColumnValue(choice) = (%q, %v), want (%q, false)", got, isNull, "done")
	}

	referenceColumn := Column{
		NAME:        "assigned_to",
		DATA_TYPE:   "reference",
		IS_NULLABLE: true,
	}
	got, isNull, err = normalizeScriptColumnValue(referenceColumn, "")
	if err != nil {
		t.Fatalf("normalizeScriptColumnValue(reference blank) error = %v", err)
	}
	if !isNull || got != "" {
		t.Fatalf("normalizeScriptColumnValue(reference blank) = (%q, %v), want (\"\", true)", got, isNull)
	}
}
