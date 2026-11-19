package main

// Keyboard moves on the canvas document (Process Designer, Requirements
// AC-37 to AC-45). These are the pure transforms the POST handler applies
// before the ordinary structural save, so the rules can be asserted without a
// database; the stored consequences are covered in
// canvas_slice3_integration_test.go.

import (
	"strings"
	"testing"

	"velm/internal/db"
)

func moveDoc() []db.StructureBlock {
	return []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "i", Kind: "control", Type: "if", Label: "If", Position: 10, Config: map[string]any{"condition": "expr: true"}},
		{Key: "a", Parent: "i", Kind: "task", Slug: "aaa", Label: "A", Position: 20},
		{Key: "b", Parent: "i", Kind: "task", Slug: "bbb", Label: "B", Position: 30},
		{Key: "x", Kind: "task", Slug: "xxx", Label: "X", Position: 40},
		{Key: "y", Kind: "task", Slug: "yyy", Label: "Y", Position: 50},
	}
}

func keysInOrder(blocks []db.StructureBlock) string {
	keys := make([]string, len(blocks))
	for index, block := range blocks {
		keys[index] = block.Key
	}
	return strings.Join(keys, ",")
}

func blockByKey(blocks []db.StructureBlock, key string) db.StructureBlock {
	for _, block := range blocks {
		if block.Key == key {
			return block
		}
	}
	return db.StructureBlock{}
}

// AC-38: a same-scope reorder changes document order and positions only;
// every block keeps its own parent, kind and payload.
func TestStructureMoveSwapAdjacentSiblings(t *testing.T) {
	blocks, refusal := applyStructureMove(moveDoc(), "x", "up")
	if refusal != "" {
		t.Fatalf("move x up refused: %s", refusal)
	}
	// x's previous sibling in the root scope is the control block i, whose
	// children travel with it, so the pre-order result is t,x,i,a,b,y.
	if got := keysInOrder(blocks); got != "t,x,i,a,b,y" {
		t.Fatalf("order after moving x up = %s, want t,x,i,a,b,y", got)
	}
	if x, i := blockByKey(blocks, "x"), blockByKey(blocks, "i"); x.Position != 10 || i.Position != 20 {
		t.Fatalf("positions after swap: x=%d i=%d, want x=10 i=20", x.Position, i.Position)
	}

	blocks, refusal = applyStructureMove(moveDoc(), "i", "down")
	if refusal != "" {
		t.Fatalf("move i down refused: %s", refusal)
	}
	if got := keysInOrder(blocks); got != "t,x,i,a,b,y" {
		t.Fatalf("order after moving i down = %s, want t,x,i,a,b,y", got)
	}
}

// AC-37: moving a control block moves its whole subtree and leaves every
// internal parent link and internal order untouched.
func TestStructureMoveCarriesSubtree(t *testing.T) {
	blocks, refusal := applyStructureMove(moveDoc(), "i", "down")
	if refusal != "" {
		t.Fatalf("move i down refused: %s", refusal)
	}
	if got := keysInOrder(blocks); got != "t,x,i,a,b,y" {
		t.Fatalf("subtree not moved as a unit: %s", got)
	}
	if a := blockByKey(blocks, "a"); a.Parent != "i" {
		t.Fatalf("a parent = %q, want i", a.Parent)
	}
	if b := blockByKey(blocks, "b"); b.Parent != "i" {
		t.Fatalf("b parent = %q, want i", b.Parent)
	}
	// A and B keep their internal order inside the moved scope.
	if a, b := blockByKey(blocks, "a").Position, blockByKey(blocks, "b").Position; !(a < b) {
		t.Fatalf("internal order changed: a=%d b=%d", a, b)
	}
}

// AC-40: dragging a scoped block out to root sets its parent to "".
func TestStructureMoveOutdentToRoot(t *testing.T) {
	blocks, refusal := applyStructureMove(moveDoc(), "a", "outdent")
	if refusal != "" {
		t.Fatalf("outdent a refused: %s", refusal)
	}
	if a := blockByKey(blocks, "a"); a.Parent != "" {
		t.Fatalf("a parent = %q after outdent, want root", a.Parent)
	}
	if got := keysInOrder(blocks); got != "t,i,b,a,x,y" {
		t.Fatalf("order after outdent = %s, want t,i,b,a,x,y", got)
	}
}

// AC-39: indenting under the immediately preceding control block makes the
// block its last child.
func TestStructureMoveIndentUnderPrecedingControl(t *testing.T) {
	blocks, refusal := applyStructureMove(moveDoc(), "x", "indent")
	if refusal != "" {
		t.Fatalf("indent x refused: %s", refusal)
	}
	if x := blockByKey(blocks, "x"); x.Parent != "i" {
		t.Fatalf("x parent = %q after indent, want i", x.Parent)
	}
	// x is now the last child of i, after a and b.
	if got := keysInOrder(blocks); got != "t,i,a,b,x,y" {
		t.Fatalf("order after indent = %s, want t,i,a,b,x,y", got)
	}
	if x, b := blockByKey(blocks, "x").Position, blockByKey(blocks, "b").Position; !(x > b) {
		t.Fatalf("x position %d is not after the last child of i (%d)", x, b)
	}
}

// AC-45: indenting is only available where the preceding sibling is a
// control block, and the refusal names the block.
func TestStructureMoveIndentRefusedAfterTask(t *testing.T) {
	blocks := []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start"},
		{Key: "a", Kind: "task", Slug: "aaa", Label: "First"},
		{Key: "b", Kind: "task", Slug: "bbb", Label: "Second"},
	}
	_, refusal := applyStructureMove(blocks, "b", "indent")
	if !strings.Contains(refusal, "indent needs a control block") {
		t.Fatalf("indent refusal = %q, want it to explain the control requirement", refusal)
	}
	if !strings.Contains(refusal, "Second") {
		t.Fatalf("indent refusal does not name the block: %q", refusal)
	}
}

// AC-41 and AC-44: bounds are no-ops, not refusals, and Start stays first.
func TestStructureMoveBoundsAreNoOps(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		// x is not at the up bound (i precedes it) so the bound blocks are i
		// for up and y for down.
		blocks, refusal := applyStructureMove(moveDoc(), map[string]string{"up": "i", "down": "y"}[direction], direction)
		if refusal != "" {
			t.Fatalf("%s at its bound refused: %s", direction, refusal)
		}
		if got := keysInOrder(blocks); got != "t,i,a,b,x,y" {
			t.Fatalf("%s at its bound changed the document: %s", direction, got)
		}
	}
	// The first block in a scope after Start cannot move above it.
	blocks, refusal := applyStructureMove(moveDoc(), "i", "up")
	if refusal != "" {
		t.Fatalf("move i up refused: %s", refusal)
	}
	if got := keysInOrder(blocks); got != "t,i,a,b,x,y" {
		t.Fatalf("a move above the trigger displaced Start: %s", got)
	}
}

// AC-52/AC-53: the server decides which move controls are truthful bounds.
func TestMoveCapabilitiesFollowStoredTree(t *testing.T) {
	blocks := []db.DocumentBlock{
		{ID: "1", Kind: "trigger", Position: 0},
		{ID: "2", Kind: "control", Type: "if", Position: 10},
		{ID: "3", Kind: "task", ParentID: "2", Position: 10},
		{ID: "4", Kind: "task", ParentID: "2", Position: 20},
		{ID: "5", Kind: "task", ParentID: "", Position: 20},
		{ID: "6", Kind: "task", ParentID: "", Position: 30},
	}
	view := make([]canvasBlockView, len(blocks))
	applyMoveCapabilities(blocks, view)

	if view[1].CanMoveUp {
		t.Error("the block after Start offers Move up, which would displace the trigger")
	}
	if view[2].CanMoveUp || view[2].CanMoveDown != true {
		t.Errorf("first child bounds: up=%v down=%v, want up=false down=true", view[2].CanMoveUp, view[2].CanMoveDown)
	}
	if !view[3].CanMoveUp || view[3].CanMoveDown {
		t.Errorf("last child bounds: up=%v down=%v, want up=true down=false", view[3].CanMoveUp, view[3].CanMoveDown)
	}
	if view[2].CanIndent || view[3].CanIndent {
		t.Error("a task preceded by a task offers Indent")
	}
	// Block 5's preceding sibling in the root scope is the if (block 2), even
	// though block 4 sits between them in document order, so Indent is offered
	// (AC-45). Block 6's preceding sibling is a task, so its Indent is not.
	if !view[4].CanIndent {
		t.Error("the root block whose preceding sibling is a control does not offer Indent")
	}
	if view[5].CanIndent {
		t.Error("the root block after a task offers Indent")
	}
	if view[2].CanOutdent != true || view[4].CanOutdent != false {
		t.Errorf("outdent bounds: child=%v root=%v, want true and false", view[2].CanOutdent, view[4].CanOutdent)
	}
}
