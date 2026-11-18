package main

// Slice 3 of the designer rework: reordering, reparenting, the keyboard moves
// and inline configuration (Design Vision D3, D4, D13; Requirements AC-36 to
// AC-54). Real Postgres, gated on TEST_DATABASE_URL: a skip is a broken run,
// not a pass.

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

// postStructureDocument posts one document the canvas sends, with the extra
// fields a keyboard move adds. The document keeps document-local keys, as the
// browser's does, so a move can name the block it means.
func postStructureDocument(t *testing.T, slug string, blocks []db.StructureBlock, extra url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"blocks": blocks})
	if err != nil {
		t.Fatalf("encode structure: %v", err)
	}
	values := url.Values{"structure": {string(payload)}}
	for key, entries := range extra {
		values[key] = entries
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/structure",
		strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req.SetPathValue("slug", slug)
	handleTaskDefinitionStructureSave(rec, req)
	return rec
}

// storedBlockRows is the shape a slice-3 assertion reads: label, position and
// whether the block sits at root.
type storedBlockRow struct {
	Label    string
	Position int
	ParentID string
}

func readStoredRows(t *testing.T, containerID string) []storedBlockRow {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(),
		`SELECT COALESCE(config->>'label', ''), position, COALESCE(parent_block_id::text, '')
		 FROM base_task_definition_child
		 WHERE container_definition_id = $1
		 ORDER BY position, _created_at`, containerID)
	if err != nil {
		t.Fatalf("read stored tree: %v", err)
	}
	defer rows.Close()
	var result []storedBlockRow
	for rows.Next() {
		var row storedBlockRow
		if err := rows.Scan(&row.Label, &row.Position, &row.ParentID); err != nil {
			t.Fatalf("scan stored tree: %v", err)
		}
		result = append(result, row)
	}
	return result
}

func storedRowByLabel(t *testing.T, containerID, label string) storedBlockRow {
	t.Helper()
	for _, row := range readStoredRows(t, containerID) {
		if row.Label == label {
			return row
		}
	}
	t.Fatalf("no stored block labelled %q", label)
	return storedBlockRow{}
}

func storedIDByLabel(t *testing.T, containerID, label string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT _id::text FROM base_task_definition_child
		 WHERE container_definition_id = $1 AND config->>'label' = $2
		 ORDER BY position, _created_at LIMIT 1`, containerID, label).Scan(&id); err != nil {
		t.Fatalf("read block id for %q: %v", label, err)
	}
	return id
}

// blockFragment returns the markup of one block's <li>, so an assertion about
// configuration can be scoped to the block that owns it.
func blockFragment(body, blockID string) string {
	at := strings.Index(body, `id="block-`+blockID+`"`)
	if at < 0 {
		return ""
	}
	// Start at the <li> rather than at the id so the class attribute, which
	// carries is-selected and is-expanded, is part of the fragment.
	start := strings.LastIndex(body[:at], "<li ")
	if start < 0 {
		return ""
	}
	end := strings.Index(body[at:], "</li>")
	if end < 0 {
		return body[start:]
	}
	return body[start : start+end]
}

// AC-36: every non-trigger block is draggable and carries its document key;
// the Start node is neither.
func TestExistingBlocksDraggable(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-draggable")
	insertDefinition(t, ctx, slug, "Draggable Blocks", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "slice3-draggable-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Drag me", Position: 10},
	})

	body := renderDefinitionView(t, slug, "").Body.String()
	start := blockFragment(body, "trigger-marker")
	_ = start
	// Find the task block's li by its label instead of an id we do not know.
	li := fragmentForLabel(body, "Drag me")
	if li == "" {
		t.Fatalf("task block not rendered: %s", body)
	}
	if !strings.Contains(li, `draggable="true"`) || !strings.Contains(li, `data-block-key=`) {
		t.Errorf("task block is not draggable with a key: %s", li)
	}
	trigger := fragmentForLabel(body, "Start")
	if strings.Contains(trigger, `draggable="true"`) {
		t.Errorf("Start is draggable: %s", trigger)
	}
	if !strings.Contains(trigger, `data-block-key=`) {
		t.Errorf("Start has no document key: %s", trigger)
	}
}

// fragmentForLabel returns the <li> whose label link text is label.
func fragmentForLabel(body, label string) string {
	marker := ">" + label + "</a>"
	at := strings.Index(body, marker)
	if at < 0 {
		return ""
	}
	start := strings.LastIndex(body[:at], "<li ")
	if start < 0 {
		return ""
	}
	end := strings.Index(body[at:], "</li>")
	if end < 0 {
		return body[start:]
	}
	return body[start : at+end]
}

// AC-38: a same-scope reorder persists by position, and the document order on
// reload matches the drop.
func TestReorderPersists(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-reorder")
	containerID := insertDefinition(t, ctx, slug, "Reorder", "", tdeTwoInputs, true, true)
	firstSlug, _ := canvasTaskBlock(t, ctx, "slice3-reorder-first")
	secondSlug, _ := canvasTaskBlock(t, ctx, "slice3-reorder-second")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: firstSlug, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: secondSlug, Label: "Second", Position: 20},
	})

	rec := postStructureDocument(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: firstSlug, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: secondSlug, Label: "Second", Position: 20},
	}, url.Values{"move": {"up"}, "move_key": {"b"}}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rows := readStoredRows(t, containerID)
	if len(rows) != 3 || rows[1].Label != "Second" || rows[2].Label != "First" {
		t.Fatalf("stored order after reorder = %+v, want Second before First", rows)
	}
	if rows[1].Position != 10 || rows[2].Position != 20 {
		t.Errorf("positions after reorder = %d,%d, want 10,20", rows[1].Position, rows[2].Position)
	}
}

// AC-39: indenting a block into the preceding control block makes it that
// block's child at the last position in the scope.
func TestReparentPersists(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-reparent")
	containerID := insertDefinition(t, ctx, slug, "Reparent", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "slice3-reparent-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "e", Parent: "i", Kind: "control", Type: "end", Label: "End", Position: 10},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Inside", Position: 20},
	})

	rec := postStructureDocument(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "e", Parent: "i", Kind: "control", Type: "end", Label: "End", Position: 10},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Inside", Position: 20},
	}, url.Values{"move": {"indent"}, "move_key": {"a"}}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("reparent = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	gateID := storedIDByLabel(t, containerID, "Gate")
	inside := storedRowByLabel(t, containerID, "Inside")
	if inside.ParentID != gateID {
		t.Errorf("Inside parent = %q, want the Gate block %q", inside.ParentID, gateID)
	}
	// The Gate scope already holds End at 10, so the last position is 20.
	if inside.Position != 20 {
		t.Errorf("Inside position = %d, want the last position in its new scope (20)", inside.Position)
	}
}

// AC-40: outdenting a scoped block to root clears its parent.
func TestOutdentToRoot(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-outdent")
	containerID := insertDefinition(t, ctx, slug, "Outdent", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "slice3-outdent-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "e", Parent: "i", Kind: "control", Type: "end", Label: "End", Position: 10},
		{Key: "a", Parent: "i", Kind: "task", Slug: childSlug, Label: "Inside", Position: 20},
	})

	rec := postStructureDocument(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "e", Parent: "i", Kind: "control", Type: "end", Label: "End", Position: 10},
		{Key: "a", Parent: "i", Kind: "task", Slug: childSlug, Label: "Inside", Position: 20},
	}, url.Values{"move": {"outdent"}, "move_key": {"a"}}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("outdent = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if inside := storedRowByLabel(t, containerID, "Inside"); inside.ParentID != "" {
		t.Errorf("Inside parent = %q, want root", inside.ParentID)
	}
}

// AC-43: every expanded non-trigger block offers the four keyboard moves with
// accessible names.
func TestKeyboardMoveButtonsPresent(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-buttons")
	insertDefinition(t, ctx, slug, "Buttons", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "slice3-buttons-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Expanded", Position: 10},
	})
	body := renderDefinitionView(t, slug, "").Body.String()
	li := fragmentForLabel(body, "Expanded")
	for _, name := range []string{"Move up", "Move down", "Indent", "Outdent"} {
		if !strings.Contains(li, `aria-label="`+name+`"`) {
			t.Errorf("expanded block offers no %q button: %s", name, li)
		}
	}
}

// AC-44: the first block in a scope cannot move up, the last cannot move down,
// and a root block cannot outdent.
func TestMoveButtonsDisabledAtBounds(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-bounds")
	containerID := insertDefinition(t, ctx, slug, "Bounds", "", tdeTwoInputs, true, true)
	firstSlug, _ := canvasTaskBlock(t, ctx, "slice3-bounds-first")
	lastSlug, _ := canvasTaskBlock(t, ctx, "slice3-bounds-last")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: firstSlug, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: lastSlug, Label: "Last", Position: 20},
	})
	firstID := storedIDByLabel(t, containerID, "First")
	lastID := storedIDByLabel(t, containerID, "Last")

	first := blockFragment(renderDefinitionView(t, slug, "?block="+firstID).Body.String(), firstID)
	if !disabledButton(first, "Move up") {
		t.Errorf("first block's Move up is not disabled: %s", first)
	}
	if !disabledButton(first, "Outdent") {
		t.Errorf("root block's Outdent is not disabled: %s", first)
	}
	last := blockFragment(renderDefinitionView(t, slug, "?block="+lastID).Body.String(), lastID)
	if !disabledButton(last, "Move down") {
		t.Errorf("last block's Move down is not disabled: %s", last)
	}
}

func disabledButton(fragment, accessibleName string) bool {
	at := strings.Index(fragment, `aria-label="`+accessibleName+`"`)
	if at < 0 {
		return false
	}
	start := strings.LastIndex(fragment[:at], "<button")
	if start < 0 {
		return false
	}
	// The disabled attribute is rendered after aria-label (the template puts
	// it at the end of the opening tag), so the check must run to the tag's
	// closing > rather than stopping at the label.
	close := strings.Index(fragment[at:], ">")
	if close < 0 {
		return false
	}
	return strings.Contains(fragment[start:at+close], "disabled")
}

// AC-45: Indent is offered only where the immediately preceding sibling is a
// control block.
func TestIndentRequiresControlPredecessor(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-indent")
	containerID := insertDefinition(t, ctx, slug, "Indent", "", tdeTwoInputs, true, true)
	afterControlSlug, _ := canvasTaskBlock(t, ctx, "slice3-indent-after-control")
	afterTaskSlug, _ := canvasTaskBlock(t, ctx, "slice3-indent-after-task")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "e", Parent: "i", Kind: "control", Type: "end", Label: "End", Position: 10},
		{Key: "a", Kind: "task", Slug: afterControlSlug, Label: "After Control", Position: 20},
		{Key: "b", Kind: "task", Slug: afterTaskSlug, Label: "After Task", Position: 30},
	})
	afterControlID := storedIDByLabel(t, containerID, "After Control")
	afterTaskID := storedIDByLabel(t, containerID, "After Task")

	afterControl := blockFragment(renderDefinitionView(t, slug, "?block="+afterControlID).Body.String(), afterControlID)
	if disabledButton(afterControl, "Indent") {
		t.Errorf("Indent is disabled after a control block: %s", afterControl)
	}
	afterTask := blockFragment(renderDefinitionView(t, slug, "?block="+afterTaskID).Body.String(), afterTaskID)
	if !disabledButton(afterTask, "Indent") {
		t.Errorf("Indent is enabled after a task block: %s", afterTask)
	}
}

// AC-46: one button post performs exactly one move and the response comes back
// with that block selected.
func TestEveryButtonUsesTheDocumentPost(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-button-post")
	containerID := insertDefinition(t, ctx, slug, "Button Post", "", tdeTwoInputs, true, true)
	firstSlug, _ := canvasTaskBlock(t, ctx, "slice3-button-post-first")
	secondSlug, _ := canvasTaskBlock(t, ctx, "slice3-button-post-second")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: firstSlug, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: secondSlug, Label: "Second", Position: 20},
	})

	rec := postStructureDocument(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: firstSlug, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: secondSlug, Label: "Second", Position: 20},
	}, url.Values{"move": {"down"}, "move_key": {"a"}, "selected": {"a"}}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("button post = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rows := readStoredRows(t, containerID)
	if len(rows) != 3 || rows[1].Label != "Second" || rows[2].Label != "First" {
		t.Fatalf("stored order after one move = %+v, want Second before First", rows)
	}
	body := rec.Body.String()
	movedID := storedIDByLabel(t, containerID, "First")
	if !strings.Contains(body, `id="block-`+movedID+`"`) {
		t.Fatalf("response does not render the moved block: %s", body)
	}
	if li := blockFragment(body, movedID); !strings.Contains(li, "is-selected") {
		t.Errorf("the moved block is not the selected block after the post: %s", li)
	}
}

// AC-47: a child naming a parent that appears later is refused, naming the
// child, and nothing is written.
func TestParentAfterChildRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-parent-later")
	containerID := insertDefinition(t, ctx, slug, "Parent Later", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "slice3-parent-later-leaf")
	before := storedBlockCount(t, ctx, containerID)

	rec := postStructureDocument(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Parent: "i", Kind: "task", Slug: childSlug, Label: "Early Child", Position: 10},
		{Key: "i", Kind: "control", Type: "if", Label: "Late Parent", Position: 20, Config: map[string]any{"condition": "expr: true"}},
	}, nil, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("parent-after-child = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "its parent must be a block earlier in the document") {
		t.Errorf("refusal does not explain the order rule: %s", rec.Body.String())
	}
	if after := storedBlockCount(t, ctx, containerID); after != before {
		t.Errorf("refused save wrote rows: %d -> %d", before, after)
	}
}

// AC-48: a block naming itself as parent is refused.
func TestSelfParentRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-self-parent")
	containerID := insertDefinition(t, ctx, slug, "Self Parent", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "slice3-self-parent-leaf")
	before := storedBlockCount(t, ctx, containerID)

	rec := postStructureDocument(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Parent: "a", Kind: "task", Slug: childSlug, Label: "Itself", Position: 10},
	}, nil, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self parent = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "a block cannot be its own parent") {
		t.Errorf("refusal does not name the self-parent rule: %s", rec.Body.String())
	}
	if after := storedBlockCount(t, ctx, containerID); after != before {
		t.Errorf("refused save wrote rows: %d -> %d", before, after)
	}
}

// AC-49: seven levels save and reload with every parent link intact, and the
// indent ladder caps at depth 4 rather than dropping the class.
func TestDeepNestingSavesAndRenders(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-deep")
	containerID := insertDefinition(t, ctx, slug, "Deep", "", tdeTwoInputs, true, true)

	blocks := []db.StructureBlock{{Key: "t", Kind: "trigger", Label: "Start", Position: 0}}
	parent := ""
	for level := 1; level <= 7; level++ {
		key := "n" + itoa(level)
		blocks = append(blocks, db.StructureBlock{
			Key: key, Parent: parent, Kind: "control", Type: "if",
			Label: "Level " + itoa(level), Position: level * 10,
			Config: map[string]any{"condition": "expr: true"},
		})
		parent = key
	}
	// The deepest if needs a body to be a sound scope.
	blocks = append(blocks, db.StructureBlock{
		Key: "e", Parent: parent, Kind: "control", Type: "end", Label: "Deep End", Position: 80,
	})
	rec := postStructureDocument(t, slug, blocks, nil, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("seven-level save = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rows := readStoredRows(t, containerID)
	if len(rows) != 9 {
		t.Fatalf("stored rows = %d, want 9 (Start, seven levels and the closing end)", len(rows))
	}
	// Walk the parent links back from the deepest stored block. The trigger is
	// not a container, so Level 1 is a root block and the seven ifs hang under
	// each other; the deepest row is the end block inside Level 7.
	parentID := storedIDByLabel(t, containerID, "Deep End")
	seen := 0
	for parentID != "" {
		seen++
		if seen > 9 {
			t.Fatalf("parent chain does not terminate")
		}
		var next string
		if err := db.Pool.QueryRow(context.Background(),
			`SELECT COALESCE(parent_block_id::text, '') FROM base_task_definition_child WHERE _id = $1`,
			parentID).Scan(&next); err != nil {
			t.Fatalf("read parent chain: %v", err)
		}
		parentID = next
	}
	if seen != 8 {
		t.Errorf("parent chain length = %d, want 8 (Start plus seven levels)", seen)
	}
	body := renderDefinitionView(t, slug, "").Body.String()
	if !strings.Contains(body, "ui-canvas-depth-4") {
		t.Errorf("deep nesting does not render the capped depth-4 class")
	}
	if strings.Contains(body, "ui-canvas-depth-5") {
		t.Errorf("canvas invented a depth class past the ladder's cap")
	}
}

// AC-50: a refused move leaves the stored tree exactly as it was.
func TestRefusedMovePreservesTree(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-refused-move")
	containerID := insertDefinition(t, ctx, slug, "Refused Move", "", tdeTwoInputs, true, true)
	firstSlug, _ := canvasTaskBlock(t, ctx, "slice3-refused-first")
	secondSlug, _ := canvasTaskBlock(t, ctx, "slice3-refused-second")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: firstSlug, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: secondSlug, Label: "Second", Position: 20},
	})
	before := readStoredRows(t, containerID)

	rec := postStructureDocument(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: firstSlug, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: secondSlug, Label: "Second", Position: 20},
	}, url.Values{"move": {"indent"}, "move_key": {"b"}}, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("indent after a task = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	after := readStoredRows(t, containerID)
	if len(after) != len(before) {
		t.Fatalf("refused move changed row count: %d -> %d", len(before), len(after))
	}
	for index := range before {
		if before[index] != after[index] {
			t.Fatalf("refused move changed the tree: %+v -> %+v", before, after)
		}
	}
}

// AC-51: the configuration side rail is gone.
func TestNoConfigSideRail(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-rail")
	insertDefinition(t, ctx, slug, "No Rail", "", tdeTwoInputs, true, true)
	body := renderDefinitionView(t, slug, "").Body.String()
	if strings.Contains(body, `id="canvas-rail"`) {
		t.Errorf("the page still renders the side rail")
	}
	if strings.Contains(body, "data-rail") {
		t.Errorf("the page still carries a data-rail attribute")
	}
}

// AC-52: a block's config fields render inside that block's own card.
func TestConfigRendersInsideBlock(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-config-inside")
	containerID := insertDefinition(t, ctx, slug, "Config Inside", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "slice3-config-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "a", Parent: "i", Kind: "task", Slug: childSlug, Label: "Inside", Position: 10},
	})
	gateID := storedIDByLabel(t, containerID, "Gate")
	li := blockFragment(renderDefinitionView(t, slug, "?block="+gateID).Body.String(), gateID)
	if !strings.Contains(li, `data-config="condition"`) {
		t.Fatalf("the expanded block does not contain its condition field: %s", li)
	}
	if !strings.Contains(li, `textarea`) {
		t.Errorf("the condition is not a textarea inside the card: %s", li)
	}
}

// AC-53: exactly one block is expanded, and the others carry no config field.
func TestOnlyOneBlockExpanded(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-one-expanded")
	containerID := insertDefinition(t, ctx, slug, "One Expanded", "", tdeTwoInputs, true, true)
	firstSlug, _ := canvasTaskBlock(t, ctx, "slice3-one-expanded-first")
	secondSlug, _ := canvasTaskBlock(t, ctx, "slice3-one-expanded-second")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: firstSlug, Label: "First", Position: 10},
		{Key: "b", Kind: "task", Slug: secondSlug, Label: "Second", Position: 20},
	})
	body := renderDefinitionView(t, slug, "").Body.String()
	tree := treeFragment(body)
	if count := strings.Count(tree, "is-expanded"); count != 1 {
		t.Errorf("canvas has %d expanded blocks, want exactly 1", count)
	}
	collapsed := blockFragment(body, storedIDByLabel(t, containerID, "Second"))
	if strings.Contains(collapsed, "textarea") || strings.Contains(collapsed, "data-config") {
		t.Errorf("a collapsed block still contains config fields: %s", collapsed)
	}
}

func treeFragment(body string) string {
	start := strings.Index(body, `id="canvas-tree"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(body[start:], "</ol>")
	if end < 0 {
		return body[start:]
	}
	return body[start : start+end]
}

// AC-54: mapping rows render only while the block that owns them is expanded.
func TestMappingRowsOnlyWhenExpanded(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice3-mapping-expanded")
	containerID := insertDefinition(t, ctx, slug, "Mapping Expanded", "", tdeTwoInputs, true, true)
	mappedSlug, _ := canvasTaskBlock(t, ctx, "slice3-mapping-mapped")
	otherSlug, _ := canvasTaskBlock(t, ctx, "slice3-mapping-other")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: mappedSlug, Label: "Mapped", Position: 10},
		{Key: "b", Kind: "task", Slug: otherSlug, Label: "Collapsed", Position: 20},
	})
	mappedID := storedIDByLabel(t, containerID, "Mapped")
	otherID := storedIDByLabel(t, containerID, "Collapsed")
	body := renderDefinitionView(t, slug, "?block="+mappedID).Body.String()
	if !strings.Contains(blockFragment(body, mappedID), "data-config-mapping") {
		t.Errorf("the expanded block does not render its mapping rows")
	}
	if strings.Contains(blockFragment(body, otherID), "data-config-mapping") {
		t.Errorf("a collapsed block renders mapping rows")
	}
}
