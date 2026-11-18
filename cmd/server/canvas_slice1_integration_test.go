package main

// Slice 1 of the designer rework: the canvas is the only authoring view, the
// trigger is structural, and a refusal keeps its page (Design Vision D1, D6,
// D7; Requirements AC-1 to AC-18). Real Postgres, gated on TEST_DATABASE_URL:
// a skip is a broken run, not a pass.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"velm/internal/db"
)

// postStructureWithHeaders posts a structure document the way a browser form
// does, and optionally as HTMX does, so both refusal paths are exercised.
func postStructureWithHeaders(t *testing.T, slug string, blocks []db.StructureBlock, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"blocks": blocks})
	if err != nil {
		t.Fatalf("encode structure: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/structure",
		strings.NewReader(url.Values{"structure": {string(payload)}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req.SetPathValue("slug", slug)
	handleTaskDefinitionStructureSave(rec, req)
	return rec
}

// sliceOneContainer seeds a container definition with one task child.
func sliceOneContainer(t *testing.T, ctx context.Context, name string) (string, string, string) {
	t.Helper()
	slug := tdeSlug(t, name)
	id := insertDefinition(t, ctx, slug, "Slice One "+name, "", "", true, true)
	childSlug := tdeSlug(t, name+"-child")
	insertDefinition(t, ctx, childSlug, "Child "+name, "", "", false, true)
	return slug, id, childSlug
}

func storedKinds(t *testing.T, ctx context.Context, containerID string) []string {
	t.Helper()
	rows, err := db.Pool.Query(ctx, `
		SELECT block_kind FROM base_task_definition_child
		WHERE container_definition_id = $1
		ORDER BY position, _created_at`, containerID)
	if err != nil {
		t.Fatalf("read stored tree: %v", err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scan stored tree: %v", err)
		}
		kinds = append(kinds, kind)
	}
	return kinds
}

// AC-1: no tab bar, and neither support surface is linked.
func TestEditorHasNoTabBar(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice1-tabs")
	insertDefinition(t, ctx, slug, "No Tabs", "", "", true, true)

	body := renderDefinitionView(t, slug, "").Body.String()
	for _, unwanted := range []string{"?view=rows", "?view=raw", `class="ui-def-view-tab"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("editor still contains %q", unwanted)
		}
	}
}

// AC-2: the default view is the canvas.
func TestEditorDefaultsToCanvas(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice1-default")
	insertDefinition(t, ctx, slug, "Defaults To Canvas", "", "", true, true)

	rec := renderDefinitionView(t, slug, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("editor = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`id="canvas"`, `id="canvas-form"`} {
		if !strings.Contains(body, want) {
			t.Errorf("default editor body is missing %s", want)
		}
	}
}

// AC-3: the rows stepper survives at ?view=rows, unlinked.
func TestRowsViewStillReachableByURL(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice1-rows")
	insertDefinition(t, ctx, slug, "Rows View", "", "", true, true)

	rec := renderDefinitionView(t, slug, "?view=rows")
	if rec.Code != http.StatusOK {
		t.Fatalf("?view=rows = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="ui-def-stepper"`) {
		t.Error("?view=rows does not render the stepper")
	}
	if strings.Contains(body, `id="canvas-form"`) {
		t.Error("?view=rows still renders the canvas form")
	}
}

// AC-4: the raw structure editor survives at ?view=raw, unlinked.
func TestRawViewStillReachableByURL(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice1-raw")
	insertDefinition(t, ctx, slug, "Raw View", "", "", true, true)

	rec := renderDefinitionView(t, slug, "?view=raw")
	if rec.Code != http.StatusOK {
		t.Fatalf("?view=raw = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="raw-form"`) {
		t.Error("?view=raw does not render the raw form")
	}
	if strings.Contains(body, `id="canvas-form"`) {
		t.Error("?view=raw still renders the canvas form")
	}
}

// AC-9 and AC-10: Start is rendered as a fixed node with no delete, move or
// drag affordance.
func TestStartNodeHasNoControls(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice1-start")
	insertDefinition(t, ctx, slug, "Start Node", "", "", true, true)

	body := renderDefinitionView(t, slug, "").Body.String()
	start := strings.Index(body, `data-kind="trigger"`)
	if start < 0 {
		t.Fatalf("the canvas does not render a trigger node: %s", body)
	}
	tagStart := strings.LastIndex(body[:start], "<li")
	tagEnd := strings.Index(body[start:], ">")
	if tagStart < 0 || tagEnd < 0 {
		t.Fatal("could not isolate the trigger node tag")
	}
	tag := body[tagStart : start+tagEnd+1]
	for _, unwanted := range []string{"data-delete", "data-move", `draggable="true"`} {
		if strings.Contains(tag, unwanted) {
			t.Errorf("the Start node carries %q: %s", unwanted, tag)
		}
	}
	if !strings.Contains(body[start:], "Start") {
		t.Error("the trigger node is not labelled Start")
	}
}

// AC-11: the palette offers no trigger.
func TestPaletteHasNoTrigger(t *testing.T) {
	for _, kind := range canvasPalette() {
		if kind.Kind == "trigger" {
			t.Fatalf("the palette still offers a trigger: %+v", kind)
		}
	}
}

// AC-12 (defect a): a task-only tree is refused by name and nothing is written.
func TestTaskOnlyTreeIsRefusedAndNamed(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug, containerID, childSlug := sliceOneContainer(t, ctx, "task-only")
	before := storedBlockCount(t, ctx, containerID)

	rec := postStructureWithHeaders(t, slug, []db.StructureBlock{
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Do the work", Position: 10},
	}, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("task-only save = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "A definition needs exactly one trigger block") {
		t.Errorf("refusal does not name the missing trigger: %s", rec.Body.String())
	}
	if after := storedBlockCount(t, ctx, containerID); after != before {
		t.Errorf("refused save wrote rows: %d -> %d", before, after)
	}
}

// AC-13: two root triggers are refused, naming the second.
func TestTwoTriggersRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug, _, _ := sliceOneContainer(t, ctx, "two-triggers")

	rec := postStructureWithHeaders(t, slug, []db.StructureBlock{
		{Key: "t1", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "t2", Kind: "trigger", Label: "Second Start", Position: 10},
	}, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("two triggers = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Second Start") {
		t.Errorf("refusal does not name the second trigger: %s", rec.Body.String())
	}
}

// AC-14: a trigger that is not first is refused with the documented message.
func TestTriggerNotFirstRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug, _, childSlug := sliceOneContainer(t, ctx, "trigger-late")

	rec := postStructureWithHeaders(t, slug, []db.StructureBlock{
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Do the work", Position: 0},
		{Key: "t", Kind: "trigger", Label: "Start", Position: 10},
	}, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("late trigger = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "the trigger must be the first block in the document") {
		t.Errorf("refusal does not name the trigger position: %s", rec.Body.String())
	}
}

// AC-15: with Start present the document saves and stores in submitted order.
func TestTreeWithStartSaves(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug, containerID, childSlug := sliceOneContainer(t, ctx, "with-start")

	rec := postStructureWithHeaders(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: childSlug, Label: "Second", Position: 20},
	}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("tree with Start = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	kinds := storedKinds(t, ctx, containerID)
	want := []string{"trigger", "task", "task"}
	if len(kinds) != len(want) {
		t.Fatalf("stored kinds = %v, want %v", kinds, want)
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("stored kinds = %v, want %v", kinds, want)
		}
	}
}

// AC-16 (defect b): a refusal without HX-Request returns the whole editor.
func TestRefusalWithoutHTMXRendersFullPage(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug, containerID, childSlug := sliceOneContainer(t, ctx, "refusal-page")
	before := storedBlockCount(t, ctx, containerID)

	rec := postStructureWithHeaders(t, slug, []db.StructureBlock{
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Do the work", Position: 10},
	}, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("refusal = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"<html", `id="canvas-form"`, "A definition needs exactly one trigger block", "/task-definitions"} {
		if !strings.Contains(body, want) {
			t.Errorf("plain-form refusal is missing %q", want)
		}
	}
	if after := storedBlockCount(t, ctx, containerID); after != before {
		t.Errorf("refused save wrote rows: %d -> %d", before, after)
	}
}

// AC-17: the same refusal under HTMX is the fragment, retargeted at #step-errors.
func TestRefusalWithHTMXRendersFragment(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug, _, childSlug := sliceOneContainer(t, ctx, "refusal-fragment")

	rec := postStructureWithHeaders(t, slug, []db.StructureBlock{
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Do the work", Position: 10},
	}, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("htmx refusal = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("HX-Retarget"); got != "#step-errors" {
		t.Errorf("HX-Retarget = %q, want #step-errors", got)
	}
	if strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("htmx refusal returned a full page: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "A definition needs exactly one trigger block") {
		t.Errorf("htmx refusal does not carry the message: %s", rec.Body.String())
	}
}

// AC-18: the canvas form is HTMX-targeted and still a plain form.
func TestCanvasFormIsHTMXTargeted(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice1-form")
	insertDefinition(t, ctx, slug, "Canvas Form", "", "", true, true)

	body := renderDefinitionView(t, slug, "").Body.String()
	start := strings.Index(body, `id="canvas-form"`)
	if start < 0 {
		t.Fatalf("no canvas form: %s", body)
	}
	end := strings.Index(body[start:], ">")
	tag := body[start : start+end+1]
	for _, want := range []string{`method="post"`, `hx-post="/task-definitions/` + slug + `/structure"`, `hx-target="#step-errors"`, `hx-swap="innerHTML"`} {
		if !strings.Contains(tag, want) {
			t.Errorf("canvas form is missing %q: %s", want, tag)
		}
	}
}

// AC-8: a zero-block definition opens with Start and saves unchanged, leaving
// exactly one trigger row.
func TestZeroBlockSaveSucceeds(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "zero-block-save")
	containerID := insertDefinition(t, ctx, slug, "Zero Block Save", "", "", true, true)

	body := renderDefinitionView(t, slug, "").Body.String()
	if !strings.Contains(body, `data-kind="trigger"`) {
		t.Fatalf("a zero-block definition did not open with Start: %s", body)
	}
	document, err := db.StoredStructureDocument(ctx, containerID)
	if err != nil {
		t.Fatalf("read stored document: %v", err)
	}
	rec := postStructureWithHeaders(t, slug, document, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("unchanged save = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := len(storedKinds(t, ctx, containerID)); got != 1 {
		t.Fatalf("stored %d rows after save, want the single trigger", got)
	}
	if kinds := storedKinds(t, ctx, containerID); kinds[0] != "trigger" {
		t.Fatalf("stored block = %q, want trigger", kinds[0])
	}
}
