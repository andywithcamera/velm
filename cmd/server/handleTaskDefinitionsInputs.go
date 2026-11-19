package main

// Task definition inputs designer (#358, slice 2).
//
// input_schema_yaml is the requester's form, and until now it could only be
// written by hand. This section edits it as rows — one row per declared input,
// in the order the YAML declares them — and keeps a raw YAML tab as the escape
// hatch for anything the row editor cannot represent.
//
// Two rules shape the design:
//
//  1. Rows are a DOM edit, not a write. "Add row" fetches a server-rendered
//     fragment that writes nothing; remove and move reorder the DOM. Exactly one
//     route saves the whole section, so a half-built form is never persisted.
//  2. The form never rewrites a document it cannot read losslessly. The gate
//     lives in db.InspectTaskInputSchema and is the only copy of the rule; this
//     file asks it, it does not repeat it.

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"velm/internal/db"
)

// taskInputDataTypeOptions is the set the designer offers: exactly the types
// the catalog can render without scripting (InputControlKind and InputHTMLType,
// TaskInputSchema.go:55-84). reference:* and code:* are readable but not
// offered; the raw tab is where they belong.
var taskInputDataTypeOptions = []string{
	"text", "long_text", "richtext", "number", "integer", "decimal",
	"date", "datetime", "email", "url", "boolean", "choice",
}

// taskInputReservedNames are the three input names the engine reads for itself.
// Flagged where they are typed, with the effect, and never blocked.
var taskInputReservedNames = map[string]string{
	"body":         "Folded into the instance description",
	"priority":     "Sets the task priority, falling back to the default when not one of the allowed values",
	"window_start": "Parsed as the planned start; an unparseable value is ignored",
}

// taskInputTokenPattern keeps row tokens usable inside form field names.
var taskInputTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

var taskInputRowCounter atomic.Uint64

// newTaskInputRowToken names a row the DOM has just added. Rows loaded from the
// stored YAML are named r0, r1, … by position; a new one must not collide with
// those.
func newTaskInputRowToken() string {
	return fmt.Sprintf("new-%d-%d", time.Now().UnixNano(), taskInputRowCounter.Add(1))
}

func taskInputDataTypeOffered(dataType string) bool {
	for _, option := range taskInputDataTypeOptions {
		if option == dataType {
			return true
		}
	}
	return false
}

func taskInputIsBoolean(dataType string) bool {
	return strings.EqualFold(strings.TrimSpace(dataType), "boolean")
}

// taskInputRowView is one row of the designer, ready to render.
type taskInputRowView struct {
	Token                string
	Position             int
	Field                db.TaskInputField
	Required             bool
	RequiredOffered      bool
	TypeOffered          bool
	DefaultIsBoolean     bool
	StoredDefaultUnusual bool
	Types                []string
	Reserved             map[string]string
	Error                string
}

// taskInputsView is the whole inputs section. Offered is false when the stored
// document cannot be read losslessly; Reason then says why, and RawYAML is the
// only editing surface.
type taskInputsView struct {
	Offered   bool
	Reason    string
	Rows      []taskInputRowView
	RawYAML   string
	RawError  string
	RawNotice string
	Error     string
	Notice    string
	Reserved  map[string]string
}

// buildTaskInputsView loads the gate's verdict and the rows it authorises.
func buildTaskInputsView(row db.TaskDefinitionRow) taskInputsView {
	view := taskInputsView{RawYAML: row.InputSchemaYAML, Reserved: taskInputReservedNames}
	inspection := db.InspectTaskInputSchema(row.InputSchemaYAML)
	if !inspection.Lossless {
		view.Reason = inspection.Reason
		return view
	}
	view.Offered = true
	for index, field := range inspection.Fields {
		view.Rows = append(view.Rows, taskInputRowView{
			Token:            "r" + strconv.Itoa(index),
			Position:         index + 1,
			Field:            field,
			Required:         !field.IsNullable,
			RequiredOffered:  !taskInputIsBoolean(field.DataType),
			TypeOffered:      taskInputDataTypeOffered(field.DataType),
			DefaultIsBoolean: taskInputIsBoolean(field.DataType),
			StoredDefaultUnusual: taskInputIsBoolean(field.DataType) &&
				field.DefaultValue != "" && field.DefaultValue != "yes" && field.DefaultValue != "no",
			Types:    taskInputDataTypeOptions,
			Reserved: taskInputReservedNames,
		})
	}
	return view
}

func newTaskInputRowView() taskInputRowView {
	return taskInputRowView{
		Token:           newTaskInputRowToken(),
		Field:           db.TaskInputField{DataType: "text", IsNullable: false},
		Required:        true,
		RequiredOffered: true,
		TypeOffered:     true,
		Types:           taskInputDataTypeOptions,
		Reserved:        taskInputReservedNames,
	}
}

// renderEditorWithInputs renders the whole editor against the submitted inputs
// section. A refusal therefore keeps the author's rows on screen.
func renderEditorWithInputs(w http.ResponseWriter, r *http.Request, row db.TaskDefinitionRow, status int, inputs taskInputsView) {
	viewData, ok := editorViewData(w, r, row)
	if !ok {
		return
	}
	viewData["Inputs"] = inputs
	writeTaskDefinitionPage(w, "layout.html", viewData, status)
}

// handleTaskDefinitionInputRowFragment answers "Add row". It is a GET that
// writes nothing: a reload before saving shows the unmodified document, so the
// row editor stays available.
func handleTaskDefinitionInputRowFragment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	row, ok := loadDefinitionForRequest(w, r)
	if !ok {
		return
	}
	writeTaskInputRowFragment(w, row.Slug)
}

// writeTaskInputRowFragment is the one place "Add row" is rendered, shared by
// the definition editor and the new-definition flow (which has no slug yet).
func writeTaskInputRowFragment(w http.ResponseWriter, label string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "task-definition-input-row", newTaskInputRowView()); err != nil {
		log.Printf("task definition input row fragment failed for %q: %v", label, err)
		http.Error(w, "Failed to render input row", http.StatusInternalServerError)
	}
}

// handleNewDefinitionInputRowFragment answers "Add row" on the new-definition
// page, before a definition exists to address.
func handleNewDefinitionInputRowFragment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeTaskInputRowFragment(w, "new definition")
}

// submittedInputRow is one row as the form sent it, already trimmed.
type submittedInputRow struct {
	Token        string
	OriginalName string
	Name         string
	Label        string
	DataType     string
	DefaultValue string
	HelpText     string
	Required     bool
	Choices      []db.ChoiceOption
}

// parseSubmittedInputRows rebuilds the rows in document order. Order is carried
// by a hidden row_order input inside each row, because a form map is unordered
// and the DOM is the authority on form order.
func parseSubmittedInputRows(r *http.Request) ([]submittedInputRow, string) {
	tokens := r.PostForm["row_order"]
	if len(tokens) == 0 {
		return nil, "This form has no input rows to save"
	}
	seen := make(map[string]bool, len(tokens))
	rows := make([]submittedInputRow, 0, len(tokens))
	for _, token := range tokens {
		if !taskInputTokenPattern.MatchString(token) || seen[token] {
			return nil, "This form is not a valid input list; reload the page and try again"
		}
		seen[token] = true
		suffix := "__" + token
		choices := submittedChoices(r, token)
		rows = append(rows, submittedInputRow{
			Token:        token,
			OriginalName: strings.TrimSpace(r.PostFormValue("original_name" + suffix)),
			Name:         strings.TrimSpace(r.PostFormValue("name" + suffix)),
			Label:        strings.TrimSpace(r.PostFormValue("label" + suffix)),
			DataType:     strings.TrimSpace(r.PostFormValue("data_type" + suffix)),
			DefaultValue: r.PostFormValue("default_value" + suffix),
			HelpText:     strings.TrimSpace(r.PostFormValue("help_text" + suffix)),
			Required:     taskInputCheckboxTicked(r.PostForm["required"+suffix]),
			Choices:      choices,
		})
	}
	return rows, ""
}

func taskInputCheckboxTicked(values []string) bool {
	for _, value := range values {
		if value == "yes" {
			return true
		}
	}
	return false
}

func submittedChoices(r *http.Request, token string) []db.ChoiceOption {
	values := r.PostForm["choice_value__"+token]
	labels := r.PostForm["choice_label__"+token]
	choices := make([]db.ChoiceOption, 0, len(values))
	for index, value := range values {
		label := ""
		if index < len(labels) {
			label = labels[index]
		}
		choices = append(choices, db.ChoiceOption{
			Value: strings.TrimSpace(value),
			Label: strings.TrimSpace(label),
		})
	}
	return choices
}

// buildTaskInputFields turns submitted rows into the struct the writer marshals.
// It enforces the two value rules that are not the parser's: a boolean default
// is yes/no (the catalog only ticks "yes", task-catalog-view.html:282), and a
// choice pair needs a value. Name, identifier and duplicate rules stay the
// parser's alone.
func buildTaskInputFields(rows []submittedInputRow, stored map[string]db.TaskInputField) ([]db.TaskInputField, int, string) {
	fields := make([]db.TaskInputField, 0, len(rows))
	for index, row := range rows {
		dataType := row.DataType
		if dataType == "" {
			dataType = "text"
		}
		if taskInputIsBoolean(dataType) {
			defaultValue := strings.TrimSpace(row.DefaultValue)
			storedField, known := stored[row.OriginalName]
			preserved := known && taskInputIsBoolean(storedField.DataType) &&
				defaultValue == strings.TrimSpace(storedField.DefaultValue)
			if defaultValue != "" && defaultValue != "yes" && defaultValue != "no" && !preserved {
				return nil, index, "A boolean default must be yes or no"
			}
			row.DefaultValue = defaultValue
		}
		if dataType == "choice" {
			for _, choice := range row.Choices {
				if choice.Value == "" {
					return nil, index, "A choice needs a value"
				}
			}
		}

		isNullable := !row.Required
		if taskInputIsBoolean(dataType) {
			// Required is not offered on a boolean, so a missing checkbox means
			// nothing: keep what the definition already declared.
			if storedField, known := stored[row.OriginalName]; known && taskInputIsBoolean(storedField.DataType) {
				isNullable = storedField.IsNullable
			}
		}

		choices := row.Choices
		if dataType != "choice" {
			// A type change away from choice must not silently delete pairs the
			// author cannot see any more; keep what is stored.
			if storedField, known := stored[row.OriginalName]; known {
				choices = storedField.Choices
			} else {
				choices = nil
			}
		}

		fields = append(fields, db.TaskInputField{
			Name:         row.Name,
			Label:        row.Label,
			DataType:     dataType,
			IsNullable:   isNullable,
			DefaultValue: strings.TrimSpace(row.DefaultValue),
			HelpText:     row.HelpText,
			Choices:      choices,
		})
	}
	return fields, -1, ""
}

var (
	taskInputNoNamePattern    = regexp.MustCompile(`field (\d+) has no name`)
	taskInputQuotedPattern    = regexp.MustCompile(`"([^"]+)"`)
	taskInputDuplicatePattern = regexp.MustCompile(`declares "([^"]+)" twice`)
)

// attributeInputSchemaError finds the row an error message is about, so the
// message appears where the author is typing. It only locates; the parser
// decided.
func attributeInputSchemaError(message string, rows []submittedInputRow) int {
	if match := taskInputNoNamePattern.FindStringSubmatch(message); match != nil {
		position, err := strconv.Atoi(match[1])
		if err == nil && position >= 1 && position <= len(rows) {
			return position - 1
		}
		return -1
	}
	if match := taskInputDuplicatePattern.FindStringSubmatch(message); match != nil {
		// The second row is the one that introduced the duplicate.
		for index := len(rows) - 1; index >= 0; index-- {
			if rows[index].Name == match[1] {
				return index
			}
		}
		return -1
	}
	if match := taskInputQuotedPattern.FindStringSubmatch(message); match != nil {
		for index, row := range rows {
			if row.Name == match[1] {
				return index
			}
		}
	}
	return -1
}

// rowsToViews renders submitted rows back to the author after a refusal, in the
// same order and with their typing intact.
func rowsToViews(rows []submittedInputRow, message string, errorIndex int) []taskInputRowView {
	views := make([]taskInputRowView, 0, len(rows))
	for index, row := range rows {
		view := taskInputRowView{
			Token:            row.Token,
			Position:         index + 1,
			Field:            db.TaskInputField{Name: row.Name, Label: row.Label, DataType: row.DataType, IsNullable: !row.Required, DefaultValue: row.DefaultValue, HelpText: row.HelpText, Choices: row.Choices},
			Required:         row.Required,
			RequiredOffered:  !taskInputIsBoolean(row.DataType),
			TypeOffered:      taskInputDataTypeOffered(row.DataType),
			DefaultIsBoolean: taskInputIsBoolean(row.DataType),
			StoredDefaultUnusual: taskInputIsBoolean(row.DataType) &&
				row.DefaultValue != "" && row.DefaultValue != "yes" && row.DefaultValue != "no",
			Types:    taskInputDataTypeOptions,
			Reserved: taskInputReservedNames,
		}
		if row.DataType == "" {
			view.Field.DataType = "text"
		}
		if index == errorIndex {
			view.Error = message
		}
		views = append(views, view)
	}
	return views
}

// handleTaskDefinitionInputsSave writes the whole inputs section in one go: the
// form is assembled into YAML, the real parser validates it, and only then is
// the column written (AC-22, AC-33).
func handleTaskDefinitionInputsSave(w http.ResponseWriter, r *http.Request) {
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

	inspection := db.InspectTaskInputSchema(row.InputSchemaYAML)
	if !inspection.Lossless {
		// The row editor was not offered, so this save cannot be trusted: refuse
		// and leave the column byte-identical (AC-34).
		view := buildTaskInputsView(row)
		view.Error = inspection.Reason
		renderEditorWithInputs(w, r, row, http.StatusBadRequest, view)
		return
	}

	submitted, refusal := parseSubmittedInputRows(r)
	if refusal != "" {
		view := buildTaskInputsView(row)
		view.Error = refusal
		renderEditorWithInputs(w, r, row, http.StatusBadRequest, view)
		return
	}

	stored := make(map[string]db.TaskInputField, len(inspection.Fields))
	for _, field := range inspection.Fields {
		stored[field.Name] = field
	}

	fields, errorIndex, valueRefusal := buildTaskInputFields(submitted, stored)
	if valueRefusal != "" {
		view := buildTaskInputsView(row)
		view.Rows = rowsToViews(submitted, valueRefusal, errorIndex)
		view.Notice = "Nothing was saved."
		renderEditorWithInputs(w, r, row, http.StatusBadRequest, view)
		return
	}

	assembled, err := db.MarshalTaskInputSchema(fields)
	if err != nil {
		log.Printf("task definition %q input schema marshal failed: %v", row.Slug, err)
		http.Error(w, "Failed to save inputs", http.StatusInternalServerError)
		return
	}

	// One validator: the form assembles YAML and asks the real parser (AC-33).
	if _, err := db.ParseTaskInputSchema(assembled); err != nil {
		message := strings.TrimPrefix(err.Error(), "parse input schema: ")
		view := buildTaskInputsView(row)
		view.Rows = rowsToViews(submitted, message, attributeInputSchemaError(message, submitted))
		view.Error = message
		view.Notice = "Nothing was saved."
		renderEditorWithInputs(w, r, row, http.StatusBadRequest, view)
		return
	}

	// The form's own output must pass the gate, or the next load would disable
	// the editor (AC-36).
	if verdict := db.InspectTaskInputSchema(assembled); !verdict.Lossless {
		log.Printf("task definition %q form output is not lossless: %s", row.Slug, verdict.Reason)
		http.Error(w, "Failed to save inputs", http.StatusInternalServerError)
		return
	}

	if err := db.UpdateTaskDefinitionInputSchema(r.Context(), row.Slug, assembled); err != nil {
		if errors.Is(err, db.ErrTaskDefinitionNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("task definition %q input schema update failed: %v", row.Slug, err)
		http.Error(w, "Failed to save inputs", http.StatusInternalServerError)
		return
	}

	row, err = db.GetTaskDefinitionBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		log.Printf("task definition %q reload failed: %v", row.Slug, err)
		http.Error(w, "Failed to load task definition", http.StatusInternalServerError)
		return
	}
	view := buildTaskInputsView(row)
	view.Notice = "Inputs saved."
	renderEditorWithInputs(w, r, row, http.StatusOK, view)
}

// handleTaskDefinitionRawSave writes the raw tab exactly as typed. It is the
// escape hatch: ParseTaskInputSchema alone validates it, and the lossless gate
// re-runs on the next load, never on the save itself (AC-37).
func handleTaskDefinitionRawSave(w http.ResponseWriter, r *http.Request) {
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

	raw := r.PostFormValue("input_schema_yaml")
	if _, err := db.ParseTaskInputSchema(raw); err != nil {
		message := strings.TrimPrefix(err.Error(), "parse input schema: ")
		view := buildTaskInputsView(row)
		view.RawYAML = raw
		view.RawError = "does not parse: " + message
		view.Notice = "Nothing was saved."
		renderEditorWithInputs(w, r, row, http.StatusBadRequest, view)
		return
	}

	if err := db.UpdateTaskDefinitionInputSchema(r.Context(), row.Slug, raw); err != nil {
		if errors.Is(err, db.ErrTaskDefinitionNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("task definition %q raw schema update failed: %v", row.Slug, err)
		http.Error(w, "Failed to save raw YAML", http.StatusInternalServerError)
		return
	}

	row, err := db.GetTaskDefinitionBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		log.Printf("task definition %q reload failed: %v", row.Slug, err)
		http.Error(w, "Failed to load task definition", http.StatusInternalServerError)
		return
	}
	view := buildTaskInputsView(row)
	view.RawNotice = "Raw YAML saved."
	renderEditorWithInputs(w, r, row, http.StatusOK, view)
}
