package main

// The structural save path (Process Designer, Plan T4 / Requirements §2).
//
// These tests post the document the canvas and the raw view send, and check
// both the refusal a person sees and the tree that survives it. Real Postgres,
// gated on TEST_DATABASE_URL.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"velm/internal/db"
)

func postStructure(t *testing.T, slug string, blocks []db.StructureBlock) *httptest.ResponseRecorder {
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
	handleTaskDefinitionStructureSave(rec, req)
	return rec
}

// AC-26: a condition without the expr: prefix is refused at save, with the
// block named, and nothing is evaluated as a string.
func TestStructureSaveRefusesBareCondition(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "structure-bare-condition")
	containerID := insertDefinition(t, ctx, slug, "Upgrade OS", "", "", true, true)
	seedStep(t, ctx, containerID, insertDefinition(t, ctx, tdeSlug(t, "structure-leaf-a"), "Reboot", "", "", false, true), 10)

	rec := postStructure(t, slug, []db.StructureBlock{
		{Key: "trigger", Kind: "trigger", Label: "Ordered", Position: 10},
		{Key: "if", Kind: "control", Type: "if", Label: "exit_code is zero", Position: 20,
			Config: map[string]any{"condition": "exit_code == 0"}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bare condition save = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "expr:") {
		t.Fatalf("refusal does not mention the expr: prefix: %s", rec.Body.String())
	}

	var kind string
	if err := db.Pool.QueryRow(ctx, `
		SELECT block_kind FROM base_task_definition_child
		WHERE container_definition_id = $1 ORDER BY position LIMIT 1`, containerID).Scan(&kind); err != nil {
		t.Fatalf("read stored tree: %v", err)
	}
	if kind != "task" {
		t.Fatalf("refused save replaced the tree: first block kind = %q, want the seeded task", kind)
	}
}

// AC-45: wait is refused by name, so no definition can carry a block the
// engine cannot evaluate.
func TestStructureSaveRefusesWait(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "structure-wait")
	insertDefinition(t, ctx, slug, "Upgrade OS", "", "", true, true)

	rec := postStructure(t, slug, []db.StructureBlock{
		{Key: "trigger", Kind: "trigger", Label: "Ordered", Position: 10},
		{Key: "wait", Kind: "control", Type: "wait", Label: "Wait for reboot", Position: 20},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("wait save = %d, want 400", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "not available in this release") || !strings.Contains(body, "Wait for reboot") {
		t.Fatalf("wait refusal %q does not name the block and say why", body)
	}
}

// The document the canvas sends is the document the runtime reads: an if with
// a scoped task and an end round-trips into the block tree.
func TestStructureSaveWritesControlTree(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "structure-tree")
	containerID := insertDefinition(t, ctx, slug, "Upgrade OS", "", "", true, true)
	leafSlug := tdeSlug(t, "structure-leaf-b")
	insertDefinition(t, ctx, leafSlug, "Reboot", "", "", false, true)

	rec := postStructure(t, slug, []db.StructureBlock{
		{Key: "trigger", Kind: "trigger", Label: "Ordered", Position: 10},
		{Key: "if", Kind: "control", Type: "if", Label: "exit_code is zero", Position: 20,
			Config: map[string]any{"condition": `expr: inputs.exit_code == 0`}},
		{Key: "reboot", Parent: "if", Kind: "task", Slug: leafSlug, Label: "Reboot", Position: 30},
		{Key: "end", Kind: "control", Type: "end", Label: "", Position: 40},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("structure save = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	rows, err := db.Pool.Query(ctx, `
		SELECT COALESCE(block_type, ''), block_kind, COALESCE(parent_block_id::text, '')
		FROM base_task_definition_child
		WHERE container_definition_id = $1 ORDER BY position`, containerID)
	if err != nil {
		t.Fatalf("read stored tree: %v", err)
	}
	defer rows.Close()
	var blocks [][3]string
	for rows.Next() {
		var blockType, kind, parent string
		if err := rows.Scan(&blockType, &kind, &parent); err != nil {
			t.Fatalf("scan stored block: %v", err)
		}
		blocks = append(blocks, [3]string{kind, blockType, parent})
	}
	if len(blocks) != 4 {
		t.Fatalf("stored %d blocks, want 4: %v", len(blocks), blocks)
	}
	if blocks[0][0] != "trigger" || blocks[1][0] != "control" || blocks[1][1] != "if" || blocks[3][1] != "end" {
		t.Fatalf("stored tree = %v, want trigger, if, scoped task, end", blocks)
	}
	if blocks[2][0] != "task" || blocks[2][2] == "" {
		t.Fatalf("scoped task has no parent; the canvas document lost its nesting: %v", blocks)
	}
}
