package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// seedFlatGHIssueTask inserts a flat task row of the shape the web form
// produced on 2026-11-08: definition_id NULL, no variables, titled
// "GH-<n> <issue title>".
func seedFlatGHIssueTask(ctx context.Context, t *testing.T, title string) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task (title, work_type, state)
		VALUES ($1, 'TASK', 'ready')
		RETURNING _id::text`, title).Scan(&id); err != nil {
		t.Fatalf("seed flat task %q: %v", title, err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM base_task WHERE _id = $1`, id)
	})
	return id
}

// TestRecreateGitHubIssueTasks covers issue #226 end to end: a flat GH-<n>
// row is replaced by a definition instance carrying the issue title, URL and
// body, its intake child is routed, the flat twin is closed with a pointer,
// and a second run creates and closes nothing.
func TestRecreateGitHubIssueTasks(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := instanceTestSuffix(t)

	intakeUser, intakeGroup := seedSecondActor(ctx, t, "intake")
	intakeDef := seedDefinition(ctx, t, "gh-intake-"+suffix, "Intake Validation", false, "")
	if _, err := Pool.Exec(ctx, `UPDATE base_task_definition SET default_group_id = $2, default_assignee_id = $3 WHERE _id = $1`,
		intakeDef, intakeGroup, intakeUser); err != nil {
		t.Fatalf("route intake definition: %v", err)
	}
	rootDef := seedDefinition(ctx, t, "gh-feature-request-"+suffix, "Feature Request", true, "")
	seedChildLink(ctx, t, rootDef, intakeDef, 10, nil)

	// Issue numbers are matched by title across the whole database (that is
	// the point: the flat rows are the live population), so a test run must
	// not collide with another run's rows. Derive numbers that no GitHub
	// issue will have.
	issueNumber := int(time.Now().UnixNano()%1_000_000) + 500000
	otherNumber := issueNumber + 1

	flatID := seedFlatGHIssueTask(ctx, t, fmt.Sprintf("GH-%d Flat thing", issueNumber))

	issueURL := fmt.Sprintf("https://github.com/andywithcamera/velm/issues/%d", issueNumber)
	issues := []GitHubIssue{{
		Number: issueNumber,
		Title:  "Flat thing",
		Body:   "It is flat and it should not be.",
		URL:    issueURL,
	}}

	results, err := RecreateGitHubIssueTasks(ctx, issues, "gh-feature-request-"+suffix, intakeUser)
	if err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if len(results) != 1 || results[0].ReplacementID == "" {
		t.Fatalf("results = %#v, want one replacement instance", results)
	}

	var title string
	var vars map[string]any
	if err := Pool.QueryRow(ctx, `SELECT title, variables FROM base_task WHERE _id = $1`, results[0].ReplacementID).
		Scan(&title, &vars); err != nil {
		t.Fatalf("read replacement: %v", err)
	}
	if want := fmt.Sprintf("GH-%d Flat thing (TASK-", issueNumber); !strings.HasPrefix(title, want) {
		t.Fatalf("replacement title = %q, want it to start %q", title, want)
	}
	inputs, ok := vars["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("replacement variables.inputs = %#v, want an object", vars["inputs"])
	}
	if inputs["github_url"] != issueURL {
		t.Fatalf("inputs.github_url = %#v, want %q", inputs["github_url"], issueURL)
	}
	if inputs["body"] != issues[0].Body {
		t.Fatalf("inputs.body = %#v, want the issue body", inputs["body"])
	}

	// The intake child is assigned per the definition's routing.
	var intakeAssignee, intakeState, intakeDefID, intakeGroupGot string
	if err := Pool.QueryRow(ctx, `
		SELECT COALESCE(assigned_user_id::text, ''), state,
		       COALESCE(definition_id::text, ''), COALESCE(assignment_group_id::text, '')
		FROM base_task
		WHERE parent_task_id = $1 AND _deleted_at IS NULL`, results[0].ReplacementID).
		Scan(&intakeAssignee, &intakeState, &intakeDefID, &intakeGroupGot); err != nil {
		t.Fatalf("read intake child: %v", err)
	}
	if intakeAssignee != intakeUser {
		t.Fatalf("intake assignee = %q, want %q (def %s, group %s, state %s)", intakeAssignee, intakeUser, intakeDefID, intakeGroupGot, intakeState)
	}

	// The flat twin is closed, not deleted, and points at its replacement.
	var flatState, flatReason, flatDescription string
	if err := Pool.QueryRow(ctx, `
		SELECT state, COALESCE(closure_reason, ''), COALESCE(description, '')
		FROM base_task WHERE _id = $1`, flatID).Scan(&flatState, &flatReason, &flatDescription); err != nil {
		t.Fatalf("read flat twin: %v", err)
	}
	if flatState != taskStateDone {
		t.Fatalf("flat twin state = %q, want closed", flatState)
	}
	if flatReason != "completed" {
		t.Fatalf("flat twin closure_reason = %q, want completed", flatReason)
	}
	if !strings.Contains(flatDescription, "Superseded by ") {
		t.Fatalf("flat twin description = %q, want a pointer to its replacement", flatDescription)
	}

	// Second run: idempotent. The replacement already exists, the twin is
	// closed, so nothing is spawned and nothing is closed again.
	again, err := RecreateGitHubIssueTasks(ctx, issues, "gh-feature-request-"+suffix, intakeUser)
	if err != nil {
		t.Fatalf("second recreate: %v", err)
	}
	if len(again) != 1 || again[0].ReplacementID != results[0].ReplacementID {
		t.Fatalf("second run results = %#v, want the same replacement and no new spawn", again)
	}
	if len(again[0].ClosedTwinIDs) != 0 {
		t.Fatalf("second run closed %v, want nothing", again[0].ClosedTwinIDs)
	}
	var count int
	if err := Pool.QueryRow(ctx, `SELECT count(*) FROM base_task
		WHERE _deleted_at IS NULL AND title LIKE $1`, fmt.Sprintf("GH-%d %%%%", issueNumber)).Scan(&count); err != nil {
		t.Fatalf("count GH-1 tasks: %v", err)
	}
	if count != 2 { // the closed flat twin and the one replacement
		t.Fatalf("GH-1 task rows = %d, want 2 (replacement + closed twin)", count)
	}

	// An issue with no flat twin and no replacement spawns one and closes
	// nothing: the population has drifted since the rows were made.
	only := []GitHubIssue{{
		Number: otherNumber, Title: "Never had a twin", Body: "b",
		URL: fmt.Sprintf("https://example.test/%d", otherNumber),
	}}
	fresh, err := RecreateGitHubIssueTasks(ctx, only, "gh-feature-request-"+suffix, intakeUser)
	if err != nil {
		t.Fatalf("recreate without twin: %v", err)
	}
	if fresh[0].ReplacementID == "" || len(fresh[0].ClosedTwinIDs) != 0 {
		t.Fatalf("results without a twin = %#v", fresh[0])
	}
	_ = time.Now
}
