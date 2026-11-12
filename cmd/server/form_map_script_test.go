package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"path/filepath"
	"strings"
	"testing"
)

// parseFormTemplates mirrors initTemplates for the tests below.
func parseFormTemplates(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := template.ParseGlob(filepath.Join("..", "..", "web", "templates", "*.html"))
	if err != nil {
		t.Fatalf("ParseGlob(main templates) error = %v", err)
	}
	if _, err := tmpl.ParseGlob(filepath.Join("..", "..", "web", "templates", "components", "*.html")); err != nil {
		t.Fatalf("ParseGlob(component templates) error = %v", err)
	}
	return tmpl
}

// The relationship map is markup plus a script block. A stray {{end}} closed
// the "form-view" define above the script, so html/template silently discarded
// it: the map rendered its markup and the "Loading related…" placeholder, and
// never ran any JavaScript. Parsing succeeds in that state, so only asserting
// on the rendered output catches it.
func TestFormMapScriptIsInsideTheTemplate(t *testing.T) {
	tmpl := parseFormTemplates(t)

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "form-view", map[string]any{
		"FormTable": "base_task",
		"FormID":    "11111111-1111-1111-1111-111111111111",
		"FormTitle": "Task",
	}); err != nil {
		t.Fatalf("execute form-view: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		`data-form-map-canvas="true"`,
		"var wrap = document.querySelector('[data-form-map=\"true\"]')",
		"function loadMap()",
		"'/f/graph/' + encodeURIComponent(table)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered form is missing %q; the map script is outside the {{define}} block", want)
		}
	}
}

// realtimeTables is a JS array literal. Using printf "%q" handed html/template
// an already-quoted string, which the JS value escaper then quoted again, so
// every EventSource asked for table=%22base_task%22 and the stream handler
// answered 400. The rendered array must hold bare identifiers.
func TestRealtimeTablesRendersBareTableNames(t *testing.T) {
	tmpl := parseFormTemplates(t)

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "table-view", map[string]any{
		"TableName":            "base_task",
		"ListSourceTables":     []string{"base_task", "story"},
		"TaskBoard":            false,
		"Rows":                 []any{},
		"QueryColumns":         []any{},
		"TotalRows":            0,
		"TotalPages":           0,
		"PageSize":             25,
		"Page":                 1,
		"TableCanCreate":       false,
		"TableRouteName":       "base_task",
		"TableLabel":           "Tasks",
		"PaginationCountExact": true,
	}); err != nil {
		t.Fatalf("execute table-view: %v", err)
	}

	start := strings.Index(buf.String(), "const realtimeTables = [")
	if start < 0 {
		t.Fatal("realtimeTables array not rendered")
	}
	rest := buf.String()[start:]
	end := strings.Index(rest, "].filter")
	if end < 0 {
		t.Fatal("realtimeTables array is not closed")
	}
	literal := strings.TrimSpace(strings.TrimPrefix(rest[:end], "const realtimeTables = ["))

	var tables []string
	if err := json.Unmarshal([]byte("["+literal+"]"), &tables); err != nil {
		t.Fatalf("realtimeTables is not a valid JSON array (%v): %s", err, literal)
	}
	if len(tables) != 2 || tables[0] != "base_task" || tables[1] != "story" {
		t.Fatalf("realtimeTables = %q, want [\"base_task\" \"story\"] with no embedded quotes", tables)
	}
}
