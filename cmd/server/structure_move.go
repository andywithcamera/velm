package main

// Keyboard moves on the canvas document (Process Designer, Requirements
// AC-43 to AC-46).
//
// A move is expressed against the same document the canvas submits, not as a
// second write path: the buttons post the whole document with a `move` field,
// and this file computes the one move that field asks for before the ordinary
// structural save stores it. So a keyboard author needs no JavaScript, every
// button posts exactly one document, and the rules live where a test can call
// them directly.
//
// Indent and outdent move a block together with its subtree: in the flat
// pre-order document that means the contiguous run of blocks whose ancestor
// chain passes through the moved block.

import (
	"strings"

	"velm/internal/db"
)

// applyStructureMove performs one move on a copy of the document. An empty
// message with an unchanged document means a no-op at a bound (first up, last
// down) and is a success, not a refusal.
func applyStructureMove(blocks []db.StructureBlock, key, direction string) ([]db.StructureBlock, string) {
	key = strings.TrimSpace(key)
	direction = strings.TrimSpace(direction)
	index := structureBlockIndex(blocks, key)
	if index < 0 {
		return blocks, "That block is no longer in the document; reload the page"
	}
	block := blocks[index]
	label := structureBlockLabel(block)
	parent := strings.TrimSpace(block.Parent)

	siblings := structureSiblings(blocks, parent)
	siblingIndex := -1
	for i, sibling := range siblings {
		if sibling == index {
			siblingIndex = i
			break
		}
	}

	switch direction {
	case "up":
		if siblingIndex <= 0 {
			return blocks, ""
		}
		anchor := blocks[siblings[siblingIndex-1]]
		// The trigger is structural and pinned first (Design Vision D6): a
		// move that would displace it is a no-op, never a refusal the button
		// offered.
		if strings.TrimSpace(anchor.Kind) == "trigger" {
			return blocks, ""
		}
		return relocateStructureSubtree(blocks, index, parent, strings.TrimSpace(anchor.Key), true), ""
	case "down":
		if siblingIndex < 0 || siblingIndex >= len(siblings)-1 {
			return blocks, ""
		}
		anchor := blocks[siblings[siblingIndex+1]]
		return relocateStructureSubtree(blocks, index, parent, strings.TrimSpace(anchor.Key), false), ""
	case "indent":
		if siblingIndex <= 0 {
			return blocks, "Block " + strconvQuote(label) + ": indent needs a control block immediately before it"
		}
		previous := blocks[siblings[siblingIndex-1]]
		if strings.TrimSpace(previous.Kind) != "control" {
			return blocks, "Block " + strconvQuote(label) + ": indent needs a control block immediately before it"
		}
		return relocateStructureSubtree(blocks, index, strings.TrimSpace(previous.Key), strings.TrimSpace(previous.Key), false), ""
	case "outdent":
		if parent == "" {
			return blocks, ""
		}
		parentIndex := structureBlockIndex(blocks, parent)
		if parentIndex < 0 {
			return blocks, "Block " + strconvQuote(label) + ": its parent is no longer in the document"
		}
		grandparent := strings.TrimSpace(blocks[parentIndex].Parent)
		return relocateStructureSubtree(blocks, index, grandparent, parent, false), ""
	default:
		return blocks, "Block " + strconvQuote(label) + ": unknown move " + strconvQuote(direction)
	}
}

// structureBlockLabel is the label a refusal names, falling back the same way
// the canvas does so the author recognises the block.
func structureBlockLabel(block db.StructureBlock) string {
	if label := strings.TrimSpace(block.Label); label != "" {
		return label
	}
	if slug := strings.TrimSpace(block.Slug); slug != "" {
		return slug
	}
	return "block " + strings.TrimSpace(block.Key)
}

func structureBlockIndex(blocks []db.StructureBlock, key string) int {
	if key == "" {
		return -1
	}
	for index, block := range blocks {
		if strings.TrimSpace(block.Key) == key {
			return index
		}
	}
	return -1
}

// structureSiblings returns the indexes of the blocks whose parent is parent,
// in document order (which is scope order).
func structureSiblings(blocks []db.StructureBlock, parent string) []int {
	var indexes []int
	for index, block := range blocks {
		if strings.TrimSpace(block.Parent) == parent {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

// structureSubtreeEnd is the index just past the block's last descendant in
// the flat pre-order document.
func structureSubtreeEnd(blocks []db.StructureBlock, index int) int {
	end := index + 1
	for end < len(blocks) && structureIsDescendant(blocks, blocks[end], blocks[index].Key) {
		end++
	}
	return end
}

func structureIsDescendant(blocks []db.StructureBlock, block db.StructureBlock, ancestorKey string) bool {
	byKey := make(map[string]db.StructureBlock, len(blocks))
	for _, candidate := range blocks {
		byKey[strings.TrimSpace(candidate.Key)] = candidate
	}
	seen := map[string]bool{}
	for parent := strings.TrimSpace(block.Parent); parent != ""; {
		if parent == ancestorKey {
			return true
		}
		if seen[parent] {
			return false
		}
		seen[parent] = true
		next, found := byKey[parent]
		if !found {
			return false
		}
		parent = strings.TrimSpace(next.Parent)
	}
	return false
}

// relocateStructureSubtree lifts the block and its descendants out of the
// document and reinserts them under newParent, either before the anchor's
// subtree or after it. Positions are renumbered inside every scope so the
// stored order is what the author now sees.
func relocateStructureSubtree(blocks []db.StructureBlock, index int, newParent, anchorKey string, before bool) []db.StructureBlock {
	end := structureSubtreeEnd(blocks, index)
	subtree := make([]db.StructureBlock, end-index)
	copy(subtree, blocks[index:end])
	rest := make([]db.StructureBlock, 0, len(blocks))
	rest = append(rest, blocks[:index]...)
	rest = append(rest, blocks[end:]...)

	subtree[0].Parent = newParent

	insertAt := len(rest)
	if anchorKey != "" {
		anchorIndex := structureBlockIndex(rest, anchorKey)
		if anchorIndex >= 0 {
			if before {
				insertAt = anchorIndex
			} else {
				insertAt = structureSubtreeEnd(rest, anchorIndex)
			}
		}
	}
	result := make([]db.StructureBlock, 0, len(blocks))
	result = append(result, rest[:insertAt]...)
	result = append(result, subtree...)
	result = append(result, rest[insertAt:]...)
	renumberStructurePositions(result)
	return result
}

// renumberStructurePositions gives every scope positions 10, 20, 30… in
// document order. sort_order is written from the same value by the store, so
// the rows view and the tree agree.
func renumberStructurePositions(blocks []db.StructureBlock) {
	next := map[string]int{}
	for index := range blocks {
		parent := strings.TrimSpace(blocks[index].Parent)
		if strings.TrimSpace(blocks[index].Kind) == "trigger" {
			// Start keeps position 0 (Design Vision D6); everything else in
			// a scope is spaced by 10 from there.
			blocks[index].Position = 0
			continue
		}
		if next[parent] == 0 {
			next[parent] = 10
		} else {
			next[parent] += 10
		}
		blocks[index].Position = next[parent]
	}
}

// applyMoveCapabilities fills the four keyboard affordances from the stored
// tree. A button is disabled at a bound, and Indent only where the preceding
// sibling is a control block, so the canvas never offers a move the handler
// will refuse.
func applyMoveCapabilities(blocks []db.DocumentBlock, view []canvasBlockView) {
	for index := range view {
		if index >= len(blocks) {
			return
		}
		block := blocks[index]
		parent := strings.TrimSpace(block.ParentID)
		siblingIndex := -1
		siblings := 0
		previousIsControl := false
		predecessorIsTrigger := false
		var previous db.DocumentBlock
		for _, candidate := range blocks {
			if strings.TrimSpace(candidate.ParentID) != parent {
				continue
			}
			if candidate.ID == block.ID {
				siblingIndex = siblings
				if siblings > 0 {
					previousIsControl = strings.TrimSpace(previous.Kind) == "control"
					predecessorIsTrigger = strings.TrimSpace(previous.Kind) == "trigger"
				}
			}
			previous = candidate
			siblings++
		}
		canMoveUp := siblingIndex > 0
		if canMoveUp && predecessorIsTrigger {
			// Move up would put the block before Start, which the trigger
			// invariant refuses; do not offer it (Design Vision D6).
			canMoveUp = false
		}
		view[index].CanMoveUp = canMoveUp
		view[index].CanMoveDown = siblingIndex >= 0 && siblingIndex < siblings-1
		view[index].CanIndent = siblingIndex > 0 && previousIsControl
		view[index].CanOutdent = parent != ""
	}
}
