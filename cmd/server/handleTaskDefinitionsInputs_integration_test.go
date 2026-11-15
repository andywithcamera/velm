package main

// Integration tests for slice 2 of the task definition editor: the inputs
// designer. Gated on TEST_DATABASE_URL like every other real-Postgres test here;
// a skip means the environment is wrong, not that the code is fine.

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"velm/internal/db"
)

const tdeInputsRich = `- name: server
  label: Server
  data_type: text
  is_nullable: false
- name: reason
  label: Reason
  data_type: long_text
  is_nullable: true
  default_value: because
- name: priority
  data_type: choice
  choices:
    - value: high
      label: High
- name: urgent
  data_type: boolean
  is_nullable: false
`

// inputRowValues appends one row to a form submission. Rows are keyed by token
// so their order is carried by row_order, not by map iteration.
func inputRowValues(values url.Values, token, originalName, name, label, dataType, defaultValue, helpText string, required bool, choices ...[2]string) {
	values.Add("row_order", token)
	values.Set("original_name__"+token, originalName)
	values.Set("name__"+token, name)
	values.Set("label__"+token, label)
	values.Set("data_type__"+token, dataType)
	values.Set("default_value__"+token, defaultValue)
	values.Set("help_text__"+token, helpText)
	if required {
		values.Set("required__"+token, "yes")
	}
	for _, choice := range choices {
		values.Add("choice_value__"+token, choice[0])
		values.Add("choice_label__"+token, choice[1])
	}
}

func valuesFromFields(fields []db.TaskInputField) url.Values {
	values := url.Values{}
	for index, field := range fields {
		token := "r" + string(rune('0'+index))
		choices := make([][2]string, 0, len(field.Choices))
		for _, choice := range field.Choices {
			choices = append(choices, [2]string{choice.Value, choice.Label})
		}
		inputRowValues(values, token, field.Name, field.Name, field.Label, field.DataType,
			field.DefaultValue, field.HelpText, !field.IsNullable, choices...)
	}
	return values
}

func postTaskInputs(t *testing.T, slug string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/inputs", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	handleTaskDefinitionInputsSave(rec, req)
	return rec
}

func postTaskInputsRaw(t *testing.T, slug, raw string) *httptest.ResponseRecorder {
	t.Helper()
	values := url.Values{"input_schema_yaml": {raw}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/inputs/raw", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	handleTaskDefinitionRawSave(rec, req)
	return rec
}

func getEditor(t *testing.T, slug string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/task-definitions/"+slug, nil)
	req.SetPathValue("slug", slug)
	handleTaskDefinitionEditor(rec, req)
	return rec
}

func storedSchema(t *testing.T, ctx context.Context, slug string) string {
	t.Helper()
	row, err := db.GetTaskDefinitionBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("reload %q: %v", slug, err)
	}
	return row.InputSchemaYAML
}

func storedUpdatedAt(t *testing.T, ctx context.Context, slug string) string {
	t.Helper()
	var stamp string
	if err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(_updated_at, 'epoch'::timestamptz)::text FROM base_task_definition WHERE slug = $1`, slug).
		Scan(&stamp); err != nil {
		t.Fatalf("read _updated_at: %v", err)
	}
	return stamp
}

// countSchemaKeys counts top-level item keys, ignoring keys nested in a choice
// pair and the first key sitting on the "- " line.
func countSchemaKeys(written, key string) int {
	count := 0
	for _, line := range strings.Split(written, "\n") {
		content := line
		if strings.HasPrefix(content, "- ") {
			content = "  " + strings.TrimPrefix(content, "- ")
		}
		indent := len(content) - len(strings.TrimLeft(content, " "))
		if indent == 2 && strings.HasPrefix(strings.TrimSpace(content), key+":") {
			count++
		}
	}
	return count
}

func parsedNames(t *testing.T, raw string) []string {
	t.Helper()
	fields, err := db.ParseTaskInputSchema(raw)
	if err != nil {
		t.Fatalf("parse stored schema: %v\n%s", err, raw)
	}
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	return names
}

// AC-18, AC-23, AC-24, AC-25, AC-38: one row per field, in YAML order, with the
// six controls, the notes that explain the engine, and one row per line.
func TestTaskDefinitionInputsEditorRendersRowsInOrder(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsrender")
	insertDefinition(t, ctx, slug, "Inputs Render TDE", "", tdeInputsRich, false, true)

	rec := getEditor(t, slug)
	if rec.Code != http.StatusOK {
		t.Fatalf("editor status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	if got := strings.Count(body, `class="ui-def-inrow"`); got != 4 {
		t.Errorf("rendered %d input rows, want 4", got)
	}
	order := []string{`name="name__r0"`, `name="name__r1"`, `name="name__r2"`, `name="name__r3"`}
	last := -1
	for index, needle := range order {
		at := strings.Index(body, needle)
		if at < 0 {
			t.Fatalf("row %d is missing (%s)", index, needle)
		}
		if at < last {
			t.Errorf("row %d is out of document order", index)
		}
		last = at
	}
	for _, want := range []string{`value="server"`, `value="reason"`, `value="priority"`, `value="urgent"`} {
		if !strings.Contains(body, want) {
			t.Errorf("editor is missing the stored value %s", want)
		}
	}

	// Required is the inverse of is_nullable.
	if !strings.Contains(body, `name="required__r0" value="yes" checked`) {
		t.Error("the required field is not ticked")
	}
	if !strings.Contains(body, `name="required__r1" value="yes" />`) {
		t.Error("the optional field is ticked")
	}
	// A boolean is answered yes/no, so required is not offered (AC-24).
	if strings.Contains(body, `name="required__r3"`) {
		t.Error("a boolean row offers a required checkbox, which the engine would ignore")
	}
	if !strings.Contains(body, "Booleans are answered yes/no; required has no effect.") {
		t.Error("the boolean note is missing")
	}
	// A default makes the field optional (AC-25).
	if strings.Count(body, "A default makes this optional at request time") != 4 {
		t.Error("the default note is missing from a row")
	}
	if strings.Count(body, "data-default-note hidden") != 3 {
		t.Errorf("the default note is shown on rows without a default (found %d hidden notes, want 3)", strings.Count(body, "data-default-note hidden"))
	}
	// Reserved names are flagged where they are typed (AC-38).
	if !strings.Contains(body, `data-reserved-note="priority">priority: Sets the task priority`) {
		t.Error("the reserved name priority is not flagged")
	}
	if !strings.Contains(body, `data-reserved-note="body" hidden`) {
		t.Error("the body flag is shown on a row that does not use that name")
	}
	if !strings.Contains(body, `value="high"`) {
		t.Error("the stored choice is missing")
	}
}

// AC-29, AC-30: the type control offers the renderable twelve, and a stored
// reference: type is preserved rather than rewritten.
func TestTaskDefinitionInputsTypeVocabularyAndPreservedType(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputstypes")
	schema := `- name: target
  data_type: 'reference:tables'
- name: script
  data_type: 'code:go'
- name: note
  data_type: text
`
	insertDefinition(t, ctx, slug, "Inputs Types TDE", "", schema, false, true)

	rec := getEditor(t, slug)
	if rec.Code != http.StatusOK {
		t.Fatalf("editor status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, option := range taskInputDataTypeOptions {
		if !strings.Contains(body, `<option value="`+option+`"`) {
			t.Errorf("the type control does not offer %q", option)
		}
	}
	if strings.Contains(body, `<option value="reference:`) || strings.Contains(body, `<option value="code:`) {
		t.Error("the type control offers a non-renderable type")
	}
	if strings.Count(body, "Renders as a plain text input; edit in Raw YAML.") != 2 {
		t.Error("a preserved type does not say why it is read-only")
	}
	if !strings.Contains(body, `value="reference:tables" readonly`) {
		t.Error("the stored reference type is not shown read-only")
	}

	values := url.Values{}
	inputRowValues(values, "r0", "target", "target", "", "reference:tables", "", "", true)
	inputRowValues(values, "r1", "script", "script", "", "code:go", "", "", true)
	inputRowValues(values, "r2", "note", "note", "", "text", "", "", false)
	saved := postTaskInputs(t, slug, values)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200: %s", saved.Code, saved.Body.String())
	}
	fields, err := db.ParseTaskInputSchema(storedSchema(t, ctx, slug))
	if err != nil {
		t.Fatalf("stored schema does not parse: %v", err)
	}
	if fields[0].DataType != "reference:tables" || fields[1].DataType != "code:go" {
		t.Errorf("types were rewritten: %+v", fields)
	}
}

// AC-22, AC-23, AC-27, AC-36: one save writes every row in DOM order, emits all
// seven keys, trims text, and the form's own output keeps the editor offered.
func TestTaskDefinitionInputsSaveWritesOrderAndAllKeys(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsorder")
	insertDefinition(t, ctx, slug, "Inputs Order TDE", "", tdeTwoInputs, false, true)

	values := url.Values{}
	inputRowValues(values, "r0", "reason", "  reason  ", "  Reason  ", "text", "  none  ", " why ", true)
	inputRowValues(values, "r1", "server", "server", "Server", "text", "", "", false)

	rec := postTaskInputs(t, slug, values)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	written := storedSchema(t, ctx, slug)
	names := parsedNames(t, written)
	if len(names) != 2 || names[0] != "reason" || names[1] != "server" {
		t.Fatalf("stored order = %v, want [reason server]\n%s", names, written)
	}
	fields, _ := db.ParseTaskInputSchema(written)
	if fields[0].Label != "Reason" || fields[0].DefaultValue != "none" || fields[0].HelpText != "why" {
		t.Errorf("values were not trimmed: %+v", fields[0])
	}
	if fields[1].IsNullable != true || fields[0].IsNullable != false {
		t.Errorf("is_nullable is not the inverse of Required: %+v", fields)
	}
	for _, key := range db.TaskInputFieldKeys {
		if count := countSchemaKeys(written, key); count != 2 {
			t.Errorf("key %q appears %d times, want 2:\n%s", key, count, written)
		}
	}

	// AC-36: reload, and the form still offers the same rows in the same order.
	reloaded := getEditor(t, slug)
	body := reloaded.Body.String()
	if !strings.Contains(body, "Add row") {
		t.Error("reloading the form's own output disabled the row editor")
	}
	if !strings.Contains(body, `name="name__r0" value="reason"`) || !strings.Contains(body, `name="name__r1" value="server"`) {
		t.Error("the reloaded form does not show the saved rows in order")
	}
}

// AC-31, AC-33: a duplicate name is the parser's refusal, shown in one row, and
// nothing is written.
func TestTaskDefinitionInputsDuplicateNameRefusedInOneRow(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsdupe")
	insertDefinition(t, ctx, slug, "Inputs Dupe TDE", "", "", false, true)

	values := url.Values{}
	inputRowValues(values, "r0", "", "server", "Server", "text", "", "", true)
	inputRowValues(values, "r1", "", "server", "Server again", "text", "", "", true)
	before := storedSchema(t, ctx, slug)

	rec := postTaskInputs(t, slug, values)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate save status = %d, want 400", rec.Code)
	}
	body := rec.Body.String()
	want := html.EscapeString(`input schema declares "server" twice`)
	if !strings.Contains(body, want) {
		t.Fatalf("the parser's message is not rendered verbatim:\n%s", body)
	}
	if got := strings.Count(body, "data-row-error="); got != 1 {
		t.Errorf("the refusal marked %d rows, want exactly 1", got)
	}
	if after := storedSchema(t, ctx, slug); after != before {
		t.Error("a refused save wrote to input_schema_yaml")
	}
}

// AC-32, AC-33: an unsafe or empty name is attributed to its row, by name where
// the parser quotes one and by position where it cannot.
func TestTaskDefinitionInputsUnsafeNameRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsunsafe")
	insertDefinition(t, ctx, slug, "Inputs Unsafe TDE", "", "", false, true)

	unsafe := url.Values{}
	inputRowValues(unsafe, "r0", "", "server-name", "Server", "text", "", "", true)
	rec := postTaskInputs(t, slug, unsafe)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unsafe name status = %d, want 400", rec.Code)
	}
	message := html.EscapeString(`input schema field "server-name" is not a valid name (letters, digits, underscore)`)
	if !strings.Contains(rec.Body.String(), message) {
		t.Fatalf("unsafe name message is missing:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `data-row-error="r0"`) {
		t.Error("the unsafe name was not attributed to its row")
	}

	empty := url.Values{}
	inputRowValues(empty, "r0", "", "server", "Server", "text", "", "", true)
	inputRowValues(empty, "r1", "", "", "Untitled", "text", "", "", false)
	rec = postTaskInputs(t, slug, empty)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "input schema field 2 has no name") {
		t.Fatalf("empty name message is missing:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `data-row-error="r1"`) {
		t.Error("the empty name was not attributed to the second row")
	}
}

// AC-26, AC-28: a boolean default is yes/no and a choice needs a value.
func TestTaskDefinitionInputsBooleanDefaultAndChoiceValueRules(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsvalues")
	insertDefinition(t, ctx, slug, "Inputs Values TDE", "", "", false, true)

	bad := url.Values{}
	inputRowValues(bad, "r0", "", "urgent", "Urgent", "boolean", "true", "", false)
	rec := postTaskInputs(t, slug, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-canonical boolean default status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "A boolean default must be yes or no") {
		t.Errorf("boolean default refusal is missing:\n%s", rec.Body.String())
	}

	good := url.Values{}
	inputRowValues(good, "r0", "", "urgent", "Urgent", "boolean", "yes", "", false)
	rec = postTaskInputs(t, slug, good)
	if rec.Code != http.StatusOK {
		t.Fatalf("boolean yes status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	fields, _ := db.ParseTaskInputSchema(storedSchema(t, ctx, slug))
	if len(fields) != 1 || fields[0].DefaultValue != "yes" {
		t.Fatalf("boolean default was not stored: %+v", fields)
	}

	choice := url.Values{}
	inputRowValues(choice, "r0", "", "pick", "Pick", "choice", "", "", true, [2]string{"", "No value"})
	rec = postTaskInputs(t, slug, choice)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty choice value status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "A choice needs a value") {
		t.Errorf("choice refusal is missing:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `data-row-error="r0"`) {
		t.Error("the choice refusal does not name the row")
	}
}

// AC-34, AC-48: a document with a key the form does not model disables the row
// editor, and the form save refuses rather than rewriting it.
func TestTaskDefinitionInputsLosslessGateRefusesUnknownKeys(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsgate")
	seed := "- name: server\n  data_type: text\n  colour: red\n"
	insertDefinition(t, ctx, slug, "Inputs Gate TDE", "", seed, false, true)

	rec := getEditor(t, slug)
	if rec.Code != http.StatusOK {
		t.Fatalf("editor status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "contains keys this form does not model: colour") {
		t.Errorf("the refusal does not name the unknown key:\n%s", body)
	}
	if strings.Contains(body, ">Add row<") {
		t.Error("the row editor is offered for a document it cannot represent")
	}
	if !strings.Contains(body, "colour: red") {
		t.Error("the raw YAML does not show the stored document")
	}

	values := url.Values{}
	inputRowValues(values, "r0", "server", "server", "Server", "text", "", "", true)
	before, stamp := storedSchema(t, ctx, slug), storedUpdatedAt(t, ctx, slug)
	refusal := postTaskInputs(t, slug, values)
	if refusal.Code != http.StatusBadRequest {
		t.Fatalf("form save on a raw-only definition status = %d, want 400", refusal.Code)
	}
	if !strings.Contains(refusal.Body.String(), "contains keys this form does not model: colour") {
		t.Error("the refusal does not carry the same reason")
	}
	if after := storedSchema(t, ctx, slug); after != before {
		t.Errorf("a refused form save rewrote the column:\n%q\n%q", before, after)
	}
	if storedUpdatedAt(t, ctx, slug) != stamp {
		t.Error("a refused form save touched the row")
	}
}

// AC-39: a hand-written document with comments saves through the form to the
// same fields; the comment is gone and nothing else changed.
func TestTaskDefinitionInputsFormSavePreservesFields(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsround")
	seed := `# the requester form
- name: server   # which box
  label: Server
  data_type: text
  is_nullable: false
  choices: []
- name: reason
  data_type: long_text
`
	insertDefinition(t, ctx, slug, "Inputs Round TDE", "", seed, false, true)

	original, err := db.ParseTaskInputSchema(seed)
	if err != nil {
		t.Fatalf("seed does not parse: %v", err)
	}
	rec := postTaskInputs(t, slug, valuesFromFields(original))
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	written := storedSchema(t, ctx, slug)
	if strings.Contains(written, "#") {
		t.Errorf("the comment survived a form save:\n%s", written)
	}
	saved, err := db.ParseTaskInputSchema(written)
	if err != nil {
		t.Fatalf("saved schema does not parse: %v", err)
	}
	if len(saved) != len(original) {
		t.Fatalf("saved %d fields, want %d", len(saved), len(original))
	}
	for index := range original {
		// The slash in output is expected: both sides went through the parser.
		if saved[index].Name != original[index].Name || saved[index].DataType != original[index].DataType || saved[index].IsNullable != original[index].IsNullable {
			t.Errorf("field %d changed: %+v -> %+v", index, original[index], saved[index])
		}
	}
}

// AC-37: the raw tab is writable on every definition and writes exactly what
// was typed; the gate re-runs afterwards, in both directions.
func TestTaskDefinitionRawYAMLSaveAndGateRerun(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsraw")
	insertDefinition(t, ctx, slug, "Inputs Raw TDE", "", tdeTwoInputs, false, true)

	custom := "- name: server\n  data_type: text\n  colour: red\n"
	rec := postTaskInputsRaw(t, slug, custom)
	if rec.Code != http.StatusOK {
		t.Fatalf("raw save status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if storedSchema(t, ctx, slug) != custom {
		t.Errorf("raw save did not write the document as typed:\n%q", storedSchema(t, ctx, slug))
	}
	body := getEditor(t, slug).Body.String()
	if strings.Contains(body, ">Add row<") {
		t.Error("the row editor is still offered after a raw save that broke modellability")
	}

	clean := "- name: server\n  data_type: text\n  is_nullable: false\n"
	rec = postTaskInputsRaw(t, slug, clean)
	if rec.Code != http.StatusOK {
		t.Fatalf("second raw save status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(getEditor(t, slug).Body.String(), ">Add row<") {
		t.Error("the row editor did not come back after a modellable raw save")
	}
}

// AC-37: an unparseable raw save is refused with the parser's message and leaves
// the column alone.
func TestTaskDefinitionRawYAMLRefusesUnparseable(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsrawbad")
	insertDefinition(t, ctx, slug, "Inputs Raw Bad TDE", "", tdeTwoInputs, false, true)
	before := storedSchema(t, ctx, slug)

	rec := postTaskInputsRaw(t, slug, "- name: [\n")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unparseable raw save status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "does not parse") {
		t.Errorf("the parser's message is missing:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "name: [") {
		t.Error("the submitted text was not kept on screen after a refusal")
	}
	if storedSchema(t, ctx, slug) != before {
		t.Error("a refused raw save rewrote the column")
	}
}

// AC-19, AC-48: "Add row" is a GET that writes nothing, and the mutation paths
// answer a GET with 405.
func TestTaskDefinitionInputRowFragmentAndGetIsNotAMutation(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsrowfrag")
	insertDefinition(t, ctx, slug, "Inputs Row Fragment TDE", "", tdeTwoInputs, false, true)

	before, stamp := storedSchema(t, ctx, slug), storedUpdatedAt(t, ctx, slug)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/task-definitions/"+slug+"/inputs/rows/new", nil)
	req.SetPathValue("slug", slug)
	handleTaskDefinitionInputRowFragment(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("row fragment status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"data-input-row", `name="name__new-`, `<option value="text" selected>text</option>`, `name="required__`, " checked"} {
		if !strings.Contains(body, want) {
			t.Errorf("row fragment is missing %q:\n%s", want, body)
		}
	}
	if storedSchema(t, ctx, slug) != before || storedUpdatedAt(t, ctx, slug) != stamp {
		t.Error("the row fragment GET wrote to the definition")
	}

	for name, handler := range map[string]http.HandlerFunc{
		"inputs save": handleTaskDefinitionInputsSave,
		"raw save":    handleTaskDefinitionRawSave,
	} {
		w := httptest.NewRecorder()
		bad := httptest.NewRequest(http.MethodGet, "/task-definitions/"+slug+"/inputs", nil)
		bad.SetPathValue("slug", slug)
		handler(w, bad)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s answered a GET with %d, want 405", name, w.Code)
		}
	}
	if storedSchema(t, ctx, slug) != before {
		t.Error("a GET to a mutation path wrote to the column")
	}
}

// AC-24: a boolean's stored is_nullable survives a form save, because the row
// offers no required control to carry it.
func TestTaskDefinitionInputsBooleanKeepsNullableFlag(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "inputsboolnull")
	insertDefinition(t, ctx, slug, "Inputs Bool Null TDE", "", "- name: urgent\n  data_type: boolean\n  is_nullable: false\n", false, true)

	values := url.Values{}
	inputRowValues(values, "r0", "urgent", "urgent", "Urgent", "boolean", "", "", false)
	rec := postTaskInputs(t, slug, values)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	fields, err := db.ParseTaskInputSchema(storedSchema(t, ctx, slug))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(fields) != 1 || fields[0].IsNullable {
		t.Errorf("the boolean's is_nullable was cleared: %+v", fields)
	}
}
