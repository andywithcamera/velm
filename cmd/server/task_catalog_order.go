package main

// The catalogue's order page (2026-11-25).
//
// Ordering used to happen inside the card: a <details> opened a form in the
// grid cell, and the form posted to a shared endpoint. Andy's review found
// what that costs. The whole results region is swapped on search, so a
// half-filled form vanished on a keystroke in a different control; an open
// <details> stretched its grid row; the button was replaced by its own
// confirmation, so a card went dead after one use; and the only number that
// decides "should I start this?" — what the chain will open — was not on the
// card at all.
//
// So the grid is now a set of links and everything that acts lives here: one
// definition, its steps in order, its declared inputs, one Start button.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"velm/internal/db"
)

// catalogFieldView is one input control with the value it should carry. The
// stored default is the starting value; a refused submission passes what the
// requester typed back in, because making someone retype a form to fix one
// missing field is how a browser Back button gets used instead.
type catalogFieldView struct {
	db.TaskInputField
	Value string
}

// catalogOrderFields turns declared inputs into the controls the template
// renders. Separated from the handler so the value precedence is testable
// without a database.
//
// A submitted form wins over the stored default, except for a checkbox: an
// unchecked box is simply absent from the form, so absence there means "no",
// not "fall back to the default", which would re-tick a box the requester just
// cleared.
func catalogOrderFields(fields []db.TaskInputField, submitted url.Values) []catalogFieldView {
	out := make([]catalogFieldView, 0, len(fields))
	for _, field := range fields {
		value := field.DefaultValue
		if submitted != nil {
			raw, ok := submitted[field.Name]
			switch {
			case ok:
				value = raw[0]
			case field.InputControlKind() == "checkbox":
				value = ""
			}
		}
		out = append(out, catalogFieldView{TaskInputField: field, Value: value})
	}
	return out
}

// catalogStep is one line of "what this chain does". Kind is empty for a step
// that is a task definition, and names the construct for a control block, so
// the reader can tell an If from a task called "If".
type catalogStep struct {
	// Number is the position in the readable sequence, not the block's stored
	// position: the trigger and a scope's End sit between the steps and are
	// not counted, so numbering by position would jump.
	Number int
	Label  string
	Kind   string
	Depth  int
}

// catalogControlLabels is the palette's vocabulary, read back. The canvas
// names these for the author; the order page names them for the requester, who
// needs to know a branch exists before starting something that branches.
var catalogControlLabels = map[string]string{
	"if":         "If",
	"for_each":   "For each",
	"run_task":   "Run task",
	"run_script": "Run script",
	"retry":      "Retry",
	"parallel":   "Parallel",
	"wait":       "Wait",
	"end":        "End",
}

// catalogStepsFromDocument renders the stored tree as an ordered list. The
// trigger and the closing End of a scope are boundaries, not steps, so they
// are left out: an order page that opens with "Start" tells the requester
// nothing.
//
// Depth comes from the block's parent, computed here rather than stored,
// because a block whose parent was filtered out must not indent into a step
// that is no longer on the page.
func catalogStepsFromDocument(blocks []db.DocumentBlock) []catalogStep {
	byID := make(map[string]db.DocumentBlock, len(blocks))
	for _, block := range blocks {
		byID[block.ID] = block
	}
	depthOf := func(block db.DocumentBlock) int {
		depth := 0
		parent := block.ParentID
		for parent != "" && depth < 8 {
			ancestor, ok := byID[parent]
			if !ok {
				break
			}
			if ancestor.Kind != "trigger" {
				depth++
			}
			parent = ancestor.ParentID
		}
		return depth
	}
	out := make([]catalogStep, 0, len(blocks))
	for _, block := range blocks {
		if block.Kind == "trigger" {
			continue
		}
		if block.Kind == "control" && block.Type == "end" {
			continue
		}
		step := catalogStep{Number: len(out) + 1, Depth: depthOf(block)}
		if block.Kind == "task" {
			step.Label = firstNonEmpty(block.Label, block.DisplayName, block.Slug)
			if block.IsContainer {
				step.Kind = fmt.Sprintf("chain of %d %s", block.ChildStepCount, stepWord(block.ChildStepCount))
			}
		} else {
			step.Kind = firstNonEmpty(catalogControlLabels[block.Type], block.Type, block.Kind)
			step.Label = firstNonEmpty(block.Label, step.Kind)
			if step.Label == step.Kind {
				step.Kind = ""
			}
		}
		out = append(out, step)
	}
	return out
}

// taskCatalogOrderView is everything the order page needs. It is built by one
// loader so the GET page and a refused POST render the same thing.
type taskCatalogOrderView struct {
	Slug        string
	DisplayName string
	Description string
	IsContainer bool
	StepCount   int
	Active      bool
	Steps       []catalogStep
	Fields      []catalogFieldView
	// Problem is set when the order cannot be placed at all: inactive, or the
	// stored plumbing would fail. It renders instead of the Start button, so
	// the reader learns why before typing, not after submitting.
	Problem string
	// Error is a refusal of the submission itself: a missing required field,
	// a value outside a choice list, the for_each cap.
	Error string
}

// Outcome is the sentence above the steps: what pressing Start will create.
// It is derived rather than stored so the page and the button cannot disagree,
// and so it is testable without a database.
func (v taskCatalogOrderView) Outcome() string {
	if !v.IsContainer {
		return "Starting this opens a single task."
	}
	if v.StepCount == 0 {
		return "Starting this opens a chain with no steps yet, so it will create only the chain task."
	}
	return fmt.Sprintf("Starting this opens a chain of %d %s.", v.StepCount, stepWord(v.StepCount))
}

// HasInputs mirrors the catalogue entry: a definition that declares nothing
// takes no input and starts in one click.
func (v taskCatalogOrderView) HasInputs() bool { return len(v.Fields) > 0 }

// KindLabel names what will be spawned, in the same words as the catalogue
// card, so the two pages cannot disagree.
func (v taskCatalogOrderView) KindLabel() string {
	if !v.IsContainer {
		return "Single task"
	}
	return fmt.Sprintf("Chain · %d %s", v.StepCount, stepWord(v.StepCount))
}

// StartLabel is the button: it says what it will open.
func (v taskCatalogOrderView) StartLabel() string {
	if !v.IsContainer {
		return "Start"
	}
	return fmt.Sprintf("Start chain · %d %s", v.StepCount, stepWord(v.StepCount))
}

func catalogOrderURL(slug string) string { return "/task-catalogue/" + slug + "/start" }

// loadTaskCatalogOrder reads one definition for the order page. found is false
// only when the slug names no live definition: a definition the requester is
// not allowed to see must behave the same way as one that does not exist, so
// the page cannot be used to enumerate slugs.
func loadTaskCatalogOrder(ctx context.Context, r *http.Request, slug string, submitted url.Values) (taskCatalogOrderView, bool, error) {
	var view taskCatalogOrderView
	var definitionID, inputSchema string
	err := db.Pool.QueryRow(ctx, `
		SELECT d._id::text, d.slug, d.display_name, d.description, d.is_container,
		       d.is_active, COALESCE(d.input_schema_yaml, ''),
		       (SELECT count(*) FROM base_task_definition_child c
		         WHERE c.container_definition_id = d._id
		           AND COALESCE(c.block_kind, 'task') <> 'trigger')
		FROM base_task_definition d
		WHERE d.slug = $1 AND d._deleted_at IS NULL`, slug).
		Scan(&definitionID, &view.Slug, &view.DisplayName, &view.Description,
			&view.IsContainer, &view.Active, &inputSchema, &view.StepCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return view, false, nil
	}
	if err != nil {
		return view, false, err
	}

	fields, parseErr := db.ParseTaskInputSchema(inputSchema)
	if parseErr != nil {
		// A malformed schema blocks the order loudly. The catalogue still lists
		// the definition so its author can find it and fix it, which is the
		// opposite of hiding the entry that needs attention.
		view.Problem = "This definition's input form is misconfigured: " + parseErr.Error()
	}
	view.Fields = catalogOrderFields(fields, submitted)

	if view.IsContainer {
		blocks, err := db.ReadStoredDefinitionDocument(ctx, definitionID)
		if err != nil {
			return view, true, err
		}
		view.Steps = catalogStepsFromDocument(blocks)
	}

	if !view.Active {
		view.Problem = "This definition is inactive, so it cannot be started. An editor can reactivate it on the definition page."
		return view, true, nil
	}
	if reason, err := checkStoredPlumbing(ctx, view.Slug, isAdminRequest(r)); err != nil {
		return view, true, err
	} else if reason != "" {
		view.Problem = reason
	}
	return view, true, nil
}

// handleTaskCatalogOrderPage is GET /task-catalogue/{slug}/start.
func handleTaskCatalogOrderPage(w http.ResponseWriter, r *http.Request) {
	if db.Pool == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}
	// No syntax check on the slug: a definition slug may hold hyphens, and the
	// lookup is parameterised, so the database decides what exists. An unknown
	// slug and a soft-deleted one both answer 404, which is also what a slug
	// the requester may not see must do.
	slug := strings.TrimSpace(r.PathValue("slug"))
	view, found, err := loadTaskCatalogOrder(r.Context(), r, slug, nil)
	if err != nil {
		http.Error(w, "Failed to load the definition", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "Unknown definition", http.StatusNotFound)
		return
	}
	renderTaskCatalogOrder(w, r, view, http.StatusOK)
}

// renderTaskCatalogOrder writes the page. The status is passed in because a
// refused submission re-renders the same page with a banner and a 400, which
// is what makes the failure visible to anything reading the response rather
// than only to a person looking at it.
func renderTaskCatalogOrder(w http.ResponseWriter, r *http.Request, view taskCatalogOrderView, status int) {
	title := "Start " + view.DisplayName
	viewData := newViewData(w, r, catalogOrderURL(view.Slug), title, "Operations")
	viewData["View"] = "task-catalog-order"
	viewData["Order"] = view
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
		http.Error(w, "Failed to render the order page", http.StatusInternalServerError)
	}
}
