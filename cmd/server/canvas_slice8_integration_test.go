package main

// Slice 8 of the designer rework: the new-definition flow (Brief R7).
//
// One page collects slug, display name, description and active; then the
// assignment group and strategy; then — only when the strategy is named_user —
// a user scoped to that group and required; then the inputs designer; then the
// definition is created and the author lands on the canvas with its Start
// trigger in place.
//
// The template assertions need no database. The flow and scoping tests use real
// Postgres, gated on TEST_DATABASE_URL: a skip here is a broken run, not a pass.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"velm/internal/db"
)

// renderNewDefinitionFlow renders the one-page flow through the same template
// and view data the handler builds, without touching the database.
func renderNewDefinitionFlow(t *testing.T, form newDefinitionForm) string {
	t.Helper()
	ensureTestSessionStore()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/task-definitions/new", nil)
	viewData := newViewData(rec, req, "/task-definitions/new", "New Task Definition", "Operations")
	viewData["View"] = "task-definition-new"
	viewData["NewDefinition"] = newDefinitionPage{
		Form:       form,
		Strategies: db.AssignmentStrategies,
		DescriptionField: markdownFieldView{
			ID: "new-definition-description", Name: "description", Value: form.Description,
		},
	}
	writeTaskDefinitionPage(rec, "task-definition-new-view", viewData, http.StatusOK)
	return rec.Body.String()
}

// elementWith extracts the tag containing marker, so an assertion about
// hidden/required cannot pass on an unrelated part of the page.
func elementWith(t *testing.T, body, marker string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	t.Fatalf("no element carrying %q\n%s", marker, body)
	return ""
}

// AC (slice 8): the named-user field appears only for the named_user strategy,
// and is required there.
func TestNamedUserFieldOnlyForNamedUserStrategy(t *testing.T) {
	inherited := renderNewDefinitionFlow(t, newDefinitionForm{})
	label := elementWith(t, inherited, `data-named-user-field`)
	if !strings.Contains(label, "hidden") {
		t.Errorf("named-user field is not hidden for an unset strategy: %s", label)
	}
	selectTag := elementWith(t, inherited, `data-named-user-select`)
	if strings.Contains(selectTag, "required") {
		t.Errorf("named-user select is required for an unset strategy: %s", selectTag)
	}

	named := renderNewDefinitionFlow(t, newDefinitionForm{Strategy: db.AssignmentNamedUser})
	label = elementWith(t, named, `data-named-user-field`)
	if strings.Contains(label, "hidden") {
		t.Errorf("named-user field is hidden for the named_user strategy: %s", label)
	}
	selectTag = elementWith(t, named, `data-named-user-select`)
	if !strings.Contains(selectTag, "required") {
		t.Errorf("named-user select is not required for the named_user strategy: %s", selectTag)
	}

	// Only the named_user strategy is the one that asks for a person: every
	// other strategy leaves the field hidden.
	for _, strategy := range []string{
		db.AssignmentUnassigned, db.AssignmentRequester, db.AssignmentFromInput,
		db.AssignmentRandomMember, db.AssignmentRoundRobin,
	} {
		body := renderNewDefinitionFlow(t, newDefinitionForm{Strategy: strategy})
		if label := elementWith(t, body, `data-named-user-field`); !strings.Contains(label, "hidden") {
			t.Errorf("named-user field is visible for strategy %q: %s", strategy, label)
		}
	}
}

// AC (slice 8): the flow renders in its documented order — basics, assignment,
// inputs, then the link down to the canvas — and reuses the shared markdown
// field and inputs designer rather than new ones.
func TestNewDefinitionFlowOrdersItsSections(t *testing.T) {
	body := renderNewDefinitionFlow(t, newDefinitionForm{IsActive: true})
	positions := map[string]int{
		`id="basics"`:             strings.Index(body, `id="basics"`),
		`id="assignment"`:         strings.Index(body, `id="assignment"`),
		`id="inputs"`:             strings.Index(body, `id="inputs"`),
		`id="continue-to-canvas"`: strings.Index(body, `id="continue-to-canvas"`),
	}
	order := []string{`id="basics"`, `id="assignment"`, `id="inputs"`, `id="continue-to-canvas"`}
	for _, key := range order {
		if positions[key] < 0 {
			t.Fatalf("flow is missing section %s\n%s", key, body)
		}
	}
	for i := 1; i < len(order); i++ {
		if positions[order[i]] < positions[order[i-1]] {
			t.Errorf("%s appears before %s; the flow's order is basics, assignment, inputs, canvas", order[i], order[i-1])
		}
	}
	// Reuse, not a second implementation.
	if !strings.Contains(body, `data-markdown-field="true"`) {
		t.Error("the description is not the shared markdown field component")
	}
	if !strings.Contains(body, `hx-get="/task-definitions/new/inputs/rows/new"`) {
		t.Error("the inputs designer does not add rows through the shared row fragment")
	}
}

// AC (slice 8): changing the assignment group blanks a named user who is not a
// member of the new group, so a stale selection can never be submitted. The
// browser clears it on change; the handler refuses it if it arrives anyway and
// re-renders with nothing selected.
func TestChangingGroupBlanksTheNamedUser(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	newGroup := func(name string) string {
		var id string
		if err := db.Pool.QueryRow(ctx, `INSERT INTO _group (name, description, is_system) VALUES ($1, 'slice8 scoping', FALSE) RETURNING _id::text`, name).Scan(&id); err != nil {
			t.Fatalf("seed group: %v", err)
		}
		t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `DELETE FROM _group WHERE _id = $1`, id) })
		return id
	}
	newUser := func(name string) string {
		var id string
		if err := db.Pool.QueryRow(ctx, `INSERT INTO _user (name, email) VALUES ($1, $2) RETURNING _id::text`,
			name, name+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `DELETE FROM _user WHERE _id = $1`, id) })
		return id
	}

	groupA := newGroup("slice8-a-" + suffix)
	groupB := newGroup("slice8-b-" + suffix)
	userA := newUser("slice8-user-a-" + suffix)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO _group_membership (user_id, group_id) VALUES ($1, $2)`, userA, groupA); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `DELETE FROM _group_membership WHERE user_id = $1`, userA) })

	// The client half: the group's change handler clears a selection that the
	// new group does not allow.
	body := renderNewDefinitionFlow(t, newDefinitionForm{GroupID: groupA, Strategy: db.AssignmentNamedUser, NamedUserID: userA})
	if !strings.Contains(body, `group.addEventListener("change"`) || !strings.Contains(body, `user.value = ""`) {
		t.Error("the flow has no group-change handler that clears a user outside the new group")
	}

	// The server half: a moved group with the old user is refused, and the
	// re-rendered form does not carry the stale selection forward.
	slug := tdeSlug(t, "groupchange")
	tdeCleanup(t, ctx, slug)
	values := url.Values{
		"slug":                {slug},
		"display_name":        {"Group Change S8"},
		"is_active":           {"yes"},
		"default_group_id":    {groupB},
		"assignment_strategy": {db.AssignmentNamedUser},
		"default_assignee_id": {userA},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handleTaskDefinitionCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("moved group status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not a member of the chosen assignment group") {
		t.Errorf("refusal does not say the user is outside the group: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `value="`+userA+`" selected`) {
		t.Error("the re-rendered form still selects a user outside the chosen group")
	}
	if _, err := db.GetTaskDefinitionBySlug(ctx, slug); err == nil {
		t.Error("the refused definition was created anyway")
	}
}

// AC (slice 8): a definition created through the flow lands on the canvas with
// its trigger present, and the assignment and inputs it declared are stored.
func TestDefinitionCreatedThroughTheFlowLandsOnCanvasWithTrigger(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	slug := tdeSlug(t, "newflow")
	tdeCleanup(t, ctx, slug)

	var groupID string
	if err := db.Pool.QueryRow(ctx, `INSERT INTO _group (name, description, is_system) VALUES ($1, 'slice8 flow', FALSE) RETURNING _id::text`, "slice8-flow-"+suffix).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `DELETE FROM _group WHERE _id = $1`, groupID) })
	var userID string
	if err := db.Pool.QueryRow(ctx, `INSERT INTO _user (name, email) VALUES ($1, $2) RETURNING _id::text`,
		"slice8-flow-user-"+suffix, "slice8-flow-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `DELETE FROM _user WHERE _id = $1`, userID) })
	if _, err := db.Pool.Exec(ctx, `INSERT INTO _group_membership (user_id, group_id) VALUES ($1, $2)`, userID, groupID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `DELETE FROM _group_membership WHERE user_id = $1`, userID) })

	values := url.Values{
		"slug":                {slug},
		"display_name":        {"New Flow S8"},
		"description":         {"Created through the one-page flow."},
		"is_active":           {"yes"},
		"default_group_id":    {groupID},
		"assignment_strategy": {db.AssignmentNamedUser},
		"default_assignee_id": {userID},
		"row_order":           {"r1"},
		"name__r1":            {"server"},
		"label__r1":           {"Server"},
		"data_type__r1":       {"text"},
		"required__r1":        {"yes"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handleTaskDefinitionCreate(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/task-definitions/"+slug {
		t.Fatalf("create redirect = %q, want the canvas", got)
	}

	row, err := db.GetTaskDefinitionBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("created definition is not readable: %v", err)
	}
	if row.DefaultGroupID != groupID || row.AssignmentStrategy != db.AssignmentNamedUser || row.DefaultAssigneeID != userID {
		t.Errorf("stored assignment = (%q, %q, %q), want (%q, %q, %q)",
			row.DefaultGroupID, row.AssignmentStrategy, row.DefaultAssigneeID,
			groupID, db.AssignmentNamedUser, userID)
	}
	if !row.IsActive {
		t.Error("the flow's active tick was not stored")
	}
	schema, err := db.ParseTaskInputSchema(row.InputSchemaYAML)
	if err != nil {
		t.Fatalf("stored input schema does not parse: %v", err)
	}
	if len(schema) != 1 || schema[0].Name != "server" || schema[0].IsNullable {
		t.Errorf("stored inputs = %+v, want one required input named server", schema)
	}

	// Landing on the canvas: the default editor view is the canvas, and the
	// structural Start trigger is there for a definition with no authored
	// blocks (Design Vision D6).
	editor := httptest.NewRecorder()
	editorReq := httptest.NewRequest(http.MethodGet, "/task-definitions/"+slug, nil)
	editorReq.SetPathValue("slug", slug)
	handleTaskDefinitionEditor(editor, editorReq)
	if editor.Code != http.StatusOK {
		t.Fatalf("canvas status = %d, want 200: %s", editor.Code, editor.Body.String())
	}
	page := editor.Body.String()
	if !strings.Contains(page, `id="canvas"`) || !strings.Contains(page, `id="canvas-tree"`) {
		t.Error("the editor did not render the canvas")
	}
	if !strings.Contains(page, `data-trigger="true"`) {
		t.Error("the canvas has no Start trigger for the new definition")
	}
}
