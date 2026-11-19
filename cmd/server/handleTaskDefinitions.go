package main

// Task definition editor, slice 1: the list, creating a definition, and the
// basics section of its editor.
//
// A definition is a function: it declares its own inputs and behaves the same
// whatever calls it. The list is where an author finds one, /task-definitions/new
// is where one starts, and the editor's basics section is the part every
// definition has (what it is called, what it does, whether it composes steps and
// whether it may be spawned). Inputs and steps have their own sections in later
// slices; the slug is shown read-only because it is the stable reference other
// definitions map against.

import (
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"velm/internal/db"
)

// taskDefinitionSlugPattern is deliberately not db.IsSafeIdentifier: the column's
// own examples are hyphenated ('reboot-server', 076:20) and the identifier rule
// rejects hyphens.
var taskDefinitionSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

const taskDefinitionSlugMaxLength = 60

const (
	slugInvalidMessage  = "Slug must be lowercase letters, digits and hyphens, starting with a letter or digit"
	slugTooLongMessage  = "Slug must be 60 characters or fewer"
	displayNameRequired = "Display name is required"
)

// validateTaskDefinitionSlug returns a user-facing refusal, or "" when the slug
// is acceptable.
func validateTaskDefinitionSlug(slug string) string {
	if len(slug) > taskDefinitionSlugMaxLength {
		return slugTooLongMessage
	}
	if !taskDefinitionSlugPattern.MatchString(slug) {
		return slugInvalidMessage
	}
	return ""
}

// handleTaskDefinitionsPage renders the definitions list. Search is applied in
// SQL so the page does not load every definition to filter it in memory.
func handleTaskDefinitionsPage(w http.ResponseWriter, r *http.Request) {
	if db.Pool == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := db.ListTaskDefinitionRows(r.Context(), query)
	if err != nil {
		log.Printf("task definitions list failed: %v", err)
		http.Error(w, "Failed to load task definitions", http.StatusInternalServerError)
		return
	}

	viewData := newViewData(w, r, "/task-definitions", "Task Definitions", "Operations")
	viewData["View"] = "task-definitions"
	viewData["DefinitionRows"] = rows
	viewData["DefinitionQuery"] = query
	viewData["DefinitionTotal"] = len(rows)
	if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
		http.Error(w, "Failed to render task definitions", http.StatusInternalServerError)
	}
}

// handleTaskDefinitionNewPage renders the create form. It writes nothing.
func handleTaskDefinitionNewPage(w http.ResponseWriter, r *http.Request) {
	if db.Pool == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}
	renderNewDefinitionPage(w, r, http.StatusOK, newDefinitionForm{IsActive: true}, taskInputsView{}, "")
}

// newDefinitionForm is everything the one-page flow collects before the
// definition exists. It is held so a refusal re-renders what the author typed
// rather than an empty form.
type newDefinitionForm struct {
	Slug        string
	DisplayName string
	Description string
	IsActive    bool
	GroupID     string
	Strategy    string
	NamedUserID string
}

// namedUserOption is one member offered by the named-user field. GroupIDs is a
// space-separated list so the client can scope the options to the chosen
// assignment group without a round trip.
type namedUserOption struct {
	ID       string
	Name     string
	Email    string
	GroupIDs string
}

// newDefinitionPage is the view behind the one-page flow: basics, then
// assignment, then the named user when the strategy asks for one, then the
// inputs designer, then the link to the canvas.
type newDefinitionPage struct {
	Form             newDefinitionForm
	Reason           string
	Groups           []db.AuthzGroup
	Strategies       []string
	Users            []namedUserOption
	Inputs           taskInputsView
	DescriptionField markdownFieldView
}

// buildNewDefinitionPage loads the choices the flow offers. Every read is a
// page-level query, not a per-row one.
func buildNewDefinitionPage(r *http.Request, form newDefinitionForm, inputs taskInputsView, reason string) (newDefinitionPage, error) {
	page := newDefinitionPage{
		Form:       form,
		Reason:     reason,
		Groups:     []db.AuthzGroup{},
		Strategies: db.AssignmentStrategies,
		Users:      []namedUserOption{},
		Inputs:     inputs,
		DescriptionField: markdownFieldView{
			ID:          "new-definition-description",
			Name:        "description",
			Value:       form.Description,
			Placeholder: "What this definition is for. Markdown; [[ links work.",
			AutoExpand:  true,
		},
	}
	groups, err := db.ListAuthzGroups(r.Context())
	if err != nil {
		return page, err
	}
	page.Groups = groups
	users, err := db.ListAuthzUsers(r.Context())
	if err != nil {
		return page, err
	}
	memberships, err := db.ListAuthzGroupMemberships(r.Context())
	if err != nil {
		return page, err
	}
	byUser := make(map[string][]string, len(users))
	for _, membership := range memberships {
		byUser[membership.UserID] = append(byUser[membership.UserID], membership.GroupID)
	}
	for _, user := range users {
		page.Users = append(page.Users, namedUserOption{
			ID:       user.ID,
			Name:     user.Name,
			Email:    user.Email,
			GroupIDs: strings.Join(byUser[user.ID], " "),
		})
	}
	if page.Inputs.Reserved == nil {
		page.Inputs.Reserved = taskInputReservedNames
	}
	page.Inputs.Offered = true
	return page, nil
}

func renderNewDefinitionPage(w http.ResponseWriter, r *http.Request, status int, form newDefinitionForm, inputs taskInputsView, reason string) {
	page, err := buildNewDefinitionPage(r, form, inputs, reason)
	if err != nil {
		log.Printf("new task definition page load failed: %v", err)
		http.Error(w, "Failed to load the new definition form", http.StatusInternalServerError)
		return
	}
	displayName := form.DisplayName
	if displayName == "" {
		displayName = "New Task Definition"
	}
	viewData := newViewData(w, r, "/task-definitions/new", displayName, "Operations")
	viewData["View"] = "task-definition-new"
	viewData["NewDefinition"] = page
	writeTaskDefinitionPage(w, "layout.html", viewData, status)
}

// handleTaskDefinitionCreate inserts a definition from the one-page flow: the
// four basics, the assignment choice (including a named user), and the requester
// form authored before the definition existed. It then sends the author to the
// canvas, where the Start trigger is already present (Design Vision D6).
// A duplicate live slug is a 400 naming the slug, never a 500, and a
// soft-deleted definition's slug is free (094).
func handleTaskDefinitionCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if db.Pool == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}

	form := newDefinitionForm{
		Slug:        strings.TrimSpace(r.PostFormValue("slug")),
		DisplayName: strings.TrimSpace(r.PostFormValue("display_name")),
		Description: strings.TrimSpace(r.PostFormValue("description")),
		IsActive:    r.PostFormValue("is_active") != "",
		GroupID:     strings.TrimSpace(r.PostFormValue("default_group_id")),
		Strategy:    strings.TrimSpace(r.PostFormValue("assignment_strategy")),
		NamedUserID: strings.TrimSpace(r.PostFormValue("default_assignee_id")),
	}
	isContainer := r.PostFormValue("is_container") != ""

	if reason := validateTaskDefinitionSlug(form.Slug); reason != "" {
		renderNewDefinitionPage(w, r, http.StatusBadRequest, form, taskInputsView{}, reason)
		return
	}
	if form.DisplayName == "" {
		renderNewDefinitionPage(w, r, http.StatusBadRequest, form, taskInputsView{}, displayNameRequired)
		return
	}
	if form.Strategy != "" && !db.IsAssignmentStrategy(form.Strategy) {
		renderNewDefinitionPage(w, r, http.StatusBadRequest, form, taskInputsView{},
			"Assignment strategy must be one of unassigned, named_user, requester, from_input, random_member or round_robin")
		return
	}
	if form.GroupID != "" {
		exists, err := db.GroupExists(r.Context(), form.GroupID)
		if err != nil {
			log.Printf("task definition create %q group check failed: %v", form.Slug, err)
			http.Error(w, "Failed to create task definition", http.StatusInternalServerError)
			return
		}
		if !exists {
			renderNewDefinitionPage(w, r, http.StatusBadRequest, form, taskInputsView{}, "Assignment group does not exist")
			return
		}
	}

	// The named user is scoped to the assignment group: without a group there
	// is nothing to scope to, so the strategy cannot be answered.
	if form.Strategy == db.AssignmentNamedUser {
		if form.GroupID == "" {
			form.NamedUserID = ""
			renderNewDefinitionPage(w, r, http.StatusBadRequest, form, taskInputsView{},
				"Choose an assignment group before naming a user")
			return
		}
		if form.NamedUserID == "" {
			renderNewDefinitionPage(w, r, http.StatusBadRequest, form, taskInputsView{},
				"A named user strategy needs a user")
			return
		}
		member, err := db.UserIsGroupMember(r.Context(), form.NamedUserID, form.GroupID)
		if err != nil {
			log.Printf("task definition create %q membership check failed: %v", form.Slug, err)
			http.Error(w, "Failed to create task definition", http.StatusInternalServerError)
			return
		}
		if !member {
			// The group moved away from the user; the form offers no stale
			// selection to submit again (AC-8s-3).
			form.NamedUserID = ""
			renderNewDefinitionPage(w, r, http.StatusBadRequest, form, taskInputsView{},
				"That user is not a member of the chosen assignment group")
			return
		}
	} else {
		// Only a named_user strategy stores a person.
		form.NamedUserID = ""
	}

	schema, inputsView, reason := taskInputSchemaFromSubmittedRows(r)
	if reason != "" {
		renderNewDefinitionPage(w, r, http.StatusBadRequest, form, inputsView, reason)
		return
	}

	created, err := db.CreateTaskDefinitionWithSetup(r.Context(), db.CreateTaskDefinitionSetup{
		Slug:               form.Slug,
		DisplayName:        form.DisplayName,
		Description:        form.Description,
		IsContainer:        isContainer,
		IsActive:           form.IsActive,
		DefaultGroupID:     form.GroupID,
		AssignmentStrategy: form.Strategy,
		NamedUserID:        form.NamedUserID,
		InputSchemaYAML:    schema,
	})
	if errors.Is(err, db.ErrDuplicateTaskDefinitionSlug) {
		renderNewDefinitionPage(w, r, http.StatusBadRequest, form, taskInputsView{},
			"A definition with slug "+form.Slug+" already exists")
		return
	}
	if err != nil {
		log.Printf("task definition create %q failed: %v", form.Slug, err)
		http.Error(w, "Failed to create task definition", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/task-definitions/"+created.Slug, http.StatusSeeOther)
}

// taskInputSchemaFromSubmittedRows turns the flow's input rows into the column
// the editor reads. No rows is an empty schema, not a refusal: inputs are
// optional. Everything else goes through the editor's own row parser,
// assembler and validators, so there is one set of rules.
func taskInputSchemaFromSubmittedRows(r *http.Request) (string, taskInputsView, string) {
	view := taskInputsView{Offered: true, Reserved: taskInputReservedNames}
	if len(r.PostForm["row_order"]) == 0 {
		return "", view, ""
	}
	submitted, refusal := parseSubmittedInputRows(r)
	if refusal != "" {
		view.Error = refusal
		return "", view, refusal
	}
	fields, errorIndex, valueRefusal := buildTaskInputFields(submitted, nil)
	if valueRefusal != "" {
		view.Rows = rowsToViews(submitted, valueRefusal, errorIndex)
		view.Notice = "Nothing was saved."
		return "", view, valueRefusal
	}
	assembled, err := db.MarshalTaskInputSchema(fields)
	if err != nil {
		return "", view, "The inputs could not be assembled"
	}
	if _, err := db.ParseTaskInputSchema(assembled); err != nil {
		message := strings.TrimPrefix(err.Error(), "parse input schema: ")
		view.Rows = rowsToViews(submitted, message, attributeInputSchemaError(message, submitted))
		view.Error = message
		view.Notice = "Nothing was saved."
		return "", view, message
	}
	return assembled, view, ""
}

// loadDefinitionForRequest resolves the {slug} path value, writing the 404
// itself when the definition is absent or soft-deleted.
func loadDefinitionForRequest(w http.ResponseWriter, r *http.Request) (db.TaskDefinitionRow, bool) {
	if db.Pool == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return db.TaskDefinitionRow{}, false
	}
	row, err := db.GetTaskDefinitionBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, db.ErrTaskDefinitionNotFound) {
		http.NotFound(w, r)
		return db.TaskDefinitionRow{}, false
	}
	if err != nil {
		log.Printf("task definition %q lookup failed: %v", r.PathValue("slug"), err)
		http.Error(w, "Failed to load task definition", http.StatusInternalServerError)
		return db.TaskDefinitionRow{}, false
	}
	return row, true
}

// handleTaskDefinitionEditor renders one definition's editor.
func handleTaskDefinitionEditor(w http.ResponseWriter, r *http.Request) {
	row, ok := loadDefinitionForRequest(w, r)
	if !ok {
		return
	}
	renderEditorPage(w, r, row, http.StatusOK, "")
}

func renderEditorPage(w http.ResponseWriter, r *http.Request, row db.TaskDefinitionRow, status int, reason string) {
	viewData, ok := editorViewData(w, r, row)
	if !ok {
		return
	}
	viewData["BasicsError"] = reason
	writeTaskDefinitionPage(w, "layout.html", viewData, status)
}

// editorViewData builds the sections every editor render needs. It may write:
// the document loader adds a missing trigger (Design Vision D6).
func editorViewData(w http.ResponseWriter, r *http.Request, row db.TaskDefinitionRow) (map[string]any, bool) {
	return buildEditorViewData(w, r, row, true)
}

// buildEditorViewData builds the sections every editor render needs: basics,
// inputs, outputs, the stepper and the canvas. The stepper is a database read,
// so it can fail; the 500 is written here rather than halfway through a
// template. backfill selects whether a missing trigger is written, which a
// refusal must not do (Requirements AC-12).
func buildEditorViewData(w http.ResponseWriter, r *http.Request, row db.TaskDefinitionRow, backfill bool) (map[string]any, bool) {
	steps, err := buildStepsView(r.Context(), row)
	if err != nil {
		log.Printf("task definition %q steps load failed: %v", row.Slug, err)
		http.Error(w, "Failed to load task definition", http.StatusInternalServerError)
		return nil, false
	}
	viewData := newViewData(w, r, "/task-definitions/"+row.Slug, row.DisplayName, "Operations")
	viewData["View"] = "task-definition-editor"
	viewData["Definition"] = row
	viewData["Inputs"] = buildTaskInputsView(row)
	viewData["InputTypes"] = taskInputDataTypeOptions
	viewData["Outputs"] = buildOutputsView(row.VariablesYAML)
	stampStepTokens(&steps, viewData["CSRFToken"].(string))
	viewData["Steps"] = steps

	// One document behind all three views (Requirements AC-69, AC-70). The
	// rows view keeps its v1 stepper; canvas and raw read this tree.
	var blocks []db.DocumentBlock
	var blockErr error
	if backfill {
		blocks, blockErr = db.LoadDefinitionDocument(r.Context(), row.ID)
	} else {
		blocks, blockErr = db.ReadStoredDefinitionDocument(r.Context(), row.ID)
	}
	if blockErr != nil {
		log.Printf("task definition %q document load failed: %v", row.Slug, blockErr)
		http.Error(w, "Failed to load task definition", http.StatusInternalServerError)
		return nil, false
	}
	// The canvas is the only authoring view (Design Vision D1). Rows and raw
	// stay reachable by URL as support surfaces for a human rescuing a
	// definition the canvas cannot render; they are unlinked from the header.
	editorView := strings.TrimSpace(r.URL.Query().Get("view"))
	switch editorView {
	case "rows", "raw":
	default:
		editorView = "canvas"
	}
	groups, err := db.ListAuthzGroups(r.Context())
	if err != nil {
		log.Printf("task definition %q group list failed: %v", row.Slug, err)
		http.Error(w, "Failed to load task definition", http.StatusInternalServerError)
		return nil, false
	}
	canvas, err := buildCanvasView(r.Context(), row, blocks, strings.TrimSpace(r.URL.Query().Get("block")), groups, isAdminRequest(r))
	if err != nil {
		log.Printf("task definition %q canvas load failed: %v", row.Slug, err)
		http.Error(w, "Failed to load task definition", http.StatusInternalServerError)
		return nil, false
	}
	viewData["EditorView"] = editorView
	viewData["Canvas"] = canvas
	viewData["RawDocument"] = canvas.Document
	viewData["Groups"] = groups
	viewData["AssignmentStrategies"] = db.AssignmentStrategies
	return viewData, true
}

func writeTaskDefinitionPage(w http.ResponseWriter, name string, viewData map[string]any, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := templates.ExecuteTemplate(w, name, viewData); err != nil {
		log.Printf("task definition page render failed: %v", err)
	}
}

// handleTaskDefinitionBasicsSave writes the four editable basics columns. It
// answers with the editor rendered against the saved row, so a reload — and the
// response itself — show the submitted values.
func handleTaskDefinitionBasicsSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if db.Pool == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}

	row, ok := loadDefinitionForRequest(w, r)
	if !ok {
		return
	}

	displayName := strings.TrimSpace(r.PostFormValue("display_name"))
	description := strings.TrimSpace(r.PostFormValue("description"))
	isContainer := r.PostFormValue("is_container") != ""
	isActive := r.PostFormValue("is_active") != ""
	defaultGroupID := strings.TrimSpace(r.PostFormValue("default_group_id"))
	assignmentStrategy := strings.TrimSpace(r.PostFormValue("assignment_strategy"))

	// Re-read the submitted values into the row so a refusal renders what the
	// author typed rather than what is still stored.
	row.DisplayName = displayName
	row.Description = description
	row.IsContainer = isContainer
	row.IsActive = isActive
	row.DefaultGroupID = defaultGroupID
	row.AssignmentStrategy = assignmentStrategy

	if displayName == "" {
		renderEditorPage(w, r, row, http.StatusBadRequest, displayNameRequired)
		return
	}
	if assignmentStrategy != "" && !db.IsAssignmentStrategy(assignmentStrategy) {
		renderEditorPage(w, r, row, http.StatusBadRequest,
			"Assignment strategy must be one of unassigned, named_user, requester, from_input, random_member or round_robin")
		return
	}
	if defaultGroupID != "" {
		exists, err := db.GroupExists(r.Context(), defaultGroupID)
		if err != nil {
			log.Printf("task definition %q group check failed: %v", row.Slug, err)
			http.Error(w, "Failed to save task definition", http.StatusInternalServerError)
			return
		}
		if !exists {
			renderEditorPage(w, r, row, http.StatusBadRequest, "Assignment group does not exist")
			return
		}
	}
	if !isContainer && row.ChildCount > 0 {
		// A leaf does no work itself, so its steps would silently never run.
		renderEditorPage(w, r, row, http.StatusBadRequest, "Remove the "+pluralSteps(row.ChildCount)+" first")
		return
	}

	if err := db.UpdateTaskDefinitionBasics(r.Context(), row.Slug, displayName, description, isContainer, isActive, defaultGroupID, assignmentStrategy); err != nil {
		if errors.Is(err, db.ErrTaskDefinitionNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("task definition %q basics update failed: %v", row.Slug, err)
		http.Error(w, "Failed to save task definition", http.StatusInternalServerError)
		return
	}

	row, err := db.GetTaskDefinitionBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		log.Printf("task definition %q reload failed: %v", r.PathValue("slug"), err)
		http.Error(w, "Failed to load task definition", http.StatusInternalServerError)
		return
	}
	renderEditorPage(w, r, row, http.StatusOK, "")
}

// pluralSteps renders "1 step" / "3 steps" so a refusal reads as English.
func pluralSteps(count int) string {
	if count == 1 {
		return "1 step"
	}
	return strconv.Itoa(count) + " steps"
}
