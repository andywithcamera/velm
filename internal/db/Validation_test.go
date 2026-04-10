package db

import (
	"database/sql"
	"strings"
	"testing"
)

func TestValidateColumnValueSupportsExpandedColumnTypes(t *testing.T) {
	tests := []struct {
		name    string
		column  Column
		value   string
		wantErr bool
	}{
		{
			name: "choice valid",
			column: Column{
				NAME:      "state",
				DATA_TYPE: "choice",
				CHOICES: []ChoiceOption{
					{Value: "new", Label: "New"},
					{Value: "done", Label: "Done"},
				},
			},
			value: "done",
		},
		{
			name: "choice invalid",
			column: Column{
				NAME:      "state",
				DATA_TYPE: "choice",
				CHOICES: []ChoiceOption{
					{Value: "new", Label: "New"},
				},
			},
			value:   "closed",
			wantErr: true,
		},
		{name: "email valid", column: Column{NAME: "email", DATA_TYPE: "email"}, value: "test@andydoyle.ie"},
		{name: "email invalid", column: Column{NAME: "email", DATA_TYPE: "email"}, value: "bad", wantErr: true},
		{name: "url valid", column: Column{NAME: "website", DATA_TYPE: "url"}, value: "https://example.com"},
		{name: "url invalid", column: Column{NAME: "website", DATA_TYPE: "url"}, value: "example", wantErr: true},
		{name: "phone valid", column: Column{NAME: "phone", DATA_TYPE: "phone"}, value: "+353 1 234 5678"},
		{name: "phone invalid", column: Column{NAME: "phone", DATA_TYPE: "phone"}, value: "abc", wantErr: true},
		{name: "autnumber valid", column: Column{NAME: "number", DATA_TYPE: "autnumber", PREFIX: sql.NullString{String: "TASK", Valid: true}}, value: "TASK-000123"},
		{name: "autnumber invalid prefix", column: Column{NAME: "number", DATA_TYPE: "autnumber", PREFIX: sql.NullString{String: "TASK", Valid: true}}, value: "WO-000123", wantErr: true},
		{name: "typed reference valid", column: Column{NAME: "requested_for", DATA_TYPE: "reference:_user"}, value: "550e8400-e29b-41d4-a716-446655440000"},
		{name: "typed reference invalid", column: Column{NAME: "requested_for", DATA_TYPE: "reference:_user"}, value: "not-a-uuid", wantErr: true},
		{name: "richtext valid", column: Column{NAME: "description", DATA_TYPE: "richtext"}, value: "<p>Hello</p>"},
		{name: "code valid", column: Column{NAME: "definition", DATA_TYPE: "code:sql"}, value: "select 1;"},
	}

	for _, tt := range tests {
		err := validateColumnValue(tt.column, tt.value)
		if tt.wantErr && err == nil {
			t.Fatalf("%s: expected error", tt.name)
		}
		if !tt.wantErr && err != nil {
			t.Fatalf("%s: unexpected error: %v", tt.name, err)
		}
	}
}

func TestValidateFormWriteSupportsValidationExpression(t *testing.T) {
	columns := []Column{
		{
			NAME:        "start_date",
			DATA_TYPE:   "date",
			IS_NULLABLE: false,
		},
		{
			NAME:            "end_date",
			DATA_TYPE:       "date",
			IS_NULLABLE:     false,
			VALIDATION_EXPR: sql.NullString{String: "value >= start_date", Valid: true},
			VALIDATION_MSG:  sql.NullString{String: "End date must be on or after the start date", Valid: true},
		},
		{
			NAME:            "summary",
			DATA_TYPE:       "varchar(10)",
			IS_NULLABLE:     false,
			VALIDATION_EXPR: sql.NullString{String: "len(value) <= 10", Valid: true},
		},
	}

	err := ValidateFormWrite(columns, map[string]string{
		"start_date": "2026-03-20",
		"end_date":   "2026-03-19",
		"summary":    "short",
	}, true)
	if err == nil || !strings.Contains(err.Error(), "End date must be on or after the start date") {
		t.Fatalf("expected validation message error, got %v", err)
	}

	err = ValidateFormWrite(columns, map[string]string{
		"start_date": "2026-03-20",
		"end_date":   "2026-03-20",
		"summary":    "this is far too long",
	}, true)
	if err == nil || !strings.Contains(err.Error(), `field "summary"`) {
		t.Fatalf("expected summary validation error, got %v", err)
	}

	err = ValidateFormWrite(columns, map[string]string{
		"start_date": "2026-03-20",
		"end_date":   "2026-03-21",
		"summary":    "just right",
	}, true)
	if err != nil {
		t.Fatalf("expected validation expression to pass, got %v", err)
	}
}

func TestValidateFormWriteAllowsMissingAutoNumberOnCreate(t *testing.T) {
	columns := []Column{
		{
			NAME:        "number",
			DATA_TYPE:   "autnumber",
			PREFIX:      sql.NullString{String: "TASK", Valid: true},
			IS_NULLABLE: false,
		},
		{
			NAME:        "title",
			DATA_TYPE:   "text",
			IS_NULLABLE: false,
		},
	}

	if err := ValidateFormWrite(columns, map[string]string{
		"title": "Task title",
	}, true); err != nil {
		t.Fatalf("expected autnumber field to be generated on create, got %v", err)
	}
}

func TestCanonicalDataTypeSupportsAliasesAndTypedReferences(t *testing.T) {
	if got := CanonicalDataType(" LONGTEXT "); got != "long_text" {
		t.Fatalf("expected longtext alias to normalize, got %q", got)
	}
	if got := CanonicalDataType("AutoNumber"); got != "autnumber" {
		t.Fatalf("expected autonumber alias to normalize, got %q", got)
	}
	if got := CanonicalDataType("reference:_USER"); got != "reference:_user" {
		t.Fatalf("expected typed reference to normalize, got %q", got)
	}
	if got := DataTypeReferenceTable("reference:_USER"); got != "_user" {
		t.Fatalf("expected reference table suffix, got %q", got)
	}
	if !IsCodeDataType("code:sql") {
		t.Fatal("expected typed code data type to be detected")
	}
}
