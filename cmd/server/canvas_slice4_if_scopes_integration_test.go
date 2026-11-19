package main

// AC-24/D14 authoring half (rework 2026-11-19): an `if` runs two branches in
// the engine, so the canvas must show and accept a drop into each. Real
// Postgres, gated on TEST_DATABASE_URL: a skip is a broken run, not a pass.

import (
	"strings"
	"testing"

	"velm/internal/db"
)

// TestIfHasThenAndElseScopes: the `if` block renders one drop container per
// branch, each naming the if as its parent, and no other block does.
func TestIfHasThenAndElseScopes(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice4-if-scopes")
	containerID := insertDefinition(t, ctx, slug, "If scopes", "", tdeTwoInputs, true, true)
	childSlug, _ := canvasTaskBlock(t, ctx, "slice4-if-scopes-leaf")
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "Gate", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "a", Parent: "i", Kind: "task", Slug: childSlug, Label: "Then work", Branch: "then", Position: 10},
		{Key: "b", Parent: "i", Kind: "task", Slug: childSlug, Label: "Else work", Branch: "else", Position: 20},
	})

	var ifID string
	if err := db.Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task_definition_child
		WHERE container_definition_id = $1 AND block_type = 'if'`, containerID).Scan(&ifID); err != nil {
		t.Fatalf("read if block: %v", err)
	}

	body := renderDefinitionViewAs(t, slug, "", false).Body.String()

	if count := strings.Count(body, "data-drop-branch="); count != 2 {
		t.Fatalf("data-drop-branch containers = %d, want 2", count)
	}
	for _, branch := range []string{"then", "else"} {
		want := `data-drop-parent="` + ifID + `" data-drop-branch="` + branch + `"`
		if !strings.Contains(body, want) {
			t.Fatalf("no %s scope container on the if block (%s)", branch, want)
		}
	}
}
