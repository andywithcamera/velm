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
		{Key: "f", Kind: "control", Type: "for_each", Label: "Each host", Position: 30, Config: map[string]any{"collection": []any{"a", "b"}}},
		{Key: "c", Parent: "f", Kind: "task", Slug: childSlug, Label: "Loop work", Position: 10},
	})

	var ifID string
	if err := db.Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task_definition_child
		WHERE container_definition_id = $1 AND block_type = 'if'`, containerID).Scan(&ifID); err != nil {
		t.Fatalf("read if block: %v", err)
	}
	var forEachID string
	if err := db.Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task_definition_child
		WHERE container_definition_id = $1 AND block_type = 'for_each'`, containerID).Scan(&forEachID); err != nil {
		t.Fatalf("read for_each block: %v", err)
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

	// AC-24 second clause: a for_each renders exactly one scope container,
	// and that container carries no data-drop-branch.
	loop := `data-drop-parent="` + forEachID + `"`
	if count := strings.Count(body, loop); count != 1 {
		t.Fatalf("for_each scope containers = %d, want exactly 1", count)
	}
	start := strings.Index(body, loop)
	open := strings.LastIndex(body[:start], "<div")
	end := strings.Index(body[open:], ">")
	if open < 0 || end < 0 {
		t.Fatalf("cannot read the for_each container tag")
	}
	if tag := body[open : open+end+1]; strings.Contains(tag, "data-drop-branch") {
		t.Fatalf("for_each container carries data-drop-branch: %s", tag)
	}

	// The containers must hold their children, not decorate a flat list: a
	// seeded then child renders inside the then container, an else child
	// inside the else one.
	thenStart := strings.Index(body, `data-drop-parent="`+ifID+`" data-drop-branch="then"`)
	elseStart := strings.Index(body, `data-drop-parent="`+ifID+`" data-drop-branch="else"`)
	if thenStart < 0 || elseStart < 0 || elseStart < thenStart {
		t.Fatalf("cannot locate the then/else containers (then=%d else=%d)", thenStart, elseStart)
	}
	thenBox, elseBox := body[thenStart:elseStart], body[elseStart:]
	if !strings.Contains(thenBox, "Then work") {
		t.Error("the seeded then child does not render inside the then container")
	}
	if strings.Contains(thenBox, "Else work") {
		t.Error("the else child renders inside the then container")
	}
	if !strings.Contains(elseBox, "Else work") {
		t.Error("the seeded else child does not render inside the else container")
	}
}
