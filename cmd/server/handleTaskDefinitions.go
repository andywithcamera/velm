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
	renderNewDefinitionPage(w, r, http.StatusOK, map[string]string{}, "")
}

// handleTaskDefinitionCreate inserts a definition and sends the author to its
// editor. A duplicate live slug is a 400 naming the slug, never a 500, and a
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

	submitted := map[string]string{
		"slug":         strings.TrimSpace(r.PostFormValue("slug")),
		"display_name": strings.TrimSpace(r.PostFormValue("display_name")),
		"description":  strings.TrimSpace(r.PostFormValue("description")),
	}
	isContainer := r.PostFormValue("is_container") != ""

	if reason := validateTaskDefinitionSlug(submitted["slug"]); reason != "" {
		renderNewDefinitionPage(w, r, http.StatusBadRequest, submitted, reason)
		return
	}
	if submitted["display_name"] == "" {
		renderNewDefinitionPage(w, r, http.StatusBadRequest, submitted, displayNameRequired)
		return
	}

	created, err := db.CreateTaskDefinition(r.Context(), submitted["slug"], submitted["display_name"], submitted["description"], isContainer)
	if errors.Is(err, db.ErrDuplicateTaskDefinitionSlug) {
		renderNewDefinitionPage(w, r, http.StatusBadRequest, submitted,
			"A definition with slug "+submitted["slug"]+" already exists")
		return
	}
	if err != nil {
		log.Printf("task definition create %q failed: %v", submitted["slug"], err)
		http.Error(w, "Failed to create task definition", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/task-definitions/"+created.Slug, http.StatusSeeOther)
}

func renderNewDefinitionPage(w http.ResponseWriter, r *http.Request, status int, values map[string]string, reason string) {
	displayName := values["display_name"]
	if displayName == "" {
		displayName = "New Task Definition"
	}
	viewData := newViewData(w, r, "/task-definitions/new", displayName, "Operations")
	viewData["View"] = "task-definition-new"
	viewData["NewSlug"] = values["slug"]
	viewData["NewDisplayName"] = values["display_name"]
	viewData["NewDescription"] = values["description"]
	viewData["NewIsContainer"] = false
	viewData["FormError"] = reason
	writeTaskDefinitionPage(w, "layout.html", viewData, status)
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
	viewData := newViewData(w, r, "/task-definitions/"+row.Slug, row.DisplayName, "Operations")
	viewData["View"] = "task-definition-editor"
	viewData["Definition"] = row
	viewData["BasicsError"] = reason
	writeTaskDefinitionPage(w, "layout.html", viewData, status)
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

	// Re-read the submitted values into the row so a refusal renders what the
	// author typed rather than what is still stored.
	row.DisplayName = displayName
	row.Description = description
	row.IsContainer = isContainer
	row.IsActive = isActive

	if displayName == "" {
		renderEditorPage(w, r, row, http.StatusBadRequest, displayNameRequired)
		return
	}
	if !isContainer && row.ChildCount > 0 {
		// A leaf does no work itself, so its steps would silently never run.
		renderEditorPage(w, r, row, http.StatusBadRequest, "Remove the "+pluralSteps(row.ChildCount)+" first")
		return
	}

	if err := db.UpdateTaskDefinitionBasics(r.Context(), row.Slug, displayName, description, isContainer, isActive); err != nil {
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
