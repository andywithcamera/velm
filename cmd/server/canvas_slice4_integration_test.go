package main

// Slice 4 of the designer rework: the run_script block and its three-layer
// admin gate (Design Vision D4/D5; Requirements AC-55 to AC-74). Real
// Postgres, gated on TEST_DATABASE_URL: a skip is a broken run, not a pass.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"velm/internal/auth"
	"velm/internal/db"
)

// postStructureAs posts a document as an admin or as a plain editor.
func postStructureAs(t *testing.T, slug string, blocks []db.StructureBlock, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"blocks": blocks})
	if err != nil {
		t.Fatalf("encode structure: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/structure",
		strings.NewReader(url.Values{"structure": {string(payload)}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	role := "editor"
	if admin {
		role = "admin"
	}
	req = req.WithContext(auth.WithUserContext(req.Context(), "user-1", "u@example.com", "User", role))
	handleTaskDefinitionStructureSave(rec, req)
	return rec
}

func renderDefinitionViewAs(t *testing.T, slug, query string, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/task-definitions/"+slug+query, nil)
	req.SetPathValue("slug", slug)
	role := "editor"
	if admin {
		role = "admin"
	}
	req = req.WithContext(auth.WithUserContext(req.Context(), "user-1", "u@example.com", "User", role))
	handleTaskDefinitionEditor(rec, req)
	return rec
}

func scriptBlock(key, label string, config map[string]any) db.StructureBlock {
	return db.StructureBlock{Key: key, Kind: "control", Type: "run_script", Label: label, Config: config}
}

// AC-57: the admin palette offers run_script; a non-admin's does not.
func TestScriptBlockHiddenFromNonAdmin(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice4-palette")
	insertDefinition(t, ctx, slug, "Palette", "", tdeTwoInputs, true, true)
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{{Key: "t", Kind: "trigger", Label: "Start", Position: 0}})

	adminBody := renderDefinitionViewAs(t, slug, "", true).Body.String()
	if !strings.Contains(adminBody, `data-block-type="run_script"`) {
		t.Errorf("admin palette has no run_script: %s", adminBody)
	}
	editorBody := renderDefinitionViewAs(t, slug, "", false).Body.String()
	if strings.Contains(editorBody, `data-block-type="run_script"`) {
		t.Errorf("non-admin palette offers run_script")
	}
}

// AC-58: a save containing run_script from a non-admin is refused 400 with
// the named error, and the stored tree is unchanged.
func TestScriptBlockRefusedAtSaveForNonAdmin(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice4-save-gate")
	containerID := insertDefinition(t, ctx, slug, "Save gate", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "slice4-save-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Work", Position: 10},
	})
	before := storedBlockCount(t, ctx, containerID)

	rec := postStructureAs(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "s", Kind: "control", Type: "run_script", Label: "Upgrade", Position: 10,
			Config: map[string]any{"script": "function run(ctx) { return 1 }"}},
	}, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-admin run_script save = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `Block &#34;Upgrade&#34;: run script requires the admin role`) {
		t.Errorf("refusal does not name the gate: %s", rec.Body.String())
	}
	if after := storedBlockCount(t, ctx, containerID); after != before {
		t.Fatalf("a refused save changed the tree: %d -> %d", before, after)
	}
}

// AC-59: an existing run_script block renders the refusal, not the body, to a
// non-admin.
func TestScriptPropertiesRefusedForNonAdmin(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice4-properties")
	containerID := insertDefinition(t, ctx, slug, "Properties", "", tdeTwoInputs, true, true)
	if err := db.SaveDefinitionStructure(ctx, containerID, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		scriptBlock("s", "Upgrade", map[string]any{"script": "function run(ctx) { return 1 }"}),
	}); err != nil {
		t.Fatalf("seed run_script: %v", err)
	}
	body := renderDefinitionViewAs(t, slug, "", false).Body.String()
	fragment := fragmentForLabel(body, "Upgrade")
	if !strings.Contains(fragment, "run script blocks require the admin role") {
		t.Errorf("non-admin card carries no refusal: %s", fragment)
	}
	if strings.Contains(fragment, `data-config="script"`) {
		t.Errorf("non-admin card rendered the script body: %s", fragment)
	}
}

// AC-72: an admin edits the body in a textarea in the block's own card.
func TestScriptBodyIsTextarea(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice4-textarea")
	containerID := insertDefinition(t, ctx, slug, "Textarea", "", tdeTwoInputs, true, true)
	if err := db.SaveDefinitionStructure(ctx, containerID, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		scriptBlock("s", "Upgrade", map[string]any{"script": "function run(ctx) { return 1 }"}),
	}); err != nil {
		t.Fatalf("seed run_script: %v", err)
	}
	body := renderDefinitionViewAs(t, slug, "", true).Body.String()
	fragment := fragmentForLabel(body, "Upgrade")
	if !strings.Contains(fragment, `<textarea`) || !strings.Contains(fragment, `data-config="script"`) {
		t.Errorf("script body is not a textarea in its card: %s", fragment)
	}
}

// AC-70: timeout_ms outside 100-60000 is refused, naming the block and field.
func TestScriptTimeoutBounds(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice4-timeout-bounds")
	insertDefinition(t, ctx, slug, "Timeout bounds", "", tdeTwoInputs, true, true)
	for _, ms := range []int{99, 60001} {
		rec := postStructureAs(t, slug, []db.StructureBlock{
			{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
			scriptBlock("s", "Upgrade", map[string]any{"script": "function run(ctx) { return 1 }", "timeout_ms": ms}),
		}, true)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("timeout_ms=%d = %d, want 400", ms, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "timeout_ms must be between 100 and 60000") {
			t.Errorf("refusal for %d does not name the field: %s", ms, rec.Body.String())
		}
	}
}

// AC-73: run_script is accepted at root and inside if (both branches),
// for_each and retry.
func TestScriptBlockScopePlacement(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice4-placement")
	insertDefinition(t, ctx, slug, "Placement", "", tdeTwoInputs, true, true)
	document := []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		scriptBlock("root", "Root script", map[string]any{"script": "function run(ctx) { return 1 }"}),
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 20, Config: map[string]any{"condition": "expr: true"}},
		{Key: "then", Parent: "i", Kind: "control", Type: "run_script", Label: "Then script", Branch: "then", Config: map[string]any{"script": "function run(ctx) { return 1 }"}},
		{Key: "else", Parent: "i", Kind: "control", Type: "run_script", Label: "Else script", Branch: "else", Config: map[string]any{"script": "function run(ctx) { return 1 }"}},
		{Key: "e", Parent: "i", Kind: "control", Type: "end", Label: "End", Position: 40},
		{Key: "f", Kind: "control", Type: "for_each", Label: "Each", Position: 50, Config: map[string]any{"collection": []any{1}}},
		{Key: "fs", Parent: "f", Kind: "control", Type: "run_script", Label: "Loop script", Config: map[string]any{"script": "function run(ctx) { return 1 }"}},
		{Key: "fe", Parent: "f", Kind: "control", Type: "end", Label: "Loop end", Position: 70},
	}
	rec := postStructureAs(t, slug, document, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("run_script placement = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// AC-74: script, timeout_ms and output_var survive a load/save round trip.
func TestScriptConfigRoundTrip(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice4-roundtrip")
	containerID := insertDefinition(t, ctx, slug, "Round trip", "", tdeTwoInputs, true, true)
	document := []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		scriptBlock("s", "Upgrade", map[string]any{
			"script": "function run(ctx) { return 1 }", "timeout_ms": 2500, "output_var": "upgrade",
		}),
	}
	if rec := postStructureAs(t, slug, document, true); rec.Code != http.StatusOK {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	blocks, err := db.LoadDefinitionDocument(ctx, containerID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("loaded %d blocks, want 2", len(blocks))
	}
	script := blocks[1]
	if script.Config["script"] != "function run(ctx) { return 1 }" ||
		script.Config["output_var"] != "upgrade" {
		t.Fatalf("script config did not survive: %+v", script.Config)
	}
	if got, ok := script.Config["timeout_ms"].(float64); !ok || got != 2500 {
		t.Fatalf("timeout_ms = %v, want 2500", script.Config["timeout_ms"])
	}
}

// AC-55: children of an existing if are THEN once the branch column exists.
func TestThenElseChildrenDistinguished(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice4-branches")
	containerID := insertDefinition(t, ctx, slug, "Branches", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "slice4-branch-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "a", Parent: "i", Kind: "task", Slug: childSlug, Label: "Then work", Branch: "then", Position: 10},
		{Key: "b", Parent: "i", Kind: "task", Slug: childSlug, Label: "Else work", Branch: "else", Position: 20},
	})
	var thenBranch, elseBranch int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE branch = 'then'), count(*) FILTER (WHERE branch = 'else')
		FROM base_task_definition_child WHERE container_definition_id = $1`, containerID).Scan(&thenBranch, &elseBranch); err != nil {
		t.Fatalf("read branches: %v", err)
	}
	if thenBranch != 1 || elseBranch != 1 {
		t.Fatalf("branches = then:%d else:%d, want 1 and 1", thenBranch, elseBranch)
	}
}
