package main

// Slice 1 (legibility) of the process designer: the catalogue must say out
// loud whether an entry is a chain or a single task, and the order page must
// say what starting it will open. These tests are the labels and the page
// structure; the route and menu tests live in
// task_catalogue_integration_test.go because the menu is built from the
// database.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"html/template"
	"path/filepath"

	"velm/internal/db"
)

func TestTaskCatalogEntryKindLabel(t *testing.T) {
	cases := []struct {
		name      string
		isChain   bool
		steps     int
		want      string
		startWant string
	}{
		{"leaf", false, 0, "Single task", "Start"},
		{"chain with one step", true, 1, "Chain · 1 step", "Start chain"},
		{"chain with many steps", true, 3, "Chain · 3 steps", "Start chain"},
		{"empty container", true, 0, "Chain · 0 steps", "Start chain"},
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

// The order page must say what starting the definition will open, and the
// page must not be a card: one definition, its steps, its inputs. Pure, so the
// wording is testable without a database.
func TestTaskCatalogOrderViewLabelsWhatWillOpen(t *testing.T) {
	cases := []struct {
		name        string
		view        taskCatalogOrderView
		wantKind    string
		wantStart   string
		wantOutcome string
	}{
		{
			name:        "single task opens only itself",
			view:        taskCatalogOrderView{Slug: "reboot", DisplayName: "Reboot", StepCount: 0},
			wantKind:    "Single task",
			wantStart:   "Start",
			wantOutcome: "Starting this opens a single task.",
		},
		{
			// The exact case Andy hit: one step, and nothing said so.
			name:        "chain with one step",
			view:        taskCatalogOrderView{Slug: "upgrade", DisplayName: "Upgrade", IsContainer: true, StepCount: 1},
			wantKind:    "Chain · 1 step",
			wantStart:   "Start chain · 1 step",
			wantOutcome: "Starting this opens a chain of 1 step.",
		},
		{
			name:        "chain with several steps",
			view:        taskCatalogOrderView{Slug: "ship", DisplayName: "Ship", IsContainer: true, StepCount: 4},
			wantKind:    "Chain · 4 steps",
			wantStart:   "Start chain · 4 steps",
			wantOutcome: "Starting this opens a chain of 4 steps.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.view.KindLabel(); got != tc.wantKind {
				t.Errorf("KindLabel() = %q, want %q", got, tc.wantKind)
			}
			if got := tc.view.StartLabel(); got != tc.wantStart {
				t.Errorf("StartLabel() = %q, want %q", got, tc.wantStart)
			}
			want := tc.wantOutcome
			if got := tc.view.Outcome(); got != want {
				t.Errorf("Outcome() = %q, want %q", got, want)
			}
			if !strings.Contains(renderOrderPage(t, tc.view), want) {
				t.Errorf("order page does not say %q", want)
			}
		})
	}
}

// renderOrderPage renders through a freshly parsed template set rather than
// the package global, so the test does not depend on another test file having
// called initTemplates first.
func renderOrderPage(t *testing.T, view taskCatalogOrderView) string {
	t.Helper()
	tpl, err := template.ParseGlob(filepath.Join("..", "..", "web", "templates", "*.html"))
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	if _, err := tpl.ParseGlob(filepath.Join("..", "..", "web", "templates", "components", "*.html")); err != nil {
		t.Fatalf("parse components: %v", err)
	}
	var buf bytes.Buffer
	data := map[string]any{"Order": view, "CSRFToken": "test-token"}
	if err := tpl.ExecuteTemplate(&buf, "task-catalog-order-view", data); err != nil {
		t.Fatalf("ExecuteTemplate(task-catalog-order-view): %v", err)
	}
	return buf.String()
}

// The steps are the decision input: they come from the stored tree, in order,
// and the trigger and a scope's End are boundaries rather than steps.
func TestCatalogStepsFromDocumentSkipsBoundaries(t *testing.T) {
	blocks := []db.DocumentBlock{
		{ID: "t", Kind: "trigger", Type: "trigger"},
		{ID: "a", Kind: "task", DisplayName: "Write the change note", Position: 10},
		{ID: "if", Kind: "control", Type: "if", Label: "enhancement?", Position: 20},
		{ID: "b", Kind: "task", DisplayName: "Build and test", ParentID: "if", Position: 25},
		{ID: "c", Kind: "task", Label: "Push the PR", ParentID: "if", Position: 30},
		{ID: "end", Kind: "control", Type: "end", ParentID: "if", Position: 40},
	}
	steps := catalogStepsFromDocument(blocks)
	if len(steps) != 4 {
		t.Fatalf("got %d steps, want 4: %+v", len(steps), steps)
	}
	wantLabels := []string{"Write the change note", "enhancement?", "Build and test", "Push the PR"}
	for i, want := range wantLabels {
		if steps[i].Label != want {
			t.Errorf("step %d label = %q, want %q", i+1, steps[i].Label, want)
		}
		if steps[i].Number != i+1 {
			t.Errorf("step %d Number = %d, want %d", i+1, steps[i].Number, i+1)
		}
	}
	if steps[1].Kind != "If" {
		t.Errorf("the If block is not named as one: %+v", steps[1])
	}
	for i, step := range steps {
		wantDepth := 0
		if i >= 2 {
			wantDepth = 1
		}
		if step.Depth != wantDepth {
			t.Errorf("step %d Depth = %d, want %d", i+1, step.Depth, wantDepth)
		}
	}
}

// A chain whose steps are all filtered out must not print an empty numbered
// list; the page falls back to the outcome sentence alone.
func TestCatalogStepsFromDocumentEmptyWhenOnlyBoundaries(t *testing.T) {
	steps := catalogStepsFromDocument([]db.DocumentBlock{
		{ID: "t", Kind: "trigger"},
		{ID: "end", Kind: "control", Type: "end"},
	})
	if len(steps) != 0 {
		t.Fatalf("got %d steps, want none: %+v", len(steps), steps)
	}
}

// A refused submission re-renders with what was typed, not the stored default:
// otherwise the browser's Back button is the only way to keep an answer, and
// the field the requester got wrong is the one that wants keeping.
func TestCatalogFieldsPreferSubmittedValues(t *testing.T) {
	fields := []db.TaskInputField{
		{Name: "server", Label: "Server", DefaultValue: "web-01"},
		{Name: "reason", Label: "Reason", DataType: "long_text"},
	}
	submitted := map[string][]string{"server": {"db-02"}, "reason": {"Needs the patch"}}
	view := catalogOrderFields(fields, submitted)
	if got, want := view[0].Value, "db-02"; got != want {
		t.Errorf("server value = %q, want the submitted %q", got, want)
	}
	if got, want := view[1].Value, "Needs the patch"; got != want {
		t.Errorf("reason value = %q, want the submitted %q", got, want)
	}

	untouched := catalogOrderFields(fields, map[string][]string{})
	if got, want := untouched[0].Value, "web-01"; got != want {
		t.Errorf("server value = %q, want the stored default %q", got, want)
	}
}

// An unchecked checkbox is absent from the form, so it must not fall back to a
// stored default of "yes": that would re-tick the box the requester cleared.
func TestCatalogFieldsTreatAbsentCheckboxAsUnchecked(t *testing.T) {
	fields := []db.TaskInputField{
		{Name: "notify", Label: "Notify", DataType: "boolean", DefaultValue: "yes"},
	}
	view := catalogOrderFields(fields, map[string][]string{"other": {"x"}})
	if view[0].Value != "" {
		t.Errorf("notify value = %q, want it cleared", view[0].Value)
	}
	fresh := catalogOrderFields(fields, nil)
	if fresh[0].Value != "yes" {
		t.Errorf("first render notify value = %q, want the stored default", fresh[0].Value)
	}
}

// Help text sits outside the label and is linked with aria-describedby. Inside
// the label it becomes part of the control's accessible name, which is what
// made a textarea announce itself as "What to do Write why it was refused".
func TestCatalogOrderFieldsKeepHelpTextOutOfTheLabel(t *testing.T) {
	out := renderOrderPage(t, taskCatalogOrderView{
		Slug: "ship", DisplayName: "Ship a change",
		Fields: []catalogFieldView{{
			TaskInputField: db.TaskInputField{
				Name: "what_to_do", Label: "What to do", DataType: "long_text",
				HelpText: "One sentence. The steps do the detail.",
			},
			Value: "Add the flag",
		}},
	})
	if !strings.Contains(out, `aria-describedby="help-what_to_do"`) {
		t.Errorf("the field is not linked to its help text:\n%s", out)
	}
	labelOpen := strings.Index(out, `<label class="ui-order-field-label" for="input-what_to_do">`)
	if labelOpen < 0 {
		t.Fatalf("no label for the field:\n%s", out)
	}
	labelClose := strings.Index(out[labelOpen:], "</label>")
	if labelClose < 0 {
		t.Fatalf("unclosed label:\n%s", out)
	}
	if strings.Contains(out[labelOpen:labelOpen+labelClose], "One sentence") {
		t.Errorf("the help text is inside the label:\n%s", out)
	}
	if !strings.Contains(out, "Add the flag") {
		t.Errorf("the submitted value is not in the field:\n%s", out)
	}
}

// An order page that cannot be placed renders the reason instead of a Start
// button, so nobody types into a form that was never going to work.
func TestCatalogOrderPageHidesStartWhenNotOrderable(t *testing.T) {
	out := renderOrderPage(t, taskCatalogOrderView{
		Slug: "dead", DisplayName: "Inactive thing", Problem: "This definition is inactive.",
	})
	if strings.Contains(out, `type="submit"`) {
		t.Errorf("an inactive definition still offers a Start button:\n%s", out)
	}
	if !strings.Contains(out, "This definition is inactive.") {
		t.Errorf("the reason is not shown:\n%s", out)
	}
}

// A container must be unmistakably different from a leaf on the rendered card,
// and the card must be a link to that definition's order page: the grid holds
// no forms, so a search that swaps the results cannot throw away typing.
func TestTaskCatalogResultsLinksToOrderPages(t *testing.T) {
	tpl, err := template.ParseGlob(filepath.Join("..", "..", "web", "templates", "*.html"))
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	data := map[string]any{
		"TaskCatalogTotal": 2,
		"TaskCatalogCategories": []taskCatalogCategory{{
			Name: "operations",
			Entries: []taskCatalogEntry{
				{Slug: "upgrade-server", DisplayName: "Upgrade Server", IsContainer: true, StepCount: 1},
				{Slug: "reboot-server", DisplayName: "Reboot Server"},
			},
		}},
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "task-catalog-results", data); err != nil {
		t.Fatalf("ExecuteTemplate(task-catalog-results): %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Chain · 1 step",
		"Single task",
		`id="definition-upgrade-server"`,
		"Start chain",
		`href="/task-catalogue/upgrade-server/start"`,
		`href="/task-catalogue/reboot-server/start"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("catalogue results missing %q", want)
		}
	}
	// The grid is a set of links. A form in it would be state a search swap
	// destroys, which is the bug this shape exists to remove.
	for _, unwanted := range []string{"<form", "hx-post", "<input"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("catalogue results still contain %q:\n%s", unwanted, out)
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
