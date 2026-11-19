package main

// Slice 2 of the designer rework: the two-column geometry, the draggable
// palette, the root drop target, the 2px insertion indicator and the palette
// toggle (Requirements AC-19 to AC-25, AC-30, AC-32, AC-34). AC-24's THEN/ELSE
// scope containers are engine-side work already delivered (AC-55); their
// canvas rendering belongs to slice 3 and is deliberately not asserted here.
//
// The file-level greps the tester used are kept as tests, so the PM can re-run
// them: a rename that is not in the Requirements fails the suite.
//
// Real Postgres, gated on TEST_DATABASE_URL: a skip is a broken run, not a pass.

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"velm/internal/db"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}

// cssRule returns the declarations of the first rule whose selector is exactly
// selector.
func cssRule(t *testing.T, css, selector string) string {
	t.Helper()
	at := strings.Index(css, selector+" {")
	if at < 0 {
		t.Fatalf("CSS rule %q is missing", selector)
	}
	open := at + len(selector) + 2
	close := strings.Index(css[open:], "}")
	if close < 0 {
		t.Fatalf("CSS rule %q has no closing brace", selector)
	}
	return css[open : open+close]
}

// AC-19 and AC-20: two columns, 260px then the canvas; the palette comes first
// in the DOM.
func TestCanvasLayoutIsTwoColumns(t *testing.T) {
	template := repoFile(t, "../../web/templates/task-definitions-view.html")
	rule := cssRule(t, template, ".ui-canvas-layout")
	if !strings.Contains(rule, "grid-template-columns: 260px minmax(0, 1fr);") {
		t.Errorf(".ui-canvas-layout is not 260px minmax(0, 1fr):%s", rule)
	}
	if strings.Contains(rule, "18rem") || strings.Contains(rule, "15rem") {
		t.Errorf(".ui-canvas-layout still carries a rem-sized column:%s", rule)
	}

	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-slice2-layout")
	insertDefinition(t, ctx, slug, "Slice 2 Layout", "", tdeTwoInputs, true, true)
	body := renderDefinitionView(t, slug, "?view=canvas").Body.String()
	palette := strings.Index(body, `<aside class="ui-canvas-panel" id="canvas-palette"`)
	canvas := strings.Index(body, `<div class="ui-canvas-panel">`)
	if palette < 0 || canvas < 0 {
		t.Fatalf("palette (%d) or canvas (%d) panel is missing", palette, canvas)
	}
	if palette > canvas {
		t.Error("the canvas panel renders before the palette panel")
	}
}

// paletteItems splits the palette list into one tag per item.
func paletteItems(body string) []string {
	var items []string
	for _, chunk := range strings.Split(body, `<li class="ui-canvas-palette-item`)[1:] {
		end := strings.Index(chunk, ">")
		if end < 0 {
			continue
		}
		items = append(items, chunk[:end])
	}
	return items
}

// AC-21 and AC-33: every available palette item is draggable and carries
// data-block-kind; an unavailable kind carries neither.
func TestPaletteItemsDraggable(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-slice2-palette")
	insertDefinition(t, ctx, slug, "Slice 2 Palette", "", tdeTwoInputs, true, true)
	body := renderDefinitionView(t, slug, "?view=canvas").Body.String()
	items := paletteItems(body)
	if len(items) == 0 {
		t.Fatal("no palette items rendered")
	}
	available := 0
	for _, item := range items {
		disabled := strings.Contains(item, "is-disabled")
		if disabled {
			if strings.Contains(item, `draggable="true"`) {
				t.Errorf("unavailable palette item is draggable: %s", item)
			}
			continue
		}
		available++
		if !strings.Contains(item, `draggable="true"`) {
			t.Errorf("available palette item is not draggable: %s", item)
		}
		if !strings.Contains(item, "data-block-kind=") {
			t.Errorf("available palette item has no data-block-kind: %s", item)
		}
	}
	if available == 0 {
		t.Error("the palette offered no available kind")
	}
}

// AC-22: a task candidate names its kind and slug; a control item names its
// kind and type.
func TestPaletteItemsCarryPayload(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-slice2-payload")
	insertDefinition(t, ctx, slug, "Slice 2 Payload", "", tdeTwoInputs, true, true)
	candidateSlug, _ := canvasTaskBlock(t, ctx, "canvas-slice2-candidate")
	body := renderDefinitionView(t, slug, "?view=canvas").Body.String()
	if !strings.Contains(body, `data-block-kind="task" data-block-slug="`+candidateSlug+`"`) {
		t.Error("a task candidate does not carry data-block-kind=task and data-block-slug")
	}
	if !strings.Contains(body, `data-block-kind="control" data-block-type="if"`) {
		t.Error("the if palette item does not carry data-block-kind=control data-block-type=if")
	}
}

// AC-23: the tree is the root drop target and says so with an empty
// data-drop-parent.
func TestCanvasRootIsDropTarget(t *testing.T) {
	template := repoFile(t, "../../web/templates/task-definitions-view.html")
	if !strings.Contains(template, `id="canvas-tree" data-drop-parent=""`) {
		t.Error(`the canvas tree does not carry data-drop-parent=""`)
	}
	if strings.Contains(template, `data-drop-root=`) {
		t.Error("the old data-drop-root attribute is still in the markup")
	}
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-slice2-root")
	insertDefinition(t, ctx, slug, "Slice 2 Root", "", tdeTwoInputs, true, true)
	body := renderDefinitionView(t, slug, "?view=canvas").Body.String()
	if !strings.Contains(body, `id="canvas-tree" data-drop-parent=""`) {
		t.Error("the rendered tree does not expose data-drop-parent")
	}
}

// AC-25: a real insertion indicator exists, is 2px high, and dragover moves
// it using the same mode function as drop.
func TestCanvasInsertIndicator(t *testing.T) {
	template := repoFile(t, "../../web/templates/task-definitions-view.html")
	if !strings.Contains(template, `id="canvas-insert"`) {
		t.Error("the canvas has no insertion indicator element")
	}
	rule := cssRule(t, template, ".ui-canvas-insert")
	if !strings.Contains(rule, "height: 2px;") {
		t.Errorf(".ui-canvas-insert is not 2px high:%s", rule)
	}
	if !strings.Contains(template, "function placeInsertIndicator") {
		t.Error("dragover does not position the indicator")
	}
	if strings.Count(template, "function dropModeFor") != 1 {
		t.Error("the drop mode is not computed in one shared function")
	}
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-slice2-insert")
	insertDefinition(t, ctx, slug, "Slice 2 Insert", "", tdeTwoInputs, true, true)
	body := renderDefinitionView(t, slug, "?view=canvas").Body.String()
	if !strings.Contains(body, `class="ui-canvas-insert`) {
		t.Error("the rendered canvas has no ui-canvas-insert element")
	}
	if !strings.Contains(body, "ui-canvas-insert") {
		t.Error("ui-canvas-insert is missing from the rendered page")
	}
}

// AC-32: a data-palette-toggle button collapses the palette below 900px.
func TestPaletteToggleCollapsesBelow900(t *testing.T) {
	template := repoFile(t, "../../web/templates/task-definitions-view.html")
	if !strings.Contains(template, "data-palette-toggle") {
		t.Fatal("no data-palette-toggle button in the palette")
	}
	if !strings.Contains(template, "aria-controls=\"palette-body\"") {
		t.Error("the toggle does not name the list it controls")
	}
	media := template[strings.Index(template, "@media (max-width: 900px)"):]
	if !strings.Contains(media, "#palette-body { display: none; }") {
		t.Error("the 900px rule does not collapse the palette body")
	}
	if !strings.Contains(media, "#palette-body.is-open { display: block; }") {
		t.Error("the 900px rule has no open state to toggle")
	}
	if !strings.Contains(template, `paletteBody.classList.toggle("is-open")`) {
		t.Error("the toggle does not flip the palette body's is-open class")
	}
}

// AC-30: positions 10, 20, 30 survive a save and a reload in that order.
func TestSparsePositionsRoundTrip(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-slice2-sparse")
	containerID := insertDefinition(t, ctx, slug, "Slice 2 Sparse", "", tdeTwoInputs, true, true)
	first, _ := canvasTaskBlock(t, ctx, "canvas-slice2-sparse-a")
	second, _ := canvasTaskBlock(t, ctx, "canvas-slice2-sparse-b")

	rec := postStructure(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: first, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: second, Label: "Second", Position: 20},
		{Key: "c", Kind: "control", Type: "end", Label: "Last", Position: 30},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("sparse save = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	rows := readStoredRows(t, containerID)
	var positions []int
	var labels []string
	for _, row := range rows {
		positions = append(positions, row.Position)
		labels = append(labels, row.Label)
	}
	wantPositions := []int{0, 10, 20, 30}
	if len(positions) != len(wantPositions) {
		t.Fatalf("stored %d rows, want %d: %v", len(positions), len(wantPositions), positions)
	}
	for i := range wantPositions {
		if positions[i] != wantPositions[i] {
			t.Errorf("stored position %d = %d, want %d (all: %v)", i, positions[i], wantPositions[i], positions)
		}
	}

	loaded, err := db.LoadDefinitionDocument(ctx, containerID)
	if err != nil {
		t.Fatalf("reload document: %v", err)
	}
	if len(loaded) != 4 {
		t.Fatalf("reload returned %d blocks, want 4", len(loaded))
	}
	wantLabels := []string{"Start", "First", "Second", "Last"}
	for i, want := range wantLabels {
		if loaded[i].Label != want {
			t.Errorf("reloaded block %d = %q, want %q (order not reproduced)", i, loaded[i].Label, want)
		}
		if loaded[i].Position != wantPositions[i] {
			t.Errorf("reloaded block %d position = %d, want %d", i, loaded[i].Position, wantPositions[i])
		}
	}
}

// AC-34: a document carrying an unknown block kind is refused by the server
// with a named error, and the stored tree is untouched.
func TestUnknownKindDropRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "canvas-slice2-unknown")
	containerID := insertDefinition(t, ctx, slug, "Slice 2 Unknown", "", tdeTwoInputs, true, true)
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
	})
	before := storedBlockCount(t, ctx, containerID)

	rec := postStructure(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "x", Kind: "nonsense", Label: "Dropped mystery", Position: 10},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Dropped mystery") || !strings.Contains(body, "that block kind is not available in this release") {
		t.Errorf("refusal does not name the block and the reason: %s", body)
	}
	if after := storedBlockCount(t, ctx, containerID); after != before {
		t.Errorf("refused drop changed the stored tree: %d rows, want %d", after, before)
	}
}
