package main

// Slice 1 (legibility) of the process designer: the catalogue must say out
// loud whether an entry is a chain or a single task, and the order fragment
// must say how many blocks opened. These tests are the labels; the route and
// menu tests live in task_catalogue_integration_test.go because the menu is
// built from the database.

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskCatalogEntryKindLabel(t *testing.T) {
	cases := []struct {
		name      string
		isChain   bool
		steps     int
		want      string
		startWant string
	}{
		{"leaf", false, 0, "Single task", "Start…"},
		{"chain with one step", true, 1, "Chain · 1 step", "Start chain…"},
		{"chain with many steps", true, 3, "Chain · 3 steps", "Start chain…"},
		{"empty container", true, 0, "Chain · 0 steps", "Start chain…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := taskCatalogEntry{IsContainer: tc.isChain, StepCount: tc.steps}
			if got := entry.KindLabel(); got != tc.want {
				t.Errorf("KindLabel() = %q, want %q", got, tc.want)
			}
			if got := entry.StartLabel(); got != tc.startWant {
				t.Errorf("StartLabel() = %q, want %q", got, tc.startWant)
			}
		})
	}
}

func TestTaskCatalogOrderConfirmation(t *testing.T) {
	cases := []struct {
		name        string
		number      string
		isContainer bool
		steps       int
		want        string
	}{
		{
			name:        "single task opens only itself",
			number:      "TASK-000151",
			isContainer: false,
			steps:       0,
			want:        "Started TASK-000151 as a single task: no steps opened.",
		},
		{
			// The exact case Andy hit: one step, and nothing said so.
			name:        "chain with one step",
			number:      "TASK-000152",
			isContainer: true,
			steps:       1,
			want:        "Started TASK-000152 as a chain: 1 step opened.",
		},
		{
			name:        "chain with several steps",
			number:      "TASK-000160",
			isContainer: true,
			steps:       4,
			want:        "Started TASK-000160 as a chain: 4 steps opened.",
		},
		{
			name:        "container with no steps yet",
			number:      "TASK-000170",
			isContainer: true,
			steps:       0,
			want:        "Started TASK-000170 as a chain: no steps to open yet.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := catalogOrderConfirmation(tc.number, tc.isContainer, tc.steps); got != tc.want {
				t.Errorf("catalogOrderConfirmation() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The confirmation is rendered into the HTMX fragment, so the sentence must
// survive the template and not just the function.
func TestTaskCatalogOrderDoneFragmentReportsChain(t *testing.T) {
	var buf bytes.Buffer
	err := catalogOrderDoneTmpl.Execute(&buf, map[string]string{
		"TaskID":  "8a1f",
		"Number":  "TASK-000152",
		"Message": catalogOrderConfirmation("TASK-000152", true, 1),
	})
	if err != nil {
		t.Fatalf("Execute(catalogOrderDone): %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Started TASK-000152 as a chain: 1 step opened.",
		`href="/f/base_task/8a1f"`,
		`href="/task"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("order fragment missing %q; got %s", want, out)
		}
	}
}

// A container must be unmistakably different from a leaf on the rendered card,
// not just in the struct.
func TestTaskCatalogResultsMarksChainsAndSingleTasks(t *testing.T) {
	ensureTestSessionStore()
	var buf bytes.Buffer
	data := map[string]any{
		"TaskCatalogTotal": 2,
		"TaskCatalogCategories": []taskCatalogCategory{{
			Name: "operations",
			Entries: []taskCatalogEntry{
				{Slug: "upgrade-server", DisplayName: "Upgrade Server", IsContainer: true, StepCount: 1},
				{Slug: "reboot-server", DisplayName: "Reboot Server"},
			},
		}},
		"TaskCatalogOrderURL": "/task-catalogue/order",
	}
	if err := templates.ExecuteTemplate(&buf, "task-catalog-results", data); err != nil {
		t.Fatalf("ExecuteTemplate(task-catalog-results): %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Chain · 1 step",
		"Single task",
		`id="definition-upgrade-server"`,
		"Start chain…",
		`hx-post="/task-catalogue/order"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("catalogue results missing %q", want)
		}
	}
	// Actions must not point at the pre-spelling path.
	if strings.Contains(out, "/task-catalog?") || strings.Contains(out, "/task-catalog/") {
		t.Errorf("catalogue results still reference the old path:\n%s", out)
	}
}

// The old path is a permanent redirect, and the query survives it, because the
// editor links to ?q=<slug> and bookmarks carry searches.
func TestTaskCatalogueLegacyPathRedirects(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/task-catalog?q=upgrade", nil)
	rec := httptest.NewRecorder()
	handleTaskCatalogueLegacyRedirect(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMovedPermanently)
	}
	if got, want := rec.Header().Get("Location"), "/task-catalogue?q=upgrade"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestTaskCatalogueLegacyPathRedirectsWithoutQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/task-catalog", nil)
	rec := httptest.NewRecorder()
	handleTaskCatalogueLegacyRedirect(rec, req)

	if got, want := rec.Header().Get("Location"), "/task-catalogue"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// The editor page must send the reader to that definition's own catalogue
// entry; both spellings of the kind are covered because a chain and a leaf say
// different things.
func TestTaskDefinitionEditorLinksToCatalogue(t *testing.T) {
	ensureTestSessionStore()
	cases := []struct {
		name        string
		isContainer bool
		want        string
	}{
		{"container", true, "View this chain in the catalogue"},
		{"leaf", false, "View this task in the catalogue"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl, err := template.ParseGlob("../../web/templates/task-definitions-view.html")
			if err != nil {
				t.Fatalf("ParseGlob: %v", err)
			}
			var buf bytes.Buffer
			data := map[string]any{
				"CSRFToken": "test-token",
				"Definition": map[string]any{
					"Slug":        "upgrade-server",
					"DisplayName": "Upgrade Server",
					"IsContainer": tc.isContainer,
				},
			}
			if err := tpl.ExecuteTemplate(&buf, "task-definition-editor-view", data); err != nil {
				t.Fatalf("ExecuteTemplate(task-definition-editor-view): %v", err)
			}
			out := buf.String()
			if !strings.Contains(out, tc.want) {
				t.Errorf("editor missing %q", tc.want)
			}
			if !strings.Contains(out, `href="/task-catalogue?q=upgrade-server#definition-upgrade-server"`) {
				t.Errorf("editor does not link to the catalogue entry")
			}
		})
	}
}

// The spelling change is user-facing and must not leave a single "catalog" in
// body copy. CSS classes and Go identifiers keep the old spelling deliberately,
// so this asserts the rendered sentences rather than the raw file.
func TestUserFacingCatalogueSpelling(t *testing.T) {
	ensureTestSessionStore()

	// Docs library landing copy.
	docsTpl, err := template.ParseGlob("../../web/templates/docs-catalog-view.html")
	if err != nil {
		t.Fatalf("ParseGlob(docs): %v", err)
	}
	var docs bytes.Buffer
	if err := docsTpl.ExecuteTemplate(&docs, "docs-view", map[string]any{
		"DocsLibrarySelected": map[string]any{},
		"DocsLibraries":       []any{},
		"DocsArticles":        []any{},
	}); err != nil {
		t.Fatalf("ExecuteTemplate(docs-view): %v", err)
	}
	docsOut := docs.String()
	if !strings.Contains(docsOut, "Explore The Catalogue") {
		t.Error("docs landing page does not say \"Explore The Catalogue\"")
	}
	if strings.Contains(docsOut, ">Explore The Catalog<") {
		t.Error("docs landing page still says \"Explore The Catalog\"")
	}

	// The definition editor's active hint.
	defTpl, err := template.ParseGlob("../../web/templates/task-definitions-view.html")
	if err != nil {
		t.Fatalf("ParseGlob(definitions): %v", err)
	}
	var defs bytes.Buffer
	if err := defTpl.ExecuteTemplate(&defs, "task-definition-editor-view", map[string]any{
		"CSRFToken": "test-token",
		"Definition": map[string]any{
			"Slug":        "upgrade-server",
			"DisplayName": "Upgrade Server",
			"IsContainer": true,
		},
	}); err != nil {
		t.Fatalf("ExecuteTemplate(task-definition-editor-view): %v", err)
	}
	if strings.Contains(defs.String(), "spawned from the catalog<") {
		t.Error("definition editor still says \"spawned from the catalog\"")
	}
	if !strings.Contains(defs.String(), "spawned from the catalogue") {
		t.Error("definition editor does not say \"spawned from the catalogue\"")
	}
}
