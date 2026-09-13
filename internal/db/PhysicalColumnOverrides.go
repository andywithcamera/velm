package db

import (
	"database/sql"
)

// builtinColumnMetadata carries human-facing metadata for physical columns
// that information_schema cannot provide: labels, data types, choices,
// references, and conditional-visibility expressions. GetPhysicalColumns
// applies these after loading the physical catalog so system tables render
// in the UI exactly like app-defined tables.
//
// Condition expressions use the same boolean expression language evaluated
// by evaluateBooleanExpression (ExpressionEval.go).
type builtinColumnOverride struct {
	LABEL           string
	DATA_TYPE       string
	REFERENCE_TABLE string
	CONDITION_EXPR  string
	CHOICES         []ChoiceOption
	// NULLABLE relaxes the NOT NULL constraint at the form layer for
	// system-populated columns (e.g. api_key_enc is filled by the save
	// path, not the user). Does not alter the physical schema.
	NULLABLE bool
	// HIDDEN / READONLY override catalog-derived visibility. Hidden columns
	// never render in generic forms or lists; readonly renders but is
	// not editable.
	HIDDEN   bool
	READONLY bool
}

var builtinColumnMetadata = map[string]map[string]builtinColumnOverride{
	"_user": {
		"user_type": {
			LABEL:     "User Type",
			DATA_TYPE: "choice",
			CHOICES: []ChoiceOption{
				{Value: "human", Label: "Human"},
				{Value: "ai_agent", Label: "AI Agent"},
				{Value: "api", Label: "API"},
				{Value: "automation", Label: "Automation"},
			},
		},
	},
	"_ai_connection": {
		"provider_id": {
			LABEL:           "Provider",
			DATA_TYPE:       "reference",
			REFERENCE_TABLE: "_ai_provider",
		},
		"api_key_enc": {
			// System-populated by the save path (encrypted API key).
			// Never rendered; not form-required.
			NULLABLE: true,
			HIDDEN:   true,
		},
		"api_key_fingerprint": {
			// System-generated from the key for display; not form-required.
			NULLABLE: true,
			HIDDEN:   true,
		},
		"account_info": {
			// Free-form bookkeeping JSONB; optional.
			NULLABLE: true,
		},
		"is_active": {
			LABEL:     "Is Active",
			DATA_TYPE: "boolean",
		},
		"last_verified_at": {
			LABEL: "Last Verified At",
		},
		"models_synced_at": {
			LABEL:     "Models Synced At",
			DATA_TYPE: "datetime",
		},
	},
	"_ai_model": {
		"ai_connection_id": {
			LABEL:           "AI Connection",
			DATA_TYPE:       "reference",
			REFERENCE_TABLE: "_ai_connection",
		},
	},
}

// applyBuiltinColumnMetadata merges builtin metadata into a physical column
// list. Labels fill in only when the physical label is empty; data types,
// references, conditions, and choices replace what the catalog can infer.
func applyBuiltinColumnMetadata(tableName string, columns []Column) []Column {
	overrides, ok := builtinColumnMetadata[tableName]
	if !ok {
		return columns
	}
	for i := range columns {
		over, ok := overrides[columns[i].NAME]
		if !ok {
			continue
		}
		if over.LABEL != "" {
			columns[i].LABEL = over.LABEL
		}
		if over.DATA_TYPE != "" {
			columns[i].DATA_TYPE = over.DATA_TYPE
		}
		if over.REFERENCE_TABLE != "" {
			columns[i].REFERENCE_TABLE = sql.NullString{String: over.REFERENCE_TABLE, Valid: true}
		}
		if over.CONDITION_EXPR != "" {
			columns[i].CONDITION_EXPR = sql.NullString{String: over.CONDITION_EXPR, Valid: true}
		}
		if len(over.CHOICES) > 0 {
			columns[i].CHOICES = append([]ChoiceOption(nil), over.CHOICES...)
		}
		if over.NULLABLE {
			columns[i].IS_NULLABLE = true
		}
		if over.HIDDEN {
			columns[i].IS_HIDDEN = true
		}
		if over.READONLY {
			columns[i].IS_READONLY = true
		}
	}
	return columns
}
