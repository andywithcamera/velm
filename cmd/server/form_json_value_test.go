package main

import (
	"encoding/json"
	"testing"
)

// A jsonb column reaches the form as a decoded map or slice. It used to be
// rendered with fmt.Sprint, which produced Go syntax — `map[on:completed]` —
// that the field then refused to save. The catalog's spawn rules and input
// mappings could be viewed but not round-tripped.
func TestFormatFieldValueRendersJSONColumns(t *testing.T) {
	cfg := formFieldConfig{Kind: "json"}
	value := map[string]any{"on": "completed", "assign": "definition"}
	got := formatFieldValue(value, cfg)

	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("json field rendered as %q, which is not JSON: %v", got, err)
	}
	if decoded["on"] != "completed" {
		t.Fatalf("round trip lost the value: %v", decoded)
	}

	array := formatFieldValue([]any{map[string]any{"on": "rejected"}}, cfg)
	if !json.Valid([]byte(array)) {
		t.Fatalf("json array field rendered as %q, which is not JSON", array)
	}
}

// A non-JSON field keeps the plain rendering.
func TestFormatFieldValueLeavesOtherKindsAlone(t *testing.T) {
	if got := formatFieldValue(42, formFieldConfig{Kind: "number"}); got != "42" {
		t.Fatalf("number field = %q", got)
	}
}
