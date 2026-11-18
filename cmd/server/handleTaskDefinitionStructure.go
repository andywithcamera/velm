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
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
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
	// A keyboard move posts the whole document plus the one move it means
	// (Requirements AC-46). Apply it here, before validation, so the rules
	// for a move and for a stored document cannot drift apart.
	selectedKey := strings.TrimSpace(r.PostFormValue("selected"))
	if direction := strings.TrimSpace(r.PostFormValue("move")); direction != "" {
		moveKey := strings.TrimSpace(r.PostFormValue("move_key"))
		moved, refusal := applyStructureMove(blocks, moveKey, direction)
		if refusal != "" {
			refuseStep(w, r, row, refusal)
			return
		}
		blocks = moved
		selectedKey = moveKey
	}
	if message := validateStructureBlocks(blocks); message != "" {
		refuseStep(w, r, row, message)
		return
	}
	if message := checkScriptAdmin(blocks, isAdminRequest(r)); message != "" {
		refuseStep(w, r, row, message)
		return
	}
	if message := checkPlumbing(r.Context(), row.Slug, blocks, isAdminRequest(r)); message != "" {
		refuseStep(w, r, row, message)
		return
	}
	if err := db.SaveDefinitionStructure(r.Context(), row.ID, blocks); err != nil {
		log.Printf("task definition %q structure save failed: %v", row.Slug, err)
		refuseStep(w, r, row, "The structure could not be saved: "+err.Error())
		return
	}
	selectedIndex := structureBlockIndex(blocks, selectedKey)
	if isHTMXRequest(r) {
		if id := storedBlockIDAt(r, row.ID, selectedIndex); id != "" {
			w.Header().Set("HX-Redirect", "/task-definitions/"+row.Slug+"?view=canvas&block="+id)
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	renderEditorNotice(w, r, row, "Structure saved.", selectedIndex)
}

// renderEditorNotice re-renders the whole editor after a plain (non-HTMX)
// canvas save, for the same reason a refusal does: the canvas form must work
// without JavaScript and never replace the page with a fragment. selectedIndex
// is the position, in document order, of the block the author just moved, so
// the response comes back with that block still expanded (-1 for none).
func renderEditorNotice(w http.ResponseWriter, r *http.Request, row db.TaskDefinitionRow, notice string, selectedIndex int) {
	viewData, ok := editorViewData(w, r, row)
	if !ok {
		return
	}
	viewData["EditorView"] = "canvas"
	canvas, _ := viewData["Canvas"].(canvasView)
	canvas.Notice = notice
	viewData["Canvas"] = canvas
	if selectedIndex >= 0 {
		if blocks, err := db.ReadStoredDefinitionDocument(r.Context(), row.ID); err == nil && selectedIndex < len(blocks) {
			if selected, buildErr := buildCanvasView(r.Context(), row, blocks, blocks[selectedIndex].ID, isAdminRequest(r)); buildErr == nil {
				selected.Notice = notice
				viewData["Canvas"] = selected
			}
		}
	}
	writeTaskDefinitionPage(w, "layout.html", viewData, http.StatusOK)
}

// storedBlockIDAt returns the id of the block at a document position after a
// save; ids change on every structural write, so selection travels by index.
func storedBlockIDAt(r *http.Request, definitionID string, index int) string {
	if index < 0 {
		return ""
	}
	blocks, err := db.ReadStoredDefinitionDocument(r.Context(), definitionID)
	if err != nil || index >= len(blocks) {
		return ""
	}
	return blocks[index].ID
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
		case "trigger", "stage", "gate":
			// Structure only: no definition to resolve. A gate's exits are
			// checked by the plumbing check, not here.
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
				if !blockConfigPresent(b, "collection") {
					return "Block " + strconvQuote(label) + ": a for_each block needs a collection"
				}
			case "run_script":
				if blockConfigText(b, "script") == "" {
					return "Block " + strconvQuote(label) + ": a run script block needs a script body"
				}
				if raw, present := b.Config["timeout_ms"]; present && raw != nil && fmt.Sprint(raw) != "" {
					ms, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(raw)))
					if err != nil || ms < 100 || ms > 60000 {
						return "Block " + strconvQuote(label) + ": timeout_ms must be between 100 and 60000"
					}
				}
			case "retry":
				if raw, present := b.Config["attempts"]; present && raw != nil && fmt.Sprint(raw) != "" {
					n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(raw)))
					if err != nil || n < 1 || n > 10 {
						return "Block " + strconvQuote(label) + ": attempts must be between 1 and 10"
					}
				}
				if raw, present := b.Config["delay_seconds"]; present && raw != nil && fmt.Sprint(raw) != "" {
					n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(raw)))
					if err != nil || n < 0 || n > 3600 {
						return "Block " + strconvQuote(label) + ": delay_seconds must be between 0 and 3600"
					}
				}
				if backoff := blockConfigText(b, "backoff"); backoff != "" {
					switch backoff {
					case "none", "linear", "exponential":
					default:
						return "Block " + strconvQuote(label) + ": backoff must be none, linear or exponential"
					}
				}
				if onExhausted := blockConfigText(b, "on_exhausted"); onExhausted != "" {
					if onExhausted != "fail" && onExhausted != "continue" {
						return "Block " + strconvQuote(label) + ": on_exhausted must be fail or continue"
					}
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
		if strategy := strings.TrimSpace(b.AssignmentStrategy); strategy != "" {
			valid := false
			for _, known := range db.AssignmentStrategies {
				if strategy == known {
					valid = true
					break
				}
			}
			if !valid {
				return "Block " + strconvQuote(label) + ": " + strconvQuote(strategy) + " is not an assignment strategy"
			}
		}
		key := strings.TrimSpace(b.Key)
		if key != "" {
			kindByKey[key] = b
		}
		if parent := strings.TrimSpace(b.Parent); parent != "" {
			if parent == key && key != "" {
				return "Block " + strconvQuote(label) + ": a block cannot be its own parent: that cycle cannot run"
			}
			parentBlock, found := kindByKey[parent]
			if !found {
				return "Block " + strconvQuote(label) + ": its parent must be a block earlier in the document"
			}
			if strings.TrimSpace(parentBlock.Kind) != "control" {
				return "Block " + strconvQuote(label) + ": only a control block can hold a scope"
			}
			if strings.TrimSpace(parentBlock.Type) == "if" {
				branch := strings.TrimSpace(b.Branch)
				if branch != "" && branch != "then" && branch != "else" {
					return "Block " + strconvQuote(label) + ": branch must be then or else"
				}
			} else if strings.TrimSpace(b.Branch) != "" {
				return "Block " + strconvQuote(label) + ": only a child of an if has a branch"
			}
		}
		_ = index
	}
	return ""
}

// blockConfigPresent treats an empty array or object as present: a for_each
// over a literal list is authored as config["collection"] = [].
func blockConfigPresent(b db.StructureBlock, key string) bool {
	if b.Config == nil {
		return false
	}
	value, found := b.Config[key]
	return found && value != nil
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
