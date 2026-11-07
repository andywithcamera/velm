package main

import (
	"bytes"
	"strings"
	"testing"

	"velm/internal/db"
)

func TestInjectUserPasswordFormColumn(t *testing.T) {
	columns := []db.Column{
		{NAME: "name", LABEL: "Name"},
		{NAME: db.UserPasswordColumn, LABEL: "Password Hash"},
		{NAME: "email", LABEL: "Email"},
	}
	out := injectUserPasswordFormColumn(db.UserTableName, columns)

	if len(out) != 3 {
		t.Fatalf("got %d columns, want 3: %+v", len(out), out)
	}
	if out[0].NAME != db.UserPasswordField {
		t.Fatalf("password field should come first, got %q", out[0].NAME)
	}
	for _, column := range out {
		if column.NAME == db.UserPasswordColumn {
			t.Fatalf("password_hash must never be rendered on a form")
		}
	}
	if out[1].NAME != "name" || out[2].NAME != "email" {
		t.Fatalf("order of the remaining columns changed: %+v", out)
	}
}

func TestInjectUserPasswordFormColumnIgnoresOtherTables(t *testing.T) {
	columns := []db.Column{{NAME: "name"}, {NAME: db.UserPasswordColumn}}
	out := injectUserPasswordFormColumn("_task", columns)
	if len(out) != len(columns) || out[0].NAME != "name" {
		t.Fatalf("non-user tables must be left alone: %+v", out)
	}
}

func TestDropHiddenFormValuesDropsSecrets(t *testing.T) {
	columns := []db.Column{
		{NAME: db.UserPasswordColumn, IS_HIDDEN: true},
		{NAME: "api_key_enc", IS_HIDDEN: true},
		{NAME: "email"},
		{NAME: "_id", IS_HIDDEN: true},
	}
	values := map[string]string{
		db.UserPasswordColumn: "$2a$10$abcdefghijklmnopqrstuv",
		"api_key_enc":         "ciphertext",
		"email":               "a@b.c",
		"_id":                 "abc",
	}
	dropHiddenFormValues(columns, values)

	if _, ok := values[db.UserPasswordColumn]; ok {
		t.Fatalf("stored password hash must not reach the browser")
	}
	if _, ok := values["api_key_enc"]; ok {
		t.Fatalf("encrypted API key must not reach the browser")
	}
	if values["email"] != "a@b.c" {
		t.Fatalf("visible values must be kept, got %q", values["email"])
	}
	if values["_id"] != "abc" {
		t.Fatalf("underscore columns drive form routing and must be kept")
	}
}

// TestFormViewRendersPasswordField covers the template branches that only run
// for the synthetic password field: masked input, no autofill of a stored
// value, and the help text explaining that blank means "keep current".
func TestFormViewRendersPasswordField(t *testing.T) {
	t.Chdir("../..")
	tpl := parseTemplates(t)
	passwordColumn := db.UserPasswordFormColumn()
	data := map[string]any{
		"Uri": "/p/_user/new", "Title": "New User", "PageTitle": "New User",
		"Section": "App", "AppID": "", "Menu": []any{}, "Properties": map[string]string{},
		"Theme": "velm", "CSRFToken": "csrf-token", "RequestID": "req-1",
		"CSPNonce": "nonce-1", "Breadcrumbs": []Breadcrumb{},
		"FormTable": db.UserTableName, "FormID": "new", "FormTableLabel": "User",
		"FormColumns": []db.Column{passwordColumn},
		"FormFieldConfigs": map[string]formFieldConfig{
			db.UserPasswordField: {Kind: "text", InputType: "password", Placeholder: "Password"},
		},
		"FormFieldValues":       map[string]string{},
		"FormHiddenFields":      []formHiddenField{},
		"FormPasswordFieldName": db.UserPasswordField,
		"FormPasswordState":     "No password is set for this user, so they cannot sign in. Set one below.",
		"XData":                 map[string]any{},
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "form-view", data); err != nil {
		t.Fatalf("form-view render failed: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`type="password"`,
		`autocomplete="new-password"`,
		`data-form-password-state="true"`,
		"Leave blank to keep the current password",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered form missing %q", want)
		}
	}
	if strings.Contains(html, "password_hash") {
		t.Errorf("rendered form must not mention password_hash")
	}
}
