package main

// #204: ordering a definition must collect the inputs it declares. These cover
// the validation rules the catalog order endpoint applies before spawning.

import (
	"net/url"
	"testing"
)

func TestResolveCatalogInputsCollectsDeclaredFields(t *testing.T) {
	schema := "- name: description\n  label: What is the doc about?\n  data_type: long_text\n  is_nullable: false\n"
	form := url.Values{"description": {"write me a doc about cheese"}}

	inputs, err := resolveCatalogInputs(schema, form)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if inputs["description"] != "write me a doc about cheese" {
		t.Fatalf("inputs = %+v", inputs)
	}
}

// A definition that declares nothing must keep ordering exactly as before:
// no inputs, no error, every existing Start button unchanged.
func TestResolveCatalogInputsWithoutSchemaSpawnsImmediately(t *testing.T) {
	for _, schema := range []string{"", "   ", "[]"} {
		inputs, err := resolveCatalogInputs(schema, url.Values{"ignored": {"x"}})
		if err != nil {
			t.Fatalf("schema %q: %v", schema, err)
		}
		if inputs != nil {
			t.Fatalf("schema %q produced inputs %+v, want nil", schema, inputs)
		}
	}
}

func TestResolveCatalogInputsRejectsMissingRequired(t *testing.T) {
	schema := "- name: description\n  label: Description\n  data_type: long_text\n  is_nullable: false\n"
	if _, err := resolveCatalogInputs(schema, url.Values{}); err == nil {
		t.Fatal("expected an error for a missing required input")
	}
}

// The error names the field as the requester sees it, not its internal name.
func TestResolveCatalogInputsErrorUsesTheLabel(t *testing.T) {
	schema := "- name: description\n  label: What is the doc about?\n  data_type: long_text\n  is_nullable: false\n"
	_, err := resolveCatalogInputs(schema, url.Values{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); got != "What is the doc about? is required" {
		t.Fatalf("error = %q", got)
	}
}

func TestResolveCatalogInputsRejectsOutOfRangeChoice(t *testing.T) {
	schema := "- name: priority\n  data_type: choice\n  choices:\n    - value: low\n      label: Low\n    - value: high\n      label: High\n"
	if _, err := resolveCatalogInputs(schema, url.Values{"priority": {"urgent"}}); err == nil {
		t.Fatal("expected an error for a value outside the choice list")
	}
	if _, err := resolveCatalogInputs(schema, url.Values{"priority": {"high"}}); err != nil {
		t.Fatalf("valid choice rejected: %v", err)
	}
}

func TestResolveCatalogInputsRejectsBadSchema(t *testing.T) {
	if _, err := resolveCatalogInputs("- name: \"not a name\"\n", url.Values{}); err == nil {
		t.Fatal("expected an error for an unreadable schema")
	}
}
