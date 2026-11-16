package main

// Structural save for a definition tree (Process Designer, Plan T4).
//
// The runtime reads a block tree, so the blocks have to be writable before it
// can be exercised. The canvas and the raw view (slice 5) both post the same
// document here, which is the one structural write path (Requirements AC-74).
//
// The rules this slice owns are the two refusals a run depends on:
//
//   - an `if` condition is a CEL expression behind `expr:`; a bare string is
//     refused at save, not evaluated as one (AC-26);
//   - `wait` — and `parallel`, which needs the same waker — is refused by
//     name with "not available in this release" (AC-45), so no definition can
//     carry a block the engine cannot evaluate.
//
// Everything else the handler can know before the database is refused here,
// with the block named, and leaves the stored document untouched.

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"velm/internal/db"
)

// structureDocument is what the canvas and the raw view send: one list, in
// document order, with children naming their parent by key.
type structureDocument struct {
	Blocks []db.StructureBlock `json:"blocks"`
}

// handleTaskDefinitionStructureSave replaces a definition's block tree.
func handleTaskDefinitionStructureSave(w http.ResponseWriter, r *http.Request) {
	row, ok := beginStepMutation(w, r)
	if !ok {
		return
	}
	if !row.IsContainer {
		refuseStep(w, r, row, "This definition is not a container; tick Container in Basics first")
		return
	}
	blocks, message := readStructureDocument(r)
	if message != "" {
		refuseStep(w, r, row, message)
		return
	}
	if message := validateStructureBlocks(blocks); message != "" {
		refuseStep(w, r, row, message)
		return
	}
	if err := db.SaveDefinitionStructure(r.Context(), row.ID, blocks); err != nil {
		log.Printf("task definition %q structure save failed: %v", row.Slug, err)
		refuseStep(w, r, row, "The structure could not be saved: "+err.Error())
		return
	}
	succeedStep(w, r, row, "Structure saved.", "")
}

// readStructureDocument accepts the JSON body the canvas sends or the
// `structure` form field the raw tab posts. Both carry the same document.
func readStructureDocument(r *http.Request) ([]db.StructureBlock, string) {
	payload := strings.TrimSpace(r.PostFormValue("structure"))
	if payload == "" {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return nil, "The structure could not be read"
		}
		payload = strings.TrimSpace(string(body))
	}
	if payload == "" {
		return nil, "There is no structure to save"
	}
	var document structureDocument
	if err := json.Unmarshal([]byte(payload), &document); err != nil {
		// A bare array is the same document without the wrapper.
		if arrayErr := json.Unmarshal([]byte(payload), &document.Blocks); arrayErr != nil {
			return nil, "The structure is not valid JSON: " + err.Error()
		}
	}
	return document.Blocks, ""
}

// validateStructureBlocks is the handler-side gate: readable messages, no
// database, and every rule checked before the tree is replaced.
func validateStructureBlocks(blocks []db.StructureBlock) string {
	if len(blocks) == 0 {
		return "A structure needs at least one block"
	}
	kindByKey := map[string]db.StructureBlock{}
	for index, b := range blocks {
		label := strings.TrimSpace(b.Label)
		if label == "" {
			label = strings.TrimSpace(b.Slug)
		}
		if label == "" {
			label = "block " + strings.TrimSpace(b.Key)
		}
		switch strings.TrimSpace(b.Kind) {
		case "task":
			if strings.TrimSpace(b.Slug) == "" {
				return "Block " + strconvQuote(label) + " calls no definition: choose one"
			}
		case "trigger", "stage":
			// Structure only: no runtime branch, no definition.
		case "control":
			switch strings.TrimSpace(b.Type) {
			case "if":
				condition := blockConfigText(b, "condition")
				if condition == "" {
					return "Block " + strconvQuote(label) + ": an if block needs a condition"
				}
				if !strings.HasPrefix(condition, "expr:") {
					return "Block " + strconvQuote(label) + ": the condition must be a CEL expression behind the expr: prefix, for example expr: inputs.exit_code == 0"
				}
			case "for_each":
				if blockConfigText(b, "collection") == "" {
					return "Block " + strconvQuote(label) + ": a for_each block needs a collection"
				}
			case "end":
				// No configuration.
			case "wait":
				return "Block " + strconvQuote(label) + ": wait blocks are not available in this release"
			case "parallel":
				return "Block " + strconvQuote(label) + ": parallel blocks are not available in this release"
			default:
				return "Block " + strconvQuote(label) + ": choose a control type — if, for_each or end"
			}
		default:
			return "Block " + strconvQuote(label) + ": that block kind is not available in this release"
		}
		key := strings.TrimSpace(b.Key)
		if key != "" {
			kindByKey[key] = b
		}
		if parent := strings.TrimSpace(b.Parent); parent != "" {
			parentBlock, found := kindByKey[parent]
			if !found {
				return "Block " + strconvQuote(label) + ": its parent must be a block earlier in the document"
			}
			if strings.TrimSpace(parentBlock.Kind) != "control" {
				return "Block " + strconvQuote(label) + ": only a control block can hold a scope"
			}
		}
		_ = index
	}
	return ""
}

func blockConfigText(b db.StructureBlock, key string) string {
	if b.Config == nil {
		return ""
	}
	value, _ := b.Config[key].(string)
	return strings.TrimSpace(value)
}

func strconvQuote(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `"` + value + `"`
	}
	return string(encoded)
}
