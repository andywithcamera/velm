package main

import (
	"bytes"
	"html/template"
	"os"
	"strings"
	"testing"

	"velm/internal/db"
)

// The point of the shared field is that every markdown surface renders the same
// markup and gets the same behaviour. These tests hold that line: the component
// itself keeps its contract, and no page hand-rolls its own editor again.

func renderMarkdownField(t *testing.T, view markdownFieldView) string {
	t.Helper()
	tmpl, err := template.ParseFiles("../../web/templates/components/markdown-field.html")
	if err != nil {
		t.Fatalf("parse markdown-field: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "markdown-field", view); err != nil {
		t.Fatalf("execute markdown-field: %v", err)
	}
	return buf.String()
}

func TestMarkdownFieldRendersSourcePreviewContract(t *testing.T) {
	out := renderMarkdownField(t, markdownFieldView{
		ID:          "description",
		Name:        "description",
		Value:       "## Hardware\n\n- 4 vCPU",
		Placeholder: "Write markdown…",
		AutoExpand:  true,
		FieldName:   "description",
	})

	want := []string{
		`id="description"`,
		`name="description"`,
		`data-markdown-field="true"`,
		`data-markdown-input="true"`,
		`data-markdown-preview-id="description-preview"`,
		`id="description-preview"`,
		`data-markdown-preview="true"`,
		`data-markdown-status="true"`,
		`data-markdown-error="true"`,
		`data-markdown-mode="preview"`, // a field with content opens as prose
		`data-markdown-readonly="0"`,
		`data-autoexpand="true"`,
		"## Hardware",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("rendered markdown field missing %q\n%s", w, out)
		}
	}

	empty := renderMarkdownField(t, markdownFieldView{ID: "brief", Name: "brief"})
	if !strings.Contains(empty, `data-markdown-mode="write"`) {
		t.Errorf("empty field should open in source mode:\n%s", empty)
	}
}

func TestMarkdownFieldReadonlyRendersAsProseOnly(t *testing.T) {
	out := renderMarkdownField(t, markdownFieldView{ID: "description", Name: "description", Value: "text", Readonly: true})
	if !strings.Contains(out, `data-markdown-readonly="1"`) {
		t.Errorf("readonly field did not mark itself readonly:\n%s", out)
	}
	if !strings.Contains(out, "\n            readonly\n") && !strings.Contains(out, " readonly\n") {
		t.Errorf("readonly field left the textarea writable:\n%s", out)
	}
	if !strings.Contains(out, "Markdown. Read only.") {
		t.Errorf("readonly field showed an edit affordance:\n%s", out)
	}
	if strings.Contains(out, `tabindex="0"`) {
		t.Errorf("readonly prose should not take focus to start editing:\n%s", out)
	}
	if !strings.Contains(out, `data-markdown-mode="preview"`) {
		t.Errorf("readonly field must paint as prose, not source:\n%s", out)
	}
}

func TestMarkdownSurfacesUseTheSharedField(t *testing.T) {
	surfaces := map[string]string{
		"task and entity descriptions": "../../web/templates/form-view.html",
		"docs article body":            "../../web/templates/docs-view.html",
	}
	for name, path := range surfaces {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		if !strings.Contains(body, `{{template "markdown-field"`) {
			t.Errorf("%s (%s) does not render the shared markdown field", name, path)
		}
		// The page builder has its own rich-text surface for page_wysiwyg
		// columns, which is a different field type, so contenteditable is only
		// forbidden where a markdown body used to be hand-built.
		handRolled := []string{
			`data-markdown-input="true"`,   // a private source box
			`data-markdown-preview="true"`, // a private preview
			`docs-wikilink-item`,           // a private autocomplete dropdown
			`/api/docs/preview`,            // a private preview renderer
			`/api/docs/wikilink-search`,    // a private wikilink lookup
		}
		if name == "docs article body" {
			handRolled = append(handRolled, `contenteditable="true"`)
		}
		for _, handRolled := range handRolled {
			if strings.Contains(body, handRolled) {
				t.Errorf("%s (%s) still hand-rolls %q instead of using the shared field", name, path, handRolled)
			}
		}
	}
}

func TestMarkdownBehaviourLivesInOneModule(t *testing.T) {
	raw, err := os.ReadFile("../../web/static/js/markdown-field.js")
	if err != nil {
		t.Fatalf("read markdown-field.js: %v", err)
	}
	js := string(raw)
	for _, required := range []string{
		`/api/docs/preview`,
		`/api/docs/wikilink-search`,
		`data-markdown-field`,
		`data-markdown-mode`,
		`data-autoexpand`,
	} {
		if !strings.Contains(js, required) {
			t.Errorf("markdown-field.js missing %q", required)
		}
	}

	layout, err := os.ReadFile("../../web/templates/layout.html")
	if err != nil {
		t.Fatalf("read layout.html: %v", err)
	}
	if !strings.Contains(string(layout), "/static/js/markdown-field.js") {
		t.Error("layout.html does not load markdown-field.js, so no field would work")
	}
}

func TestBuildFormMarkdownFields(t *testing.T) {
	columns := []db.Column{
		{NAME: "description", DATA_TYPE: "markdown", IS_NULLABLE: true},
		{NAME: "brief", DATA_TYPE: "markdown"},
		{NAME: "notes", DATA_TYPE: "long_text"},
	}
	configs := map[string]formFieldConfig{
		"description": {Kind: "markdown"},
		"brief":       {Kind: "markdown"},
		"notes":       {Kind: "textarea"},
	}
	values := map[string]string{"description": "## Hardware", "brief": "", "notes": "plain"}

	fields := buildFormMarkdownFields(columns, configs, values, false)
	if len(fields) != 2 {
		t.Fatalf("got %d markdown fields, want 2 (long_text is not markdown): %+v", len(fields), fields)
	}
	if got := fields["description"]; got.Value != "## Hardware" || got.Required || got.Readonly {
		t.Errorf("nullable markdown field = %+v, want value kept, not required, writable", got)
	}
	if got := fields["brief"]; !got.Required || got.ID != "brief" || got.Name != "brief" {
		t.Errorf("non-null markdown field = %+v, want required and named after the column", got)
	}
	if _, ok := fields["notes"]; ok {
		t.Error("long_text column was given a markdown field")
	}

	readonlyFields := buildFormMarkdownFields(columns, configs, values, true)
	if got := readonlyFields["brief"]; got.Readonly != true || got.Required != false {
		t.Errorf("readonly form = %+v, want every markdown field readonly and not required", got)
	}
}

func TestDocsArticleMarkdownField(t *testing.T) {
	got := docsArticleMarkdownField("# Title\n\nbody")
	if got.Name != "markdown_body" || got.ID != "docs-markdown-source" {
		t.Errorf("docs field identity = %q/%q, want markdown_body/docs-markdown-source", got.Name, got.ID)
	}
	if got.Size != "editor" {
		t.Errorf("docs field size = %q, want editor", got.Size)
	}
	// A docs page is the editor; its reader is a different page.
	if got.DefaultMode != "write" {
		t.Errorf("docs field default mode = %q, want write", got.DefaultMode)
	}
	out := renderMarkdownField(t, got)
	if !strings.Contains(out, "ui-markdown-field-editor") {
		t.Errorf("editor-size field did not render its modifier class:\n%s", out)
	}
	if !strings.Contains(out, "# Title") {
		t.Errorf("docs field dropped the article body:\n%s", out)
	}
}

// The docs page is not only a body: the title, status and tags are part of the
// same form. Swapping the editor out must not take the page chrome with it.
func TestDocsEditorKeepsPageChromeAroundTheField(t *testing.T) {
	t.Chdir("../..")
	tpl := parseTemplates(t)
	var buf bytes.Buffer
	err := tpl.ExecuteTemplate(&buf, "docs-manage-view", map[string]any{
		"Uri": "/docs/manage", "Title": "Docs", "PageTitle": "Docs", "Section": "Operations",
		"Menu": []any{}, "Properties": map[string]string{}, "User": nil, "Theme": "velm",
		"CSRFToken": "csrf-token", "RequestID": "req-1", "CSPNonce": "nonce-1",
		"Breadcrumbs":         []Breadcrumb{},
		"View":                "docs-manage",
		"DocsLibrarySelected": map[string]any{"Slug": "base", "Name": "Base"},
		"DocsArticleSelected": map[string]any{"Slug": "", "Title": "", "Status": "draft", "Tags": ""},
		"DocsArticleField":    docsArticleMarkdownField(""),
	})
	if err != nil {
		t.Fatalf("docs-view render: %v", err)
	}
	out := buf.String()
	for _, want := range []struct{ needle, why string }{
		{`id="docs-article-title"`, "article title input"},
		{`name="status"`, "status select"},
		{`name="tags"`, "tags input"},
		{`id="docs-article-form"`, "article form"},
		{`id="docs-markdown-source"`, "markdown source"},
		{`data-markdown-field="true"`, "shared markdown field"},
		{`name="markdown_body"`, "body submits as markdown_body"},
		{`ui-markdown-field-editor`, "editor-size field"},
	} {
		if !strings.Contains(out, want.needle) {
			t.Errorf("docs editor lost its %s (%s)", want.why, want.needle)
		}
	}
}
