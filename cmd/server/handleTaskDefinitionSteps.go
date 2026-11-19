package main

// Task definition steps (#358, slice 3).
//
// A container is a caller: it decides which definitions run, in what order, and
// what each step receives. This section is that caller's editing surface — an
// ordered stepper with add, remove, move, dependency and input-mapping controls,
// all server-rendered, all HTMX POSTs, none of them JavaScript state.
//
// Two rules shape it:
//
//  1. Everything the handler can know before touching the database is refused
//     there, with a message naming the step. The database keeps its two votes:
//     the unique key on (container, child) and 084's acyclicity trigger. Those
//     are translated into English, never a 500 and never a raw pg error.
//  2. A refusal keeps status 400 and tells HTMX where to put the text
//     (HX-Retarget, HX-Reswap); a 400 with a body nobody swaps is a message the
//     author never sees.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"

	"velm/internal/db"
)

const stepErrorRegion = "#step-errors"

const (
	stepSelfMessage      = "A container cannot contain itself"
	stepDuplicateMessage = "This definition is already a step in this container"
	stepCycleMessage     = "This dependency would create a cycle"
	stepReasonMessage    = "That is not a reason a step can wait on"
	stepNoInputsWarning  = "This definition declares no inputs; keys cannot be checked"
)

// stepDependencyView is one depends_on entry, shown as the sibling's position
// in the stepper rather than as a uuid.
type stepDependencyView struct {
	Slug     string
	Position int
	Missing  bool
}

type stepSiblingView struct {
	Slug     string
	Position int
	Checked  bool
}

type stepMappingView struct {
	Name  string
	Value string
}

type stepView struct {
	// ContainerSlug and CSRFToken are stamped onto each step so the step
	// template can build its own forms: it is rendered with the step as the
	// template's root, not the page map.
	ContainerSlug    string
	CSRFToken        string
	Position         int
	Slug             string
	DisplayName      string
	Deleted          bool
	SortOrder        int
	DependsOn        []stepDependencyView
	DependsOnReason  string
	Mapping          []stepMappingView
	UnknownMapping   []stepMappingView
	MappingUnchecked bool
	Siblings         []stepSiblingView
	Reasons          []string
	First            bool
	Last             bool
}

type stepsView struct {
	Container  bool
	Steps      []stepView
	Candidates []db.TaskDefinitionRow
	Error      string
	Notice     string
	Warning    string
}

func stepContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// declaredInputNames returns the child's declared input names and whether they
// can be checked at all. An empty, empty-list or unparseable schema declares no
// names, so no key is refused (AC-60); the editor says so rather than pretending.
func declaredInputNames(schema string) ([]string, bool) {
	fields, err := db.ParseTaskInputSchema(schema)
	if err != nil || len(fields) == 0 {
		return nil, false
	}
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	return names, true
}

func sortedMappingKeys(mapping map[string]string) []string {
	keys := make([]string, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// buildStepsView loads the rows view's stepper in sort_order then _created_at
// order. The canvas and raw views read the block tree's own parent/position
// order instead, so the two presentations can differ while the stored document
// stays single.
func buildStepsView(ctx context.Context, row db.TaskDefinitionRow) (stepsView, error) {
	view := stepsView{Container: row.IsContainer}
	if !row.IsContainer {
		return view, nil
	}
	steps, err := db.ListDefinitionSteps(ctx, row.ID)
	if err != nil {
		return view, err
	}

	positionOf := make(map[string]int, len(steps))
	slugOf := make(map[string]string, len(steps))
	for index, step := range steps {
		positionOf[step.ChildDefinitionID] = index + 1
		slugOf[step.ChildDefinitionID] = step.ChildSlug
	}

	for index, step := range steps {
		item := stepView{
			ContainerSlug:   row.Slug,
			Position:        index + 1,
			Slug:            step.ChildSlug,
			DisplayName:     step.ChildDisplayName,
			Deleted:         step.ChildDeleted,
			SortOrder:       step.SortOrder,
			DependsOnReason: step.DependsOnReason,
			Reasons:         db.TaskClosureReasons,
			First:           index == 0,
			Last:            index == len(steps)-1,
		}
		for _, id := range step.DependsOn {
			dependency := stepDependencyView{Slug: slugOf[id], Position: positionOf[id]}
			if dependency.Position == 0 {
				dependency.Missing = true
			}
			item.DependsOn = append(item.DependsOn, dependency)
		}
		for otherIndex, other := range steps {
			if other.ChildDefinitionID == step.ChildDefinitionID {
				continue
			}
			item.Siblings = append(item.Siblings, stepSiblingView{
				Slug:     other.ChildSlug,
				Position: otherIndex + 1,
				Checked:  stepContains(step.DependsOn, other.ChildDefinitionID),
			})
		}

		names, checked := declaredInputNames(step.ChildInputSchemaYAML)
		item.MappingUnchecked = !checked
		if checked {
			for _, name := range names {
				item.Mapping = append(item.Mapping, stepMappingView{Name: name, Value: step.InputMapping[name]})
			}
			for _, key := range sortedMappingKeys(step.InputMapping) {
				if !stepContains(names, key) {
					item.UnknownMapping = append(item.UnknownMapping, stepMappingView{Name: key, Value: step.InputMapping[key]})
				}
			}
		} else {
			for _, key := range sortedMappingKeys(step.InputMapping) {
				item.Mapping = append(item.Mapping, stepMappingView{Name: key, Value: step.InputMapping[key]})
			}
		}
		view.Steps = append(view.Steps, item)
	}

	candidates, err := db.ListTaskDefinitionRows(ctx, "")
	if err != nil {
		return view, err
	}
	for _, candidate := range candidates {
		if candidate.Slug == row.Slug || positionOf[candidate.ID] > 0 {
			continue
		}
		view.Candidates = append(view.Candidates, candidate)
	}
	return view, nil
}

// stampStepTokens makes the CSRF token available to every per-step form. The
// token is minted once per render, by newViewData.
func stampStepTokens(view *stepsView, token string) {
	for index := range view.Steps {
		view.Steps[index].CSRFToken = token
	}
}

// beginStepMutation is the shared preamble: POST only (a GET on a mutation
// path is 405 and writes nothing, AC-48), a database, a parsed form and a live
// definition. It writes the response itself when it returns false.
func beginStepMutation(w http.ResponseWriter, r *http.Request) (db.TaskDefinitionRow, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return db.TaskDefinitionRow{}, false
	}
	if db.Pool == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return db.TaskDefinitionRow{}, false
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return db.TaskDefinitionRow{}, false
	}
	return loadDefinitionForRequest(w, r)
}

// renderStepper answers a successful mutation with the re-rendered stepper.
func renderStepper(w http.ResponseWriter, r *http.Request, row db.TaskDefinitionRow, view stepsView, status int) {
	viewData := newViewData(w, r, "/task-definitions/"+row.Slug, row.DisplayName, "Operations")
	stampStepTokens(&view, viewData["CSRFToken"].(string))
	viewData["Definition"] = row
	viewData["Steps"] = view
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := templates.ExecuteTemplate(w, "task-definition-stepper", viewData); err != nil {
		log.Printf("task definition %q stepper render failed: %v", row.Slug, err)
	}
}

// refuseStep renders a refusal where the author will see it. With an HTMX
// request that is the #step-errors region; without one it is the full editor
// page, because the canvas form posts normally and a bare fragment would
// replace the page and lose the editor (Design Vision D7, Requirements AC-16,
// AC-17). Every refusal is 400, names no database internals (AC-57) and leaves
// the rows untouched.
func refuseStep(w http.ResponseWriter, r *http.Request, row db.TaskDefinitionRow, message string) {
	if !isHTMXRequest(r) {
		renderEditorRefusal(w, r, row, message)
		return
	}
	view, err := buildStepsView(r.Context(), row)
	if err != nil {
		log.Printf("task definition %q stepper reload failed: %v", row.Slug, err)
		http.Error(w, "Failed to load steps", http.StatusInternalServerError)
		return
	}
	view.Error = message
	w.Header().Set("HX-Retarget", stepErrorRegion)
	w.Header().Set("HX-Reswap", "innerHTML")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	viewData := newViewData(w, r, "/task-definitions/"+row.Slug, row.DisplayName, "Operations")
	stampStepTokens(&view, viewData["CSRFToken"].(string))
	viewData["Definition"] = row
	viewData["Steps"] = view
	if err := templates.ExecuteTemplate(w, "task-definition-step-errors", viewData); err != nil {
		log.Printf("task definition %q step error render failed: %v", row.Slug, err)
	}
}

// isHTMXRequest reports whether HTMX made the request. Only an explicit
// HX-Request header counts; a browser form post has none.
func isHTMXRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("HX-Request")), "true")
}

// renderEditorRefusal re-renders the whole editor with the message inside the
// canvas card, so a plain form post keeps its page.
func renderEditorRefusal(w http.ResponseWriter, r *http.Request, row db.TaskDefinitionRow, message string) {
	// A refusal must not write: the trigger backfill is skipped so AC-12's
	// "row count is unchanged" holds on the very request that was refused.
	viewData, ok := buildEditorViewData(w, r, row, false)
	if !ok {
		return
	}
	// The refusal belongs in the canvas card even when the request carried a
	// support view in the query string.
	viewData["EditorView"] = "canvas"
	canvas, _ := viewData["Canvas"].(canvasView)
	canvas.Error = message
	viewData["Canvas"] = canvas
	writeTaskDefinitionPage(w, "layout.html", viewData, http.StatusBadRequest)
}

// succeedStep re-renders the stepper after a committed write, with an optional
// warning the author needs to see (AC-60).
func succeedStep(w http.ResponseWriter, r *http.Request, row db.TaskDefinitionRow, notice, warning string) {
	view, err := buildStepsView(r.Context(), row)
	if err != nil {
		log.Printf("task definition %q stepper reload failed: %v", row.Slug, err)
		http.Error(w, "Failed to load steps", http.StatusInternalServerError)
		return
	}
	view.Notice = notice
	view.Warning = warning
	renderStepper(w, r, row, view, http.StatusOK)
}

func findStep(steps []db.TaskDefinitionStep, slug string) (db.TaskDefinitionStep, bool) {
	for _, step := range steps {
		if step.ChildSlug == slug {
			return step, true
		}
	}
	return db.TaskDefinitionStep{}, false
}

// handleTaskDefinitionStepAdd appends a child definition as the last step.
func handleTaskDefinitionStepAdd(w http.ResponseWriter, r *http.Request) {
	row, ok := beginStepMutation(w, r)
	if !ok {
		return
	}
	if !row.IsContainer {
		refuseStep(w, r, row, "This definition is not a container; tick Container in Basics first")
		return
	}
	childSlug := strings.TrimSpace(r.PostFormValue("child_slug"))
	if childSlug == "" {
		refuseStep(w, r, row, "Choose a definition to add as a step")
		return
	}

	child, status, err := db.GetTaskDefinitionBySlugIncludingDeleted(r.Context(), childSlug)
	if errors.Is(err, db.ErrTaskDefinitionNotFound) || status != "" {
		refuseStep(w, r, row, "That definition does not exist: "+childSlug+" — it cannot be added to "+row.DisplayName)
		return
	}
	if err != nil {
		log.Printf("task definition %q child lookup failed: %v", row.Slug, err)
		http.Error(w, "Failed to add step", http.StatusInternalServerError)
		return
	}
	if child.ID == row.ID {
		refuseStep(w, r, row, stepSelfMessage+": "+row.DisplayName)
		return
	}

	err = db.AddDefinitionStep(r.Context(), row.ID, child.ID)
	if errors.Is(err, db.ErrDuplicateDefinitionStep) {
		refuseStep(w, r, row, stepDuplicateMessage)
		return
	}
	if err != nil {
		log.Printf("task definition %q add step %q failed: %v", row.Slug, childSlug, err)
		http.Error(w, "Failed to add step", http.StatusInternalServerError)
		return
	}
	succeedStep(w, r, row, "Step added.", "")
}

// handleTaskDefinitionStepRemove hard-deletes a step, unless another step
// waits on it.
func handleTaskDefinitionStepRemove(w http.ResponseWriter, r *http.Request) {
	row, ok := beginStepMutation(w, r)
	if !ok {
		return
	}
	steps, err := db.ListDefinitionSteps(r.Context(), row.ID)
	if err != nil {
		log.Printf("task definition %q steps load failed: %v", row.Slug, err)
		http.Error(w, "Failed to remove step", http.StatusInternalServerError)
		return
	}
	childSlug := r.PathValue("child_slug")
	step, found := findStep(steps, childSlug)
	if !found {
		refuseStep(w, r, row, "Step "+childSlug+" is not in this container")
		return
	}

	dependants, err := db.StepDependants(r.Context(), row.ID, step.ChildDefinitionID)
	if err != nil {
		log.Printf("task definition %q dependants load failed: %v", row.Slug, err)
		http.Error(w, "Failed to remove step", http.StatusInternalServerError)
		return
	}
	if len(dependants) > 0 {
		refuseStep(w, r, row, "Step "+childSlug+" is depended on by "+strings.Join(dependants, ", "))
		return
	}

	if err := db.RemoveDefinitionStep(r.Context(), row.ID, step.ChildDefinitionID); err != nil {
		log.Printf("task definition %q remove step %q failed: %v", row.Slug, childSlug, err)
		http.Error(w, "Failed to remove step", http.StatusInternalServerError)
		return
	}
	succeedStep(w, r, row, "Step removed.", "")
}

// handleTaskDefinitionStepMove swaps the step with its neighbour. First-up and
// last-down are 200 no-ops: the control should simply do nothing, not complain.
func handleTaskDefinitionStepMove(w http.ResponseWriter, r *http.Request) {
	row, ok := beginStepMutation(w, r)
	if !ok {
		return
	}
	direction := strings.TrimSpace(r.PostFormValue("direction"))
	if direction != "up" && direction != "down" {
		refuseStep(w, r, row, "Direction must be up or down")
		return
	}
	steps, err := db.ListDefinitionSteps(r.Context(), row.ID)
	if err != nil {
		log.Printf("task definition %q steps load failed: %v", row.Slug, err)
		http.Error(w, "Failed to move step", http.StatusInternalServerError)
		return
	}
	childSlug := r.PathValue("child_slug")
	step, found := findStep(steps, childSlug)
	if !found {
		refuseStep(w, r, row, "Step "+childSlug+" is not in this container")
		return
	}
	if _, err := db.MoveDefinitionStep(r.Context(), row.ID, step.ChildDefinitionID, direction); err != nil {
		log.Printf("task definition %q move step %q failed: %v", row.Slug, childSlug, err)
		http.Error(w, "Failed to move step", http.StatusInternalServerError)
		return
	}
	succeedStep(w, r, row, "", "")
}

// handleTaskDefinitionStepDepends writes the dependency edge.
func handleTaskDefinitionStepDepends(w http.ResponseWriter, r *http.Request) {
	row, ok := beginStepMutation(w, r)
	if !ok {
		return
	}
	steps, err := db.ListDefinitionSteps(r.Context(), row.ID)
	if err != nil {
		log.Printf("task definition %q steps load failed: %v", row.Slug, err)
		http.Error(w, "Failed to save dependency", http.StatusInternalServerError)
		return
	}
	childSlug := r.PathValue("child_slug")
	step, found := findStep(steps, childSlug)
	if !found {
		refuseStep(w, r, row, "Step "+childSlug+" is not in this container")
		return
	}

	reason := strings.TrimSpace(r.PostFormValue("depends_on_reason"))
	if reason != "" && !db.IsTaskClosureReason(reason) {
		refuseStep(w, r, row, stepReasonMessage)
		return
	}

	dependsOn := []string{}
	for _, slug := range r.PostForm["depends_on"] {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}
		if slug == childSlug {
			refuseStep(w, r, row, "A step cannot depend on itself")
			return
		}
		sibling, exists := findStep(steps, slug)
		if !exists {
			refuseStep(w, r, row, "Depends on must be another step in this container: "+slug)
			return
		}
		if !stepContains(dependsOn, sibling.ChildDefinitionID) {
			dependsOn = append(dependsOn, sibling.ChildDefinitionID)
		}
	}

	err = db.UpdateDefinitionStepDepends(r.Context(), row.ID, step.ChildDefinitionID, dependsOn, reason)
	if errors.Is(err, db.ErrStepDependencyCycle) {
		refuseStep(w, r, row, stepCycleMessage)
		return
	}
	if err != nil {
		log.Printf("task definition %q dependency save failed: %v", row.Slug, err)
		http.Error(w, "Failed to save dependency", http.StatusInternalServerError)
		return
	}
	succeedStep(w, r, row, "Dependency saved.", "")
}

// handleTaskDefinitionStepMapping writes the child input mapping. A value is
// stored exactly as submitted; an empty value deletes the key, which is the
// only way a mapping is removed (AC-46).
func handleTaskDefinitionStepMapping(w http.ResponseWriter, r *http.Request) {
	row, ok := beginStepMutation(w, r)
	if !ok {
		return
	}
	steps, err := db.ListDefinitionSteps(r.Context(), row.ID)
	if err != nil {
		log.Printf("task definition %q steps load failed: %v", row.Slug, err)
		http.Error(w, "Failed to save mapping", http.StatusInternalServerError)
		return
	}
	childSlug := r.PathValue("child_slug")
	step, found := findStep(steps, childSlug)
	if !found {
		refuseStep(w, r, row, "Step "+childSlug+" is not in this container")
		return
	}

	names, checked := declaredInputNames(step.ChildInputSchemaYAML)
	const prefix = "mapping__"
	submitted := make(map[string]string)
	for field, values := range r.PostForm {
		if !strings.HasPrefix(field, prefix) {
			continue
		}
		name := strings.TrimPrefix(field, prefix)
		if name == "" {
			continue
		}
		value := ""
		if len(values) > 0 {
			value = values[0]
		}
		if checked && !stepContains(names, name) {
			refuseStep(w, r, row, "That definition has no input named "+name)
			return
		}
		if value != "" {
			submitted[name] = value
		}
	}

	// Keys the form cannot show are kept, not dropped: a mapping this editor
	// inherited must survive an unrelated save.
	if checked {
		for key, value := range step.InputMapping {
			if _, present := submitted[key]; present || stepContains(names, key) {
				continue
			}
			submitted[key] = value
		}
	}

	if err := db.UpdateDefinitionStepMapping(r.Context(), row.ID, step.ChildDefinitionID, submitted); err != nil {
		log.Printf("task definition %q mapping save failed: %v", row.Slug, err)
		http.Error(w, "Failed to save mapping", http.StatusInternalServerError)
		return
	}
	warning := ""
	if !checked {
		warning = stepNoInputsWarning
	}
	succeedStep(w, r, row, "Mapping saved.", warning)
}

// buildOutputsView reads the declared outputs list. inputs: and locals: in the
// same column are read by nothing today, so they are shown read-only with that
// stated rather than implied to work.
type taskOutputsView struct {
	Outputs    []string
	Inputs     []string
	Locals     []string
	ParseError string
}

func buildOutputsView(variablesYAML string) taskOutputsView {
	declaration, err := db.ParseTaskVariableDeclarations(variablesYAML)
	if err != nil {
		return taskOutputsView{ParseError: strings.TrimPrefix(err.Error(), "read variables: ")}
	}
	return taskOutputsView{
		Outputs: declaration.Outputs,
		Inputs:  declaration.Inputs,
		Locals:  declaration.Locals,
	}
}
