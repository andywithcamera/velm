package main

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

// parseTemplates mirrors initTemplates so tests exercise the real template set.
func parseTemplates(t *testing.T) *template.Template {
	t.Helper()
	tpl, err := template.ParseGlob("web/templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	if _, err := tpl.ParseGlob("web/templates/components/*.html"); err != nil {
		t.Fatalf("parse components: %v", err)
	}
	return tpl
}

// TestTemplateEscaping catches html/template context faults, which ParseGlob
// does not detect: escaping is resolved at execution time, so a template can
// parse cleanly and still fail every render. Such faults are reported against
// the template source, not as execution errors, so those are the only errors
// treated as fatal here. Data-dependent errors ("... executing ...") are
// expected with a zero-value map and are skipped.
func TestTemplateEscaping(t *testing.T) {
	t.Chdir("../..")
	tpl := parseTemplates(t)
	data := map[string]any{"Breadcrumbs": []Breadcrumb{}}
	for _, tm := range tpl.Templates() {
		var buf bytes.Buffer
		err := tm.Execute(&buf, data)
		if err == nil {
			continue
		}
		if !strings.Contains(err.Error(), " executing ") {
			t.Errorf("%s: template context error: %v", tm.Name(), err)
		}
	}
}

// TestLoginPageRenders executes the login page through layout.html with the
// same view data handleLoginPage supplies, so the whole render path is covered.
func TestLoginPageRenders(t *testing.T) {
	t.Chdir("../..")
	tpl := parseTemplates(t)
	data := map[string]any{
		"Uri": "/login", "Title": "Login", "PageTitle": "Login", "Section": "Auth",
		"AppID": "", "Menu": []any{}, "Properties": map[string]string{},
		"User": nil, "Theme": "velm", "CSRFToken": "csrf-token",
		"RequestID": "req-1", "CSPNonce": "nonce-1", "Breadcrumbs": []Breadcrumb{},
		"View": "login", "LoginNext": "",
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		t.Fatalf("layout render failed: %v", err)
	}
	if !strings.Contains(buf.String(), "csrf-token") {
		t.Fatalf("rendered page missing CSRF token; %d bytes", buf.Len())
	}
}

// TestFormViewTableRowHref guards the row-link markup in table rows: the
// attribute branch and the plain branch must both end inside the tag.
func TestFormViewTableRowHref(t *testing.T) {
	t.Chdir("../..")
	tpl := parseTemplates(t)
	row := map[string]any{"Href": "/p/one", "Cells": []any{map[string]any{"Value": "one"}}}
	data := map[string]any{"Breadcrumbs": []Breadcrumb{}, "Rows": []any{row}}
	var buf bytes.Buffer
	err := tpl.ExecuteTemplate(&buf, "form-table", data)
	if err != nil && strings.Contains(err.Error(), "different contexts") {
		t.Fatalf("form-table row branch context error: %v", err)
	}
}
