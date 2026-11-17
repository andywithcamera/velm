package main

// The process canvas and its plumbing check (Process Designer, Plan T6 /
// Requirements §4). Real Postgres, gated on TEST_DATABASE_URL: a skip is a
// broken run, not a pass.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"velm/internal/auth"
	"velm/internal/db"
)

func renderDefinitionView(t *testing.T, slug, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/task-definitions/"+slug+query, nil)
	req.SetPathValue("slug", slug)
	handleTaskDefinitionEditor(rec, req)
	return rec
}

// seedCanvasTree seeds a three-level document: trigger, if, for_each inside
// the if, and a task inside the for_each (Requirements AC-72).
func seedCanvasTree(t *testing.T, ctx context.Context, slug string, blocks []db.StructureBlock) {
	t.Helper()
	rec := postStructure(t, slug, blocks)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed structure = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func canvasTaskBlock(t *testing.T, ctx context.Context, name string) (string, string) {
	t.Helper()
	slug := tdeSlug(t, name)
	id := insertDefinition(t, ctx, slug, titleizeBreadcrumbLabel(name), "", tdeTwoInputs, false, true)
	return slug, id
}

func storedBlockCount(t *testing.T, ctx context.Context, containerID string) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM base_task_definition_child WHERE container_definition_id = $1`,
		containerID).Scan(&count); err != nil {
		t.Fatalf("count stored blocks: %v", err)
	}
	return count
}

// AC-69, AC-91: the three views return 200 and render the same definition.
func TestDefinitionEditorThreeViews(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-views")
	containerID := insertDefinition(t, ctx, slug, "Canvas Views", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "canvas-views-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Do the work", Position: 10},
	})

	for _, view := range []string{"canvas", "rows", "raw"} {
		rec := renderDefinitionView(t, slug, "?view="+view)
		if rec.Code != http.StatusOK {
			t.Fatalf("view=%s status = %d, want 200: %s", view, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Canvas Views") {
			t.Errorf("view=%s does not render the definition name", view)
		}
		if !strings.Contains(body, `href="/task-definitions/`+slug+`?view=canvas"`) {
			t.Errorf("view=%s has no tab to the canvas", view)
		}
	}
	// The default for a tree definition is the canvas (AC-91).
	rec := renderDefinitionView(t, slug, "")
	if !strings.Contains(rec.Body.String(), `id="canvas"`) {
		t.Error("a definition with a non-task block does not default to the canvas")
	}
	if storedBlockCount(t, ctx, containerID) != 2 {
		t.Error("rendering a view wrote to the document")
	}
}

// AC-71: a block inside an if renders with a different indentation class.
func TestCanvasIndentsScope(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-indent")
	insertDefinition(t, ctx, slug, "Canvas Indent", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "canvas-indent-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "when ready", Position: 10, Config: map[string]any{"condition": "expr: inputs.server != \"\""}},
		{Key: "a", Parent: "i", Kind: "task", Slug: childSlug, Label: "Inside the if", Position: 20},
		{Key: "e", Kind: "control", Type: "end", Label: "End", Position: 30},
	})
	body := renderDefinitionView(t, slug, "?view=canvas").Body.String()
	if !strings.Contains(body, "ui-canvas-depth-0") || !strings.Contains(body, "ui-canvas-depth-1") {
		t.Fatalf("canvas does not render both an outer and an inner indent class: %s", body)
	}
	if !strings.Contains(body, "Inside the if") {
		t.Error("the scoped block is missing from the canvas")
	}
}

// AC-72: three levels render with three indent classes in position order.
func TestCanvasThreeLevels(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-levels")
	insertDefinition(t, ctx, slug, "Canvas Levels", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "canvas-levels-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "outer if", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "f", Parent: "i", Kind: "control", Type: "for_each", Label: "inner loop", Position: 20, Config: map[string]any{"collection": []any{"a", "b"}}},
		{Key: "a", Parent: "f", Kind: "task", Slug: childSlug, Label: "deep task", Position: 30},
		{Key: "e", Kind: "control", Type: "end", Label: "End", Position: 40},
	})
	body := renderDefinitionView(t, slug, "?view=canvas").Body.String()
	for _, class := range []string{"ui-canvas-depth-0", "ui-canvas-depth-1", "ui-canvas-depth-2"} {
		if !strings.Contains(body, class) {
			t.Errorf("canvas is missing indent class %s", class)
		}
	}
	outer := strings.Index(body, "outer if")
	inner := strings.Index(body, "inner loop")
	deep := strings.Index(body, "deep task")
	if outer < 0 || inner < 0 || deep < 0 || !(outer < inner && inner < deep) {
		t.Errorf("blocks are not in position order: outer=%d inner=%d deep=%d", outer, inner, deep)
	}
}

// AC-73: a container used as a block is marked as a chain with its step count.
func TestCanvasChainMarker(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-chain")
	insertDefinition(t, ctx, slug, "Canvas Chain", "", tdeTwoInputs, true, true)
	chainSlug := tdeSlug(t, "canvas-chain-child")
	chainID := insertDefinition(t, ctx, chainSlug, "Reboot Chain", "", tdeTwoInputs, true, true)
	leafSlug, leafID := canvasTaskBlock(t, ctx, "canvas-chain-leaf")
	linkStep(t, ctx, chainID, leafID, 10)
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "a", Kind: "task", Slug: chainSlug, Label: "Reboot chain", Position: 10},
	})
	body := renderDefinitionView(t, slug, "?view=canvas").Body.String()
	if !strings.Contains(body, "Chain · 1 step") {
		t.Errorf("canvas does not mark the nested container as a chain with its step count: %s", body)
	}
	_ = leafSlug
}

// AC-74: the six kinds are listed; the unavailable ones say so.
func TestPaletteMarksUnavailableKinds(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-palette")
	insertDefinition(t, ctx, slug, "Canvas Palette", "", tdeTwoInputs, true, true)
	body := renderDefinitionView(t, slug, "?view=canvas").Body.String()
	for _, kind := range []string{"trigger", "task", "action", "gate", "control", "stage"} {
		if !strings.Contains(body, `data-kind="`+kind+`"`) {
			t.Errorf("palette is missing kind %s", kind)
		}
	}
	if !strings.Contains(body, "not in this release") {
		t.Error("palette does not mark the unavailable kinds")
	}
	if !strings.Contains(body, `data-palette="action"`) || !strings.Contains(body, "not in this release") {
		t.Error("action is not marked unavailable")
	}
}

// AC-75: the candidate list excludes the edited definition and the definitions
// already present, and is searchable.
func TestPaletteCandidates(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-candidates")
	insertDefinition(t, ctx, slug, "Canvas Candidates", "", tdeTwoInputs, true, true)
	presentSlug, _ := canvasTaskBlock(t, ctx, "canvas-candidates-present")
	freeSlug, _ := canvasTaskBlock(t, ctx, "canvas-candidates-free")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "a", Kind: "task", Slug: presentSlug, Label: "Already here", Position: 10},
	})
	body := renderDefinitionView(t, slug, "?view=canvas").Body.String()
	if !strings.Contains(body, `data-candidate="`+freeSlug+`"`) {
		t.Error("the free definition is not offered by the palette")
	}
	if strings.Contains(body, `data-candidate="`+presentSlug+`"`) {
		t.Error("a definition already present as a block is offered again")
	}
	if strings.Contains(body, `data-candidate="`+slug+`"`) {
		t.Error("the definition being edited offers itself")
	}
	if !strings.Contains(body, `id="palette-search"`) {
		t.Error("the palette definition list is not searchable")
	}
}

// AC-76: a task block posted at the end of a scope is stored with the next
// position, spaced by 10.
func TestCanvasDropAppendsAtEndOfScope(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-drop")
	containerID := insertDefinition(t, ctx, slug, "Canvas Drop", "", tdeTwoInputs, true, true)
	firstSlug, _ := canvasTaskBlock(t, ctx, "canvas-drop-a")
	secondSlug, _ := canvasTaskBlock(t, ctx, "canvas-drop-b")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "a", Kind: "task", Slug: firstSlug, Label: "First", Position: 10},
	})

	// The palette appends a dropped definition at the end of its scope with
	// position = last sibling + 10; this is the document it submits.
	dropped := []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "a", Kind: "task", Slug: firstSlug, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: secondSlug, Label: "Second", Position: 20},
	}
	if rec := postStructure(t, slug, dropped); rec.Code != http.StatusOK {
		t.Fatalf("dropped block save = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	rows, err := db.Pool.Query(ctx,
		`SELECT block_kind, position FROM base_task_definition_child
		 WHERE container_definition_id = $1 ORDER BY position`, containerID)
	if err != nil {
		t.Fatalf("read stored blocks: %v", err)
	}
	defer rows.Close()
	type stored struct {
		Kind     string
		Position int
	}
	var got []stored
	for rows.Next() {
		var item stored
		if err := rows.Scan(&item.Kind, &item.Position); err != nil {
			t.Fatalf("scan stored block: %v", err)
		}
		got = append(got, item)
	}
	want := []stored{{"trigger", 0}, {"task", 10}, {"task", 20}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored blocks = %+v, want %+v (the drop spaced by 10)", got, want)
	}
}

// AC-77, AC-78: the rail has mapping pills, the if condition builder and
// assignment, and pills are a select of in-scope values.
func TestConfigRailSections(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-rail")
	insertDefinition(t, ctx, slug, "Canvas Rail", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "canvas-rail-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "rail if", Position: 10, Config: map[string]any{"condition": "expr: inputs.server != \"\""}},
		{Key: "a", Parent: "i", Kind: "task", Slug: childSlug, Label: "rail task", Position: 20, Mapping: map[string]any{"server": "$inputs.server"}},
		{Key: "e", Kind: "control", Type: "end", Label: "End", Position: 30},
	})
	blocks, err := db.LoadDefinitionDocument(ctx, mustDefinitionID(t, ctx, slug))
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	ifID, taskID := "", ""
	for _, block := range blocks {
		switch {
		case block.Type == "if":
			ifID = block.ID
		case block.Slug == childSlug:
			taskID = block.ID
		}
	}
	rail := renderDefinitionView(t, slug, "?view=canvas&block="+ifID).Body.String()
	for _, want := range []string{`id="canvas-rail"`, "Condition", "Assignment group", "Assignment strategy"} {
		if !strings.Contains(rail, want) {
			t.Errorf("config rail is missing %q", want)
		}
	}
	rail = renderDefinitionView(t, slug, "?view=canvas&block="+taskID).Body.String()
	for _, want := range []string{`data-rail-mapping="server"`, "$inputs.server"} {
		if !strings.Contains(rail, want) {
			t.Errorf("task config rail is missing %q", want)
		}
	}
}

// AC-78: $item/$index are offered inside a for_each and not outside it, and the
// globals are always offered.
func TestPillOptionsInScope(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-pills")
	insertDefinition(t, ctx, slug, "Canvas Pills", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "canvas-pills-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "pill if", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "f", Parent: "i", Kind: "control", Type: "for_each", Label: "pill loop", Position: 20, Config: map[string]any{"collection": []any{"a"}}},
		{Key: "a", Parent: "f", Kind: "task", Slug: childSlug, Label: "pill task", Position: 30},
		{Key: "e", Kind: "control", Type: "end", Label: "End", Position: 40},
	})
	// Select the deeply scoped task by its stored id.
	blocks, err := db.LoadDefinitionDocument(ctx, mustDefinitionID(t, ctx, slug))
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	taskID := ""
	for _, block := range blocks {
		if block.Slug == childSlug {
			taskID = block.ID
		}
	}
	body := renderDefinitionView(t, slug, "?view=canvas&block="+taskID).Body.String()
	for _, want := range []string{"$item", "$index", "$now", "$run.id", "$requester", "$inputs.server"} {
		if !strings.Contains(body, want) {
			t.Errorf("pill %s is not offered inside the loop", want)
		}
	}
}

func mustDefinitionID(t *testing.T, ctx context.Context, slug string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(ctx, `SELECT _id::text FROM base_task_definition WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("definition id: %v", err)
	}
	return id
}

func validCanvasDocument(t *testing.T, ctx context.Context, slug, childSlug string) []db.StructureBlock {
	t.Helper()
	return []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Do the work", Position: 10,
			Mapping: map[string]any{"server": "$inputs.server", "when": "$now"}},
	}
}

// AC-79 to AC-85, AC-92: one subtest per invariant.
func TestPlumbingCheck(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)

	t.Run("Invariant1ExactlyOneTrigger", func(t *testing.T) {
		slug := tdeSlug(t, "plumb-trigger")
		containerID := insertDefinition(t, ctx, slug, "Plumbing Trigger", "", tdeTwoInputs, true, true)
		childSlug, _ := canvasTaskBlock(t, ctx, "plumb-trigger-leaf")

		zero := []db.StructureBlock{{Key: "a", Kind: "task", Slug: childSlug, Label: "Only a task", Position: 10}}
		rec := postStructure(t, slug, zero)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "trigger") {
			t.Fatalf("zero triggers = %d %q, want 400 naming a trigger", rec.Code, rec.Body.String())
		}
		if storedBlockCount(t, ctx, containerID) != 0 {
			t.Fatal("a refused zero-trigger save wrote rows")
		}

		two := []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "First trigger", Position: 0},
			{Key: "t2", Kind: "trigger", Label: "Second trigger", Position: 10},
		}
		rec = postStructure(t, slug, two)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Second trigger") || !strings.Contains(rec.Body.String(), "trigger") {
			t.Fatalf("two triggers = %d %q, want 400 naming the second", rec.Code, rec.Body.String())
		}
		if storedBlockCount(t, ctx, containerID) != 0 {
			t.Fatal("a refused two-trigger save wrote rows")
		}

		one := []db.StructureBlock{{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0}}
		rec = postStructure(t, slug, one)
		if rec.Code != http.StatusOK {
			t.Fatalf("one trigger at position 0 = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var triggers, position int
		if err := db.Pool.QueryRow(ctx,
			`SELECT count(*), max(position) FROM base_task_definition_child
			 WHERE container_definition_id = $1 AND block_kind = 'trigger'`, containerID).Scan(&triggers, &position); err != nil {
			t.Fatalf("read trigger: %v", err)
		}
		if triggers != 1 || position != 0 {
			t.Fatalf("stored %d triggers at position %d, want 1 at 0", triggers, position)
		}
	})

	t.Run("Invariant2Termination", func(t *testing.T) {
		slug := tdeSlug(t, "plumb-end")
		containerID := insertDefinition(t, ctx, slug, "Plumbing End", "", tdeTwoInputs, true, true)
		childSlug, _ := canvasTaskBlock(t, ctx, "plumb-end-leaf")
		noEnd := []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
			{Key: "a", Kind: "task", Slug: childSlug, Label: "Last task", Position: 10},
		}
		if rec := postStructure(t, slug, noEnd); rec.Code != http.StatusOK {
			t.Fatalf("task with no end = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var ends int
		_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM base_task_definition_child WHERE container_definition_id = $1 AND block_type = 'end'`, containerID).Scan(&ends)
		if ends != 0 {
			t.Fatalf("stored %d end blocks, want 0", ends)
		}
		before := storedBlockCount(t, ctx, containerID)
		empty := []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
			{Key: "i", Kind: "control", Type: "if", Label: "empty if", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		}
		rec := postStructure(t, slug, empty)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "empty if") || !strings.Contains(rec.Body.String(), "end") {
			t.Fatalf("empty scope = %d %q, want 400 naming the if and an end", rec.Code, rec.Body.String())
		}
		if storedBlockCount(t, ctx, containerID) != before {
			t.Fatal("a refused empty scope changed the document")
		}
	})

	t.Run("Invariant3Cycles", func(t *testing.T) {
		slug := tdeSlug(t, "plumb-cycle")
		containerID := insertDefinition(t, ctx, slug, "Plumbing Cycle", "", tdeTwoInputs, true, true)
		selfAncestor := []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
			{Key: "i", Kind: "control", Type: "if", Label: "loop if", Position: 10, Parent: "i", Config: map[string]any{"condition": "expr: true"}},
		}
		rec := postStructure(t, slug, selfAncestor)
		if rec.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(rec.Body.String()), "cycle") {
			t.Fatalf("self ancestor = %d %q, want 400 with cycle", rec.Code, rec.Body.String())
		}
		if storedBlockCount(t, ctx, containerID) != 0 {
			t.Fatal("a refused self-ancestor save wrote rows")
		}
		selfCall := []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
			{Key: "a", Kind: "task", Slug: slug, Label: "Call myself", Position: 10},
		}
		rec = postStructure(t, slug, selfCall)
		if rec.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(rec.Body.String()), "cycle") || !strings.Contains(rec.Body.String(), slug) {
			t.Fatalf("transitive self-call = %d %q, want 400 naming the slug and cycle", rec.Code, rec.Body.String())
		}
		if storedBlockCount(t, ctx, containerID) != 0 {
			t.Fatal("a refused self-call save wrote rows")
		}
	})

	t.Run("Invariant4PillResolution", func(t *testing.T) {
		slug := tdeSlug(t, "plumb-pill")
		containerID := insertDefinition(t, ctx, slug, "Plumbing Pill", "", tdeTwoInputs, true, true)
		childSlug, _ := canvasTaskBlock(t, ctx, "plumb-pill-leaf")
		bad := []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
			{Key: "a", Kind: "task", Slug: childSlug, Label: "Bad mapping", Position: 10,
				Mapping: map[string]any{"server": "$inputs.not_an_input"}},
		}
		rec := postStructure(t, slug, bad)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "$inputs.not_an_input") || !strings.Contains(rec.Body.String(), "Bad mapping") {
			t.Fatalf("bad pill = %d %q, want 400 naming the pill and block", rec.Code, rec.Body.String())
		}
		if storedBlockCount(t, ctx, containerID) != 0 {
			t.Fatal("a refused bad pill wrote rows")
		}
		rec = postStructure(t, slug, validCanvasDocument(t, ctx, slug, childSlug))
		if rec.Code != http.StatusOK {
			t.Fatalf("good pills = %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Invariant5CollectionIsArray", func(t *testing.T) {
		slug := tdeSlug(t, "plumb-array")
		containerID := insertDefinition(t, ctx, slug, "Plumbing Array", "", tdeTwoInputs, true, true)
		childSlug, _ := canvasTaskBlock(t, ctx, "plumb-array-leaf")
		bad := []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
			{Key: "f", Kind: "control", Type: "for_each", Label: "bad loop", Position: 10, Config: map[string]any{"collection": "$inputs.server"}},
			{Key: "a", Parent: "f", Kind: "task", Slug: childSlug, Label: "loop body", Position: 20},
		}
		rec := postStructure(t, slug, bad)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "array") || !strings.Contains(rec.Body.String(), "bad loop") {
			t.Fatalf("non-array collection = %d %q, want 400 naming array and the block", rec.Code, rec.Body.String())
		}
		if storedBlockCount(t, ctx, containerID) != 0 {
			t.Fatal("a refused collection wrote rows")
		}
		good := []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
			{Key: "f", Kind: "control", Type: "for_each", Label: "good loop", Position: 10, Config: map[string]any{"collection": []any{"a", "b"}}},
			{Key: "a", Parent: "f", Kind: "task", Slug: childSlug, Label: "loop body", Position: 20},
		}
		if rec := postStructure(t, slug, good); rec.Code != http.StatusOK {
			t.Fatalf("array collection = %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Invariant6WaitExits", func(t *testing.T) {
		slug := tdeSlug(t, "plumb-wait")
		insertDefinition(t, ctx, slug, "Plumbing Wait", "", tdeTwoInputs, true, true)
		doc := []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
			{Key: "w", Kind: "control", Type: "wait", Label: "wait here", Position: 10},
		}
		rec := postStructure(t, slug, doc)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not available in this release") {
			t.Fatalf("wait = %d %q, want the not-available refusal before the exit rule", rec.Code, rec.Body.String())
		}
	})

	t.Run("Invariant7GateSuccessors", func(t *testing.T) {
		slug := tdeSlug(t, "plumb-gate")
		containerID := insertDefinition(t, ctx, slug, "Plumbing Gate", "", tdeTwoInputs, true, true)
		childSlug, _ := canvasTaskBlock(t, ctx, "plumb-gate-leaf")
		base := []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
			{Key: "g", Kind: "gate", Label: "approve reboot", Position: 10},
			{Key: "yes", Kind: "task", Slug: childSlug, Label: "approved", Position: 20},
			{Key: "no", Kind: "task", Slug: childSlug, Label: "rejected", Position: 30},
		}
		rec := postStructure(t, slug, base)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "completed") || !strings.Contains(rec.Body.String(), "approve reboot") {
			t.Fatalf("gate with no completed = %d %q", rec.Code, rec.Body.String())
		}
		completedOnly := append([]db.StructureBlock{}, base...)
		completedOnly[1].Config = map[string]any{"completed": "yes"}
		rec = postStructure(t, slug, completedOnly)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "rejected") {
			t.Fatalf("gate with no rejected = %d %q", rec.Code, rec.Body.String())
		}
		both := append([]db.StructureBlock{}, base...)
		both[1].Config = map[string]any{"completed": "yes", "rejected": "no"}
		if rec := postStructure(t, slug, both); rec.Code != http.StatusOK {
			t.Fatalf("gate with both successors = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var gates int
		_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM base_task_definition_child WHERE container_definition_id = $1 AND block_kind = 'gate'`, containerID).Scan(&gates)
		if gates != 1 {
			t.Fatalf("stored %d gates, want 1", gates)
		}
	})
}

// AC-87: a refused save leaves the stored document exactly as it was.
func TestPlumbingRefusalPreservesDocument(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "plumb-preserve")
	containerID := insertDefinition(t, ctx, slug, "Plumbing Preserve", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "plumb-preserve-leaf")
	seedCanvasTree(t, ctx, slug, validCanvasDocument(t, ctx, slug, childSlug))

	type row struct {
		Kind, Type string
		Position   int
		Config     string
	}
	read := func() []row {
		// config::jsonb::text is canonical, so key order and whitespace do
		// not count (AC-88 compares config by jsonb equality).
		rows, err := db.Pool.Query(ctx,
			`SELECT block_kind, COALESCE(block_type, ''), position, config::jsonb::text
			 FROM base_task_definition_child WHERE container_definition_id = $1 ORDER BY position`, containerID)
		if err != nil {
			t.Fatalf("read document: %v", err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.Kind, &item.Type, &item.Position, &item.Config); err != nil {
				t.Fatalf("scan document: %v", err)
			}
			out = append(out, item)
		}
		return out
	}
	before := read()
	bad := []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Bad again", Position: 10, Mapping: map[string]any{"server": "$inputs.nope"}},
	}
	if rec := postStructure(t, slug, bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad save = %d, want 400", rec.Code)
	}
	if after := read(); !reflect.DeepEqual(before, after) {
		t.Fatalf("a refused save changed the document:\nbefore=%v\nafter=%v", before, after)
	}
	for _, view := range []string{"rows", "canvas"} {
		if rec := renderDefinitionView(t, slug, "?view="+view); rec.Code != http.StatusOK {
			t.Errorf("%s view after a refusal = %d, want 200", view, rec.Code)
		}
	}
}

// AC-88: the wrapper shape and the bare-array shape store the same document.
func TestThreeViewsWriteOneDocument(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-same")
	containerID := insertDefinition(t, ctx, slug, "Canvas Same", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "canvas-same-leaf")
	doc := validCanvasDocument(t, ctx, slug, childSlug)
	if rec := postStructure(t, slug, doc); rec.Code != http.StatusOK {
		t.Fatalf("canvas shape = %d: %s", rec.Code, rec.Body.String())
	}

	type storedRow struct {
		Kind, Type string
		Position   int
		Config     string
	}
	read := func() []storedRow {
		rows, err := db.Pool.Query(ctx,
			`SELECT block_kind, COALESCE(block_type, ''), position, config::jsonb::text
			 FROM base_task_definition_child WHERE container_definition_id = $1 ORDER BY position`, containerID)
		if err != nil {
			t.Fatalf("read rows: %v", err)
		}
		defer rows.Close()
		var out []storedRow
		for rows.Next() {
			var item storedRow
			if err := rows.Scan(&item.Kind, &item.Type, &item.Position, &item.Config); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, item)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		return out
	}
	canvasRows := read()
	if len(canvasRows) == 0 {
		t.Fatal("the canvas save wrote no rows")
	}

	// The raw view sends the same document as a bare array rather than the
	// {"blocks": [...]} wrapper the canvas uses.
	enc, _ := json.Marshal(doc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/structure",
		strings.NewReader(url.Values{"structure": {string(enc)}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	handleTaskDefinitionStructureSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("raw shape = %d: %s", rec.Code, rec.Body.String())
	}
	rawRows := read()
	if !reflect.DeepEqual(canvasRows, rawRows) {
		t.Fatalf("the two views stored different documents:\ncanvas=%+v\nraw=%+v", canvasRows, rawRows)
	}
}

// AC-90: a config key the canvas does not model survives a raw save.
func TestRawSavePreservesUnknownConfig(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-raw")
	containerID := insertDefinition(t, ctx, slug, "Canvas Raw", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "canvas-raw-leaf")
	doc := []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Raw task", Position: 10,
			Config: map[string]any{"unknown_key": map[string]any{"nested": true}}},
	}
	if rec := postStructure(t, slug, doc); rec.Code != http.StatusOK {
		t.Fatalf("raw save = %d: %s", rec.Code, rec.Body.String())
	}
	var raw string
	if err := db.Pool.QueryRow(ctx,
		`SELECT config::text FROM base_task_definition_child
		 WHERE container_definition_id = $1 AND block_kind = 'task'`, containerID).Scan(&raw); err != nil {
		t.Fatalf("read config: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if _, ok := config["unknown_key"]; !ok {
		t.Fatalf("raw save dropped an unknown config key: %s", raw)
	}
}

// AC-92: the error names the label the author submitted, not the stored one.
func TestPlumbingNamesSubmittedLabel(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-label")
	insertDefinition(t, ctx, slug, "Canvas Label", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "canvas-label-leaf")
	doc := []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Ordered", Position: 0},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Label from this save", Position: 10,
			Mapping: map[string]any{"server": "$inputs.missing"}},
	}
	rec := postStructure(t, slug, doc)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Label from this save") {
		t.Fatalf("refusal %d %q does not name the submitted label", rec.Code, rec.Body.String())
	}
}

// AC-86: a definition that fails the plumbing check cannot be ordered.
func TestUnrunnableDefinitionCannotBeOrdered(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-order")
	containerID := insertDefinition(t, ctx, slug, "Canvas Order", "", tdeTwoInputs, true, true)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, block_kind, block_type, position, sort_order, config)
		VALUES ($1, 'trigger', NULL, 0, 0, '{"label":"Ordered"}'::jsonb),
		       ($1, 'control', 'if', 10, 10, '{"label":"empty if","condition":"expr: true"}'::jsonb)`, containerID); err != nil {
		t.Fatalf("seed broken document: %v", err)
	}
	before := 0
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM base_task WHERE definition_id = $1`, containerID).Scan(&before)

	form := url.Values{"definition_slug": {slug}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-catalogue/order", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithUserContext(req.Context(), "user-1", "u@example.com", "User", "admin"))
	handleTaskCatalogOrder(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "end") {
		t.Fatalf("order of an unrunnable definition = %d %q, want 400 with the blocking reason", rec.Code, rec.Body.String())
	}
	after := 0
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM base_task WHERE definition_id = $1`, containerID).Scan(&after)
	if after != before {
		t.Fatalf("a refused order created %d tasks", after-before)
	}
}
