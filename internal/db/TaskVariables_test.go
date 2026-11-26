package db

import (
	"reflect"
	"testing"
)

// Flattening is where the shape of storage is decided: a nested scope is one
// row per name, everything else is one row under its own name.
func TestFlattenTaskVars(t *testing.T) {
	inputs := map[string]any{"kind": "defect", "count": float64(3)}
	block := map[string]any{"id": "b1"}
	parent := map[string]any{"inputs": map[string]any{"kind": "parent"}}

	rows, err := flattenTaskVars(map[string]any{
		"inputs":     inputs,
		"outputs":    map[string]any{},
		"block":      block,
		"parent":     parent,
		"attempt_no": float64(2),
	})
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}

	want := map[varRowKey]any{
		{Scope: VarScopeInput, Name: "kind"}:     "defect",
		{Scope: VarScopeInput, Name: "count"}:    float64(3),
		{Scope: VarScopeBlock, Name: "id"}:       "b1",
		{Scope: VarScopeTop, Name: "parent"}:     parent,
		{Scope: VarScopeTop, Name: "attempt_no"}: float64(2),
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}

	// An empty scope stores nothing: absence and {} mean the same thing, and
	// a row per empty scope would be a row that says nothing.
	for key := range rows {
		if key.Scope == VarScopeOutput {
			t.Fatalf("empty outputs produced a row: %#v", key)
		}
	}
}

// A scope that is not an object cannot be split into names, so it is stored
// whole rather than dropped: a broken definition must not lose its value.
func TestFlattenTaskVarsKeepsNonObjectScope(t *testing.T) {
	rows, err := flattenTaskVars(map[string]any{"inputs": "oops"})
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	want := map[varRowKey]any{{Scope: VarScopeTop, Name: "inputs"}: "oops"}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}
}

func TestVariableProjections(t *testing.T) {
	cases := []struct {
		name    string
		value   any
		text    *string
		number  string
		boolean *bool
	}{
		{name: "string", value: "high", text: strPtr("high")},
		{name: "bool true", value: true, boolean: boolPtr(true)},
		{name: "bool false", value: false, boolean: boolPtr(false)},
		{name: "number", value: float64(2.5), number: "2.5"},
		{name: "integer", value: float64(3), number: "3"},
		{name: "null", value: nil},
		{name: "document", value: map[string]any{"a": 1}},
		{name: "array", value: []any{1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, number, boolean := variableProjections(tc.value)
			if !equalStringPtr(text, tc.text) {
				t.Fatalf("value_text = %v, want %v", deref(text), deref(tc.text))
			}
			if number != tc.number {
				t.Fatalf("value_number = %q, want %q", number, tc.number)
			}
			if !equalBoolPtr(boolean, tc.boolean) {
				t.Fatalf("value_bool = %v, want %v", derefBool(boolean), derefBool(tc.boolean))
			}
		})
	}
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func derefBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func equalStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func equalBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
