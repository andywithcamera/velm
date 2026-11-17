package main

// Control tasks on the board (Process Designer, Requirements AC-39 to AC-42).
//
// Control blocks are structure, not work: a person's default list shows the
// tasks they can act on, and a run's `if`/`end` rows appear only when asked for
// with ?work_type=CONTROL. The type filter offers CONTROL only once a control
// task exists. Open-work counts are deliberately untouched.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"velm/internal/auth"
	"velm/internal/db"
)

// boardControlFixture seeds a user in a group plus one run: a container task,
// a control `if`, and one leaf task, all reachable through the group.
func boardControlFixture(t *testing.T, ctx context.Context) string {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID, groupID string
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email) VALUES ($1, $2) RETURNING _id::text`,
		"board-control-"+suffix, "board-control-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO _group (name, description, is_system)
		VALUES ($1, 'temporary', FALSE) RETURNING _id::text`,
		"board-control-group-"+suffix).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO _group_membership (group_id, user_id) VALUES ($1, $2)`,
		groupID, userID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = db.Pool.Exec(bg, `DELETE FROM base_task WHERE assignment_group_id = $1`, groupID)
		_, _ = db.Pool.Exec(bg, `DELETE FROM _group_membership WHERE group_id = $1`, groupID)
		_, _ = db.Pool.Exec(bg, `DELETE FROM _group WHERE _id = $1`, groupID)
		_, _ = db.Pool.Exec(bg, `DELETE FROM _user WHERE _id = $1`, userID)
	})

	insertTask := func(title, workType string, parent any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO base_task (title, work_type, assignment_group_id, parent_task_id, state)
			VALUES ($1, $2, $3, $4, 'pending')`,
			title, workType, groupID, parent); err != nil {
			t.Fatalf("seed task %q: %v", title, err)
		}
	}
	var rootID string
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO base_task (title, work_type, assignment_group_id, state)
		VALUES ('Upgrade OS', 'TASK', $1, 'pending')
		RETURNING _id::text`, groupID).Scan(&rootID); err != nil {
		t.Fatalf("seed root task: %v", err)
	}
	insertTask("Upgrade OS · If exit_code == 0", "CONTROL", rootID)
	insertTask("Reboot the host", "TASK", rootID)
	return userID
}

func getBoard(t *testing.T, userID, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/task"+rawQuery, nil)
	req = req.WithContext(auth.WithUserContext(req.Context(), userID, "", "", "admin"))
	table := db.GetTable("base_task")
	if table.ID == "" {
		t.Fatalf("base_task table metadata missing")
	}
	handleTaskBoardView(rec, req, taskBoardRoute(), table)
	return rec
}

// AC-39 and AC-40: the default board lists the work and hides control rows;
// asking for work_type=CONTROL returns them.
func TestTaskBoardHidesControlTasks(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	userID := boardControlFixture(t, ctx)

	rec := getBoard(t, userID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("board = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Reboot the host") {
		t.Fatalf("default board omits the leaf task: %s", body)
	}
	if strings.Contains(body, "Upgrade OS · If exit_code == 0") {
		t.Fatalf("default board shows a CONTROL card: %s", body)
	}

	rec = getBoard(t, userID, "?work_type=CONTROL")
	if rec.Code != http.StatusOK {
		t.Fatalf("control board = %d, want 200", rec.Code)
	}
	body = rec.Body.String()
	if !strings.Contains(body, "Upgrade OS · If exit_code == 0") {
		t.Fatalf("explicit CONTROL filter does not return the control card: %s", body)
	}
	if strings.Contains(body, "Reboot the host") {
		t.Fatalf("explicit CONTROL filter still lists work tasks: %s", body)
	}
}

// AC-41: CONTROL is offered in the type filter only when a control task exists.
func TestTaskBoardTypeOptionsOfferControlOnlyWhenPresent(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	boardControlFixture(t, ctx)

	options := loadTaskBoardTypeOptions(ctx, []string{"base_task"})
	if !boardHasTypeOption(options, "CONTROL") {
		t.Fatalf("type options omit CONTROL while a control task exists: %+v", options)
	}

	// Remove every control row, including runs left behind by other tests:
	// the query must offer CONTROL from what exists, not from the schema.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM base_task WHERE work_type = 'CONTROL'`); err != nil {
		t.Fatalf("remove control rows: %v", err)
	}
	options = loadTaskBoardTypeOptions(ctx, []string{"base_task"})
	if boardHasTypeOption(options, "CONTROL") {
		t.Fatalf("type options offer CONTROL with no control task in the database: %+v", options)
	}
}

func boardHasTypeOption(options []taskBoardChoice, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

// AC-42: the insight builder has no work_type predicate, so a run's control
// rows are counted in its totals like any other row. Load the control rows
// through the board query, mark each one overdue, and confirm the count is
// every row returned rather than the work tasks alone.
func TestTaskBoardInsightsCountControlTasks(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	userID := boardControlFixture(t, ctx)

	queryContext, err := loadTaskBoardQueryContext(ctx, "base_task")
	if err != nil {
		t.Fatalf("load board query context: %v", err)
	}
	items, err := loadTaskBoardItems(ctx, userID, taskBoardFilters{
		Scope: taskBoardScopeMine,
		Type:  "CONTROL",
	}, nil, queryContext)
	if err != nil {
		t.Fatalf("load control items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("CONTROL filter returned %d items, want the one control task", len(items))
	}
	for i := range items {
		items[i].Card.AttentionLabel = "Overdue"
	}
	insights := buildTaskBoardInsights(items)
	overdue := 0
	for _, insight := range insights {
		if insight.Label == "Overdue" {
			overdue = insight.Count
		}
	}
	if overdue != len(items) {
		t.Fatalf("insights counted %d overdue rows, want %d: the builder dropped control tasks", overdue, len(items))
	}
}
