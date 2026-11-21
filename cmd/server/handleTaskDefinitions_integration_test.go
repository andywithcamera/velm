package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"velm/internal/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration tests for slice 1 of the task definition editor: the list, step
// counts, search, creating a definition and saving its basics.
//
// Gated on TEST_DATABASE_URL like every other real-Postgres test here. A skip
// means the environment is wrong, not that the code is fine.

func taskDefinitionsTestDB(t *testing.T) context.Context {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}
	t.Setenv("DATABASE_URL", url)
	os.Setenv("DATABASE_URL", url)
	if err := db.ConnectToDB(); err != nil {
		t.Fatalf("ConnectToDB: %v", err)
	}
	// A closed pool left non-nil makes the next test that guards on
	// db.Pool == nil skip ConnectToDB and fail with "closed pool", so tests
	// run in one order and pass in another. Restore both halves of the state.
	t.Cleanup(func() {
		db.CloseDB()
		db.Pool = nil
	})
	ctx := context.Background()
	if err := db.RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	ensureTestSessionStore()
	return ctx
}

func tdeSlug(t *testing.T, name string) string {
	t.Helper()
	return fmt.Sprintf("zz-tde-%s-%d", name, time.Now().UnixNano())
}

func tdeCleanup(t *testing.T, ctx context.Context, slug string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM base_task_definition_child WHERE container_definition_id IN (SELECT _id FROM base_task_definition WHERE slug = $1)`, slug)
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM base_task_definition WHERE slug = $1`, slug)
	})
}

// insertDefinition seeds a definition with an explicit input schema, bypassing
// the handler so a test can also seed rows the editor must refuse to read.
func insertDefinition(t *testing.T, ctx context.Context, slug, display, description, schema string, container, active bool) string {
	t.Helper()
	tdeCleanup(t, ctx, slug)
	var id string
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition
			(slug, display_name, description, is_container, is_active, input_schema_yaml)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING _id::text`, slug, display, description, container, active, schema).Scan(&id); err != nil {
		t.Fatalf("seed definition %s: %v", slug, err)
	}
	return id
}

func linkStep(t *testing.T, ctx context.Context, containerID, childID string, sortOrder int) {
	t.Helper()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child (container_definition_id, child_definition_id, sort_order)
		VALUES ($1, $2, $3)`, containerID, childID, sortOrder); err != nil {
		t.Fatalf("link step: %v", err)
	}
}

const tdeTwoInputs = `- name: server
  label: Server
  data_type: text
  is_nullable: false
- name: reason
  label: Reason
  data_type: long_text
  is_nullable: true
`

func findRow(rows []db.TaskDefinitionRow, slug string) (db.TaskDefinitionRow, bool) {
	for _, row := range rows {
		if row.Slug == slug {
			return row, true
		}
	}
	return db.TaskDefinitionRow{}, false
}

// AC-1 to AC-4, AC-6: one row per live definition, with input and step counts,
// and a soft-deleted definition gone.
func TestListTaskDefinitionRowsCountsAndSoftDelete(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)

	leafSlug := tdeSlug(t, "leaf")
	containerSlug := tdeSlug(t, "container")
	otherSlug := tdeSlug(t, "other")
	deletedSlug := tdeSlug(t, "deleted")

	leafID := insertDefinition(t, ctx, leafSlug, "Reboot Server TDE", "", tdeTwoInputs, false, true)
	containerID := insertDefinition(t, ctx, containerSlug, "Upgrade OS TDE", "", "", true, true)
	otherID := insertDefinition(t, ctx, otherSlug, "Run Remote Command TDE", "", "not: [valid", false, false)
	insertDefinition(t, ctx, deletedSlug, "Deleted TDE", "", "", false, true)

	linkStep(t, ctx, containerID, leafID, 10)
	linkStep(t, ctx, containerID, otherID, 20)
	if _, err := db.Pool.Exec(ctx, `UPDATE base_task_definition SET _deleted_at = NOW() WHERE slug = $1`, deletedSlug); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	rows, err := db.ListTaskDefinitionRows(ctx, "")
	if err != nil {
		t.Fatalf("ListTaskDefinitionRows: %v", err)
	}

	leaf, ok := findRow(rows, leafSlug)
	if !ok {
		t.Fatalf("leaf %q missing from the list", leafSlug)
	}
	if leaf.IsContainer {
		t.Error("leaf reports itself as a container")
	}
	if leaf.ChildCount != 0 {
		t.Errorf("leaf step count = %d, want 0", leaf.ChildCount)
	}
	if got := leaf.InputCount(); got != 2 {
		t.Errorf("leaf input count = %d, want 2", got)
	}

	container, ok := findRow(rows, containerSlug)
	if !ok {
		t.Fatalf("container %q missing from the list", containerSlug)
	}
	if !container.IsContainer {
		t.Error("container reports itself as a leaf")
	}
	if container.ChildCount != 2 {
		t.Errorf("container step count = %d, want 2", container.ChildCount)
	}

	// An unreadable schema is reported as a dash, never as a hidden row.
	other, ok := findRow(rows, otherSlug)
	if !ok {
		t.Fatalf("definition with a malformed schema %q was hidden from the list", otherSlug)
	}
	if got := other.InputCount(); got != -1 {
		t.Errorf("malformed schema input count = %d, want -1 (rendered as a dash)", got)
	}
	if other.IsActive {
		t.Error("inactive definition reported as active")
	}

	if _, found := findRow(rows, deletedSlug); found {
		t.Error("soft-deleted definition appears in the list")
	}
}

// AC-5: search is applied by the query, case-insensitively, on slug and name.
func TestListTaskDefinitionRowsSearch(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)

	slug := tdeSlug(t, "reboot")
	insertDefinition(t, ctx, slug, "Reboot The Box TDE", "", "", false, true)

	byName, err := db.ListTaskDefinitionRows(ctx, "reboot the box")
	if err != nil {
		t.Fatalf("search by name: %v", err)
	}
	if _, ok := findRow(byName, slug); !ok {
		t.Fatalf("case-insensitive name search missed %q", slug)
	}

	bySlug, err := db.ListTaskDefinitionRows(ctx, strings.ToUpper(slug))
	if err != nil {
		t.Fatalf("search by slug: %v", err)
	}
	if _, ok := findRow(bySlug, slug); !ok {
		t.Fatalf("case-insensitive slug search missed %q", slug)
	}

	none, err := db.ListTaskDefinitionRows(ctx, slug+"-nothing-matches-this")
	if err != nil {
		t.Fatalf("search with no match: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("search with no match returned %d rows", len(none))
	}

	// A needle's wildcards are literal text, not a pattern: "%" must not turn
	// every definition into a match.
	wildcard, err := db.ListTaskDefinitionRows(ctx, "%")
	if err != nil {
		t.Fatalf("search for a literal wildcard: %v", err)
	}
	if len(wildcard) != 0 {
		t.Errorf(`a search for "%%" matched %d definitions; the needle is not escaped`, len(wildcard))
	}
}

// queryCounter counts the statements a list read issues against the definition
// tables.
type queryCounter struct{ count int }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (c *queryCounter) TraceQueryEnd(_ context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	c.count++
}

// AC-8: the list costs the same number of queries for one definition and for a
// hundred, and that number is at most three. A count per row would fail the
// equality here.
func TestTaskDefinitionListQueryCountDoesNotGrowWithRows(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)

	prefix := tdeSlug(t, "bulk")
	var target string
	for i := 0; i < 100; i++ {
		slug := fmt.Sprintf("%s-%03d", prefix, i)
		if i == 0 {
			target = slug
		}
		insertDefinition(t, ctx, slug, fmt.Sprintf("Bulk TDE %03d", i), "", "", false, true)
	}

	original := db.Pool
	t.Cleanup(func() { db.Pool = original })

	counting := &queryCounter{}
	config := original.Config()
	config.ConnConfig.Tracer = counting
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open counting pool: %v", err)
	}
	t.Cleanup(pool.Close)
	db.Pool = pool

	many, err := db.ListTaskDefinitionRows(ctx, prefix)
	if err != nil {
		t.Fatalf("list 100 definitions: %v", err)
	}
	if len(many) != 100 {
		t.Fatalf("list returned %d definitions, want 100", len(many))
	}
	manyQueries := counting.count

	counting.count = 0
	one, err := db.ListTaskDefinitionRows(ctx, target)
	if err != nil {
		t.Fatalf("list 1 definition: %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("filtered list returned %d definitions, want 1", len(one))
	}
	oneQuery := counting.count

	if manyQueries > 3 || oneQuery > 3 {
		t.Errorf("list used %d queries for 100 rows and %d for 1; the budget is 3", manyQueries, oneQuery)
	}
	if manyQueries != oneQuery {
		t.Errorf("list used %d queries for 100 rows but %d for 1: the row count adds queries", manyQueries, oneQuery)
	}
}

// AC-64, AC-65: create writes the row and takes the column defaults; a live
// duplicate is the index's refusal, translated; a soft-deleted slug is free.
func TestCreateTaskDefinitionSlugRules(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "create")

	created, err := db.CreateTaskDefinition(ctx, slug, "Create Me TDE", "a description", false)
	if err != nil {
		t.Fatalf("CreateTaskDefinition: %v", err)
	}
	if created.Slug != slug || created.DisplayName != "Create Me TDE" {
		t.Fatalf("created definition = %+v", created)
	}
	if !created.IsActive {
		t.Error("a new definition is not active; 076 defaults is_active to TRUE")
	}
	if created.InputSchemaYAML != "" {
		t.Errorf("a new definition has an input schema %q, want empty", created.InputSchemaYAML)
	}

	if _, err := db.CreateTaskDefinition(ctx, slug, "Duplicate TDE", "", false); !errors.Is(err, db.ErrDuplicateTaskDefinitionSlug) {
		t.Fatalf("duplicate live slug error = %v, want ErrDuplicateTaskDefinitionSlug", err)
	}

	if _, err := db.Pool.Exec(ctx, `UPDATE base_task_definition SET _deleted_at = NOW() WHERE slug = $1`, slug); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := db.CreateTaskDefinition(ctx, slug, "Reborn TDE", "", false); err != nil {
		t.Fatalf("reusing a soft-deleted slug failed: %v", err)
	}

	if _, err := db.GetTaskDefinitionBySlug(ctx, tdeSlug(t, "absent")); !errors.Is(err, db.ErrTaskDefinitionNotFound) {
		t.Fatalf("absent slug error = %v, want ErrTaskDefinitionNotFound", err)
	}
}

// AC-11: a basics save writes the four columns and a reload shows them.
func TestTaskDefinitionBasicsSaveRoundTrip(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "basics")
	insertDefinition(t, ctx, slug, "Before TDE", "before", "", true, true)

	form := url.Values{
		"display_name": {"After TDE"},
		"description":  {"after"},
		"is_active":    {"yes"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/basics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	handleTaskDefinitionBasicsSave(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("basics save status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	row, err := db.GetTaskDefinitionBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.DisplayName != "After TDE" || row.Description != "after" {
		t.Errorf("after save = (%q, %q), want (\"After TDE\", \"after\")", row.DisplayName, row.Description)
	}
	if row.IsContainer {
		t.Error("is_container stayed true after being unticked")
	}
	if !row.IsActive {
		t.Error("is_active was cleared by a form that submitted it")
	}
}

// AC-11: a blank display name is refused with words, not a 500.
func TestTaskDefinitionBasicsRefusesBlankDisplayName(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "blank")
	insertDefinition(t, ctx, slug, "Keep TDE", "", "", false, true)

	form := url.Values{"display_name": {"   "}, "is_active": {"yes"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/basics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	handleTaskDefinitionBasicsSave(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Display name is required") {
		t.Errorf("refusal does not say the display name is required: %s", rec.Body.String())
	}
	row, _ := db.GetTaskDefinitionBySlug(ctx, slug)
	if row.DisplayName != "Keep TDE" {
		t.Errorf("display name changed to %q by a refused save", row.DisplayName)
	}
}

// AC-62: a container with steps cannot be made a leaf, and the refusal counts
// the steps.
func TestTaskDefinitionBasicsRefusesDroppingContainerWithSteps(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "hassteps")
	containerID := insertDefinition(t, ctx, slug, "Has Steps TDE", "", "", true, true)
	childID := insertDefinition(t, ctx, tdeSlug(t, "child"), "Child TDE", "", "", false, true)
	linkStep(t, ctx, containerID, childID, 10)
	linkStep(t, ctx, containerID, insertDefinition(t, ctx, tdeSlug(t, "child2"), "Child Two TDE", "", "", false, true), 20)

	form := url.Values{"display_name": {"Has Steps TDE"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/basics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	handleTaskDefinitionBasicsSave(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Remove the 2 steps first") {
		t.Errorf("refusal does not name the step count: %s", rec.Body.String())
	}
	row, _ := db.GetTaskDefinitionBySlug(ctx, slug)
	if !row.IsContainer {
		t.Error("is_container was cleared despite the refusal")
	}
}

// AC-1, AC-2, AC-5, AC-9: the list page renders labels, counts, the empty
// message and the new-definition link.
func TestTaskDefinitionsPageRenders(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)

	leafSlug := tdeSlug(t, "pageleaf")
	containerSlug := tdeSlug(t, "pagecontainer")
	inactiveSlug := tdeSlug(t, "pageinactive")
	leafID := insertDefinition(t, ctx, leafSlug, "Page Leaf TDE", "", tdeTwoInputs, false, true)
	containerID := insertDefinition(t, ctx, containerSlug, "Page Container TDE", "", "", true, true)
	insertDefinition(t, ctx, inactiveSlug, "Page Inactive TDE", "", "", false, false)
	linkStep(t, ctx, containerID, leafID, 10)

	rec := httptest.NewRecorder()
	handleTaskDefinitionsPage(rec, httptest.NewRequest(http.MethodGet, "/task-definitions?q=zz-tde-page", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{leafSlug, containerSlug, inactiveSlug, "Container", "Leaf", "Active", "Inactive", "/task-definitions/new"} {
		if !strings.Contains(body, want) {
			t.Errorf("list page is missing %q", want)
		}
	}
	if !strings.Contains(body, "<td>2</td>") {
		t.Error("list page does not show the two-input count")
	}

	empty := httptest.NewRecorder()
	handleTaskDefinitionsPage(empty, httptest.NewRequest(http.MethodGet, "/task-definitions?q=zz-tde-no-such-definition", nil))
	if empty.Code != http.StatusOK {
		t.Fatalf("empty list status = %d, want 200", empty.Code)
	}
	if !strings.Contains(empty.Body.String(), "No definitions match") {
		t.Errorf("empty list does not say so: %s", empty.Body.String())
	}
}

// AC-10, AC-12: the editor renders, shows the slug read-only (no control named
// slug), and 404s an absent or deleted definition.
func TestTaskDefinitionEditorRendersAndHidesSlugControl(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "editor")
	insertDefinition(t, ctx, slug, "Editor TDE", "a description", "", false, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/task-definitions/"+slug, nil)
	req.SetPathValue("slug", slug)
	handleTaskDefinitionEditor(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("editor status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{slug, "Editor TDE", "a description", `name="display_name"`, `name="is_container"`, `name="is_active"`} {
		if !strings.Contains(body, want) {
			t.Errorf("editor is missing %q", want)
		}
	}
	if strings.Contains(body, `name="slug"`) {
		t.Error("the editor renders a control named slug; the slug is read-only after creation")
	}

	missing := httptest.NewRecorder()
	missingReq := httptest.NewRequest(http.MethodGet, "/task-definitions/"+tdeSlug(t, "ghost"), nil)
	missingReq.SetPathValue("slug", tdeSlug(t, "ghost2"))
	handleTaskDefinitionEditor(missing, missingReq)
	if missing.Code != http.StatusNotFound {
		t.Errorf("absent slug status = %d, want 404", missing.Code)
	}

	deletedSlug := tdeSlug(t, "gone")
	insertDefinition(t, ctx, deletedSlug, "Gone TDE", "", "", false, true)
	if _, err := db.Pool.Exec(ctx, `UPDATE base_task_definition SET _deleted_at = NOW() WHERE slug = $1`, deletedSlug); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	gone := httptest.NewRecorder()
	goneReq := httptest.NewRequest(http.MethodGet, "/task-definitions/"+deletedSlug, nil)
	goneReq.SetPathValue("slug", deletedSlug)
	handleTaskDefinitionEditor(gone, goneReq)
	if gone.Code != http.StatusNotFound {
		t.Errorf("soft-deleted slug status = %d, want 404", gone.Code)
	}
}

// AC-63, AC-66: creating a definition through the handler redirects to the
// editor, refuses an unsafe slug, and refuses a duplicate slug by name.
func TestTaskDefinitionCreateHandler(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "handlecreate")

	post := func(values url.Values) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/task-definitions", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		handleTaskDefinitionCreate(rec, req)
		return rec
	}

	rec := post(url.Values{"slug": {slug}, "display_name": {"Handled TDE"}, "description": {"made in a test"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/task-definitions/"+slug {
		t.Errorf("create redirect = %q, want the editor", got)
	}
	if _, err := db.GetTaskDefinitionBySlug(ctx, slug); err != nil {
		t.Fatalf("created definition is not readable: %v", err)
	}

	unsafe := post(url.Values{"slug": {"Not_Valid"}, "display_name": {"Unsafe"}})
	if unsafe.Code != http.StatusBadRequest {
		t.Errorf("unsafe slug status = %d, want 400", unsafe.Code)
	}
	if !strings.Contains(unsafe.Body.String(), "lowercase letters, digits and hyphens") {
		t.Errorf("unsafe slug refusal does not describe the rule: %s", unsafe.Body.String())
	}

	duplicate := post(url.Values{"slug": {slug}, "display_name": {"Duplicate TDE"}})
	if duplicate.Code != http.StatusBadRequest {
		t.Errorf("duplicate slug status = %d, want 400", duplicate.Code)
	}
	if !strings.Contains(duplicate.Body.String(), "A definition with slug "+slug+" already exists") {
		t.Errorf("duplicate refusal does not name the slug: %s", duplicate.Body.String())
	}
}

// AC-7: the menu carries the entry, and it sits before Docs.
func TestMenuCarriesTaskDefinitionsBesideDocs(t *testing.T) {
	taskDefinitionsTestDB(t)

	if err := loadMenuFromDB(); err != nil {
		t.Fatalf("loadMenuFromDB: %v", err)
	}
	definitionsAt, docsAt := -1, -1
	for i, item := range menuItems {
		if item.Href == "/task-definitions" {
			definitionsAt = i
			if item.Title != "Task Studio" {
				t.Errorf("menu entry title = %q, want \"Task Studio\"", item.Title)
			}
		}
		if item.Href == "/docs" {
			docsAt = i
		}
	}
	if definitionsAt < 0 {
		t.Fatal("the menu has no /task-definitions entry")
	}
	if docsAt >= 0 && definitionsAt > docsAt {
		t.Errorf("Task Definitions is after Docs in the menu (index %d vs %d)", definitionsAt, docsAt)
	}
}
