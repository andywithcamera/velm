package main

// The definition editor's Assignment section (Process Designer, Requirements
// AC-67 and AC-68): a group select and a strategy select on the page, saved
// through the existing basics endpoint and rendered back.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"velm/internal/db"
)

func assignmentEditorGroup(t *testing.T, ctx context.Context) (string, string) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "Assignment Editor " + suffix
	var groupID string
	if err := db.Pool.QueryRow(ctx, `INSERT INTO _group (name) VALUES ($1) RETURNING _id::text`, name).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM base_task_definition WHERE default_group_id = $1`, groupID)
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM _group WHERE _id = $1`, groupID)
	})
	return groupID, name
}

// AC-67: the editor renders an Assignment section with both selects and the
// six strategies plus the inherit/default option.
func TestDefinitionEditorAssignmentSection(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "assign-section")
	insertDefinition(t, ctx, slug, "Assignment Section TDE", "", "", false, true)
	_, groupName := assignmentEditorGroup(t, ctx)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/task-definitions/"+slug, nil)
	req.SetPathValue("slug", slug)
	handleTaskDefinitionEditor(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("editor status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="assignment"`) {
		t.Error("editor has no Assignment section")
	}
	if !strings.Contains(body, `name="default_group_id"`) {
		t.Error("Assignment section has no group select")
	}
	if !strings.Contains(body, `name="assignment_strategy"`) {
		t.Error("Assignment section has no strategy select")
	}
	if !strings.Contains(body, "Inherit / default") {
		t.Error("strategy select has no inherit/default option")
	}
	for _, strategy := range db.AssignmentStrategies {
		if !strings.Contains(body, `value="`+strategy+`"`) {
			t.Errorf("strategy select is missing %q", strategy)
		}
	}
	if !strings.Contains(body, groupName) {
		t.Errorf("group select does not list %q", groupName)
	}
}

// AC-68: saving the section writes the columns and a reload renders them.
func TestDefinitionAssignmentSaveRoundTrip(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "assign-save")
	insertDefinition(t, ctx, slug, "Assignment Save TDE", "", "", false, true)
	groupID, _ := assignmentEditorGroup(t, ctx)

	form := url.Values{
		"display_name":        {"Assignment Save TDE"},
		"default_group_id":    {groupID},
		"assignment_strategy": {db.AssignmentRoundRobin},
		"is_active":           {"yes"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/basics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	handleTaskDefinitionBasicsSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	row, err := db.GetTaskDefinitionBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.DefaultGroupID != groupID {
		t.Fatalf("stored group = %q, want %q", row.DefaultGroupID, groupID)
	}
	if row.AssignmentStrategy != db.AssignmentRoundRobin {
		t.Fatalf("stored strategy = %q, want %q", row.AssignmentStrategy, db.AssignmentRoundRobin)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/task-definitions/"+slug, nil)
	req.SetPathValue("slug", slug)
	handleTaskDefinitionEditor(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reload status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<option value="`+groupID+`" selected`) {
		t.Error("reload does not mark the saved group selected")
	}
	if !strings.Contains(body, `<option value="`+db.AssignmentRoundRobin+`" selected`) {
		t.Error("reload does not mark the saved strategy selected")
	}
}

// A strategy outside the six is refused before the database, with words.
func TestDefinitionAssignmentRefusesUnknownStrategy(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "assign-bad")
	insertDefinition(t, ctx, slug, "Assignment Bad TDE", "", "", false, true)

	form := url.Values{
		"display_name":        {"Assignment Bad TDE"},
		"assignment_strategy": {"least_loaded"},
		"is_active":           {"yes"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/basics", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	handleTaskDefinitionBasicsSave(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Assignment strategy must be one of") {
		t.Errorf("refusal does not name the allowed strategies: %s", rec.Body.String())
	}
	row, _ := db.GetTaskDefinitionBySlug(ctx, slug)
	if row.AssignmentStrategy != "" {
		t.Fatalf("refused save stored strategy %q", row.AssignmentStrategy)
	}
}
