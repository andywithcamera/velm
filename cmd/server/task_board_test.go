package main

import (
	"bytes"
	"html/template"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskBoardRouteTargetsBaseTask(t *testing.T) {
	route := taskBoardRoute()
	if route.RouteName != "base_task" {
		t.Fatalf("taskBoardRoute().RouteName = %q, want %q", route.RouteName, "base_task")
	}
	if route.TableName != "base_task" {
		t.Fatalf("taskBoardRoute().TableName = %q, want %q", route.TableName, "base_task")
	}
}

func TestTaskBoardURLsUseDedicatedBoardPath(t *testing.T) {
	route := taskBoardRoute()
	if got := taskBoardURL(); got != "/task" {
		t.Fatalf("taskBoardURL() = %q, want %q", got, "/task")
	}
	if got := taskBoardListURL(route); got != "/t/base_task" {
		t.Fatalf("taskBoardListURL() = %q, want %q", got, "/t/base_task")
	}
}

func TestTaskBoardBulkUpdateURLUsesActualTable(t *testing.T) {
	if got := taskBoardBulkUpdateURL("dw_story"); got != "/api/bulk/update/dw_story" {
		t.Fatalf("taskBoardBulkUpdateURL(dw_story) = %q, want %q", got, "/api/bulk/update/dw_story")
	}
	if got := taskBoardBulkUpdateURL(""); got != "/api/bulk/update/base_task" {
		t.Fatalf("taskBoardBulkUpdateURL(\"\") = %q, want %q", got, "/api/bulk/update/base_task")
	}
}

func TestNormalizeTaskBoardScopeDefaultsToMine(t *testing.T) {
	if got := normalizeTaskBoardScope("anything"); got != taskBoardScopeMine {
		t.Fatalf("normalizeTaskBoardScope() = %q, want %q", got, taskBoardScopeMine)
	}
	if got := normalizeTaskBoardScope(taskBoardScopeGroups); got != taskBoardScopeGroups {
		t.Fatalf("normalizeTaskBoardScope(groups) = %q, want %q", got, taskBoardScopeGroups)
	}
}

func TestNormalizeTaskBoardGroupRejectsUnknownSelection(t *testing.T) {
	groups := []taskBoardGroupOption{
		{ID: "group-a", Label: "Group A"},
		{ID: "group-b", Label: "Group B"},
	}

	if got := normalizeTaskBoardGroup("group-b", groups); got != "group-b" {
		t.Fatalf("normalizeTaskBoardGroup(valid) = %q, want %q", got, "group-b")
	}
	if got := normalizeTaskBoardGroup("group-x", groups); got != "" {
		t.Fatalf("normalizeTaskBoardGroup(invalid) = %q, want empty string", got)
	}
}

func TestNormalizeTaskBoardTypeRejectsUnknownSelection(t *testing.T) {
	options := []taskBoardChoice{
		{Value: "", Label: "Any Type"},
		{Value: "TASK", Label: "TASK"},
	}

	if got := normalizeTaskBoardType("TASK", options); got != "TASK" {
		t.Fatalf("normalizeTaskBoardType(valid) = %q, want %q", got, "TASK")
	}
	if got := normalizeTaskBoardType("INCIDENT", options); got != "" {
		t.Fatalf("normalizeTaskBoardType(invalid) = %q, want empty string", got)
	}
}

func TestBuildTaskBoardLanesUsesChoiceOrderAndAppendsUnknownStates(t *testing.T) {
	choices := []taskBoardChoice{
		{Value: "new", Label: "New"},
		{Value: "ready_to_close", Label: "Ready to Close"},
	}
	items := []taskBoardItem{
		{State: normalizeTaskBoardLaneValue("pending"), Card: taskBoardCard{ID: "1"}},
		{State: normalizeTaskBoardLaneValue("new"), Card: taskBoardCard{ID: "2"}},
		{State: normalizeTaskBoardLaneValue("ready_to_close"), Card: taskBoardCard{ID: "3"}},
	}

	lanes := buildTaskBoardLanes(choices, items)
	if len(lanes) != 3 {
		t.Fatalf("len(buildTaskBoardLanes()) = %d, want 3", len(lanes))
	}
	if lanes[0].Value != "new" || lanes[0].Count != 1 {
		t.Fatalf("lane[0] = %+v, want new with one task", lanes[0])
	}
	if !lanes[0].CanDrop {
		t.Fatalf("expected new lane to be droppable: %+v", lanes[0])
	}
	if lanes[1].Value != "ready_to_close" || lanes[1].Count != 1 {
		t.Fatalf("lane[1] = %+v, want ready_to_close with one task", lanes[1])
	}
	if !lanes[1].CanDrop {
		t.Fatalf("expected ready_to_close lane to be droppable: %+v", lanes[1])
	}
	if lanes[2].Value != "pending" || lanes[2].Count != 1 {
		t.Fatalf("lane[2] = %+v, want pending with one task", lanes[2])
	}
	if lanes[2].CanDrop {
		t.Fatalf("expected unknown lane to be read-only: %+v", lanes[2])
	}
}

func TestTaskBoardShowsStateOmitsClosedStates(t *testing.T) {
	if taskBoardShowsState("closed") {
		t.Fatal("expected closed to be hidden from /task")
	}
	if taskBoardShowsState("done") {
		t.Fatal("expected done to be hidden from /task")
	}
	if !taskBoardShowsState("ready_to_close") {
		t.Fatal("expected ready_to_close to remain visible on /task")
	}
}

func TestTemplatesParseWithTaskBoardBranch(t *testing.T) {
	tmpl, err := template.ParseGlob(filepath.Join("..", "..", "web", "templates", "*.html"))
	if err != nil {
		t.Fatalf("ParseGlob(main templates) error = %v", err)
	}
	if _, err := tmpl.ParseGlob(filepath.Join("..", "..", "web", "templates", "components", "*.html")); err != nil {
		t.Fatalf("ParseGlob(component templates) error = %v", err)
	}
}

func TestTaskBoardTemplateRendersCleanCardHref(t *testing.T) {
	tmpl, err := template.ParseGlob(filepath.Join("..", "..", "web", "templates", "*.html"))
	if err != nil {
		t.Fatalf("ParseGlob(main templates) error = %v", err)
	}
	if _, err := tmpl.ParseGlob(filepath.Join("..", "..", "web", "templates", "components", "*.html")); err != nil {
		t.Fatalf("ParseGlob(component templates) error = %v", err)
	}

	data := map[string]any{
		"TableName":                 "base_task",
		"TableRouteName":            "base_task",
		"TableLabel":                "Tasks",
		"TaskBoard":                 true,
		"TaskBoardSummary":          "Tasks assigned to me",
		"TaskBoardPath":             "/task",
		"TaskBoardResetURL":         "/task",
		"TaskBoardListURL":          "/t/base_task",
		"TaskBoardUpdateURL":        "/api/bulk/update/base_task",
		"TaskBoardCanUpdate":        true,
		"TaskBoardScope":            "mine",
		"TaskBoardSelectedGroupID":  "",
		"TaskBoardType":             "",
		"TaskBoardTypeOptions":      []taskBoardChoice{{Value: "", Label: "Any Type"}, {Value: "TASK", Label: "TASK"}},
		"TaskBoardPriority":         "",
		"TaskBoardPriorityOptions":  []taskBoardChoice{{Value: "", Label: "Any Priority"}},
		"TaskBoardAssignedTo":       "",
		"TaskBoardAssigneeOptions":  []taskBoardChoice{{Value: "", Label: "Anyone"}},
		"TaskBoardSourceTables":     []string{"base_task", "dw_story_stream"},
		"ServerQuery":               "",
		"QueryColumns":              []tableQueryColumn{{Name: "title", Label: "Title", DataType: "text", InputKind: "text"}},
		"TaskBoardHasActiveFilters": false,
		"TaskBoardHasGroups":        false,
		"TaskBoardGroups":           []taskBoardGroupOption{},
		"TaskBoardGroupMembers":     map[string][]taskBoardUserOption{},
		"TaskBoardTotalTasks":       1,
		"TaskBoardLanes": []taskBoardLane{
			{
				Value:   "new",
				Label:   "New",
				Count:   1,
				CanDrop: true,
				Cards: []taskBoardCard{
					{
						ID:             "89ba09f0-4296-4d79-8c94-0128b2cfc3f4",
						Href:           "/f/dw_story/89ba09f0-4296-4d79-8c94-0128b2cfc3f4",
						UpdateURL:      "/api/bulk/update/dw_story",
						Title:          "Example task",
						AttentionLabel: "Overdue",
						AttentionClass: "ui-badge-danger",
						AssignedUserID: "550e8400-e29b-41d4-a716-446655440000",
						CanDrag:        true,
					},
				},
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "task-board-view", data); err != nil {
		t.Fatalf("ExecuteTemplate(task-board-view) error = %v", err)
	}

	rendered := out.String()
	if !strings.Contains(rendered, `href="/f/dw_story/89ba09f0-4296-4d79-8c94-0128b2cfc3f4"`) {
		t.Fatalf("expected clean task href, got %q", rendered)
	}
	if strings.Contains(rendered, `window.navigateWithTransition(&#34;/f/dw_story/89ba09f0-4296-4d79-8c94-0128b2cfc3f4&#34;)`) {
		t.Fatalf("unexpected quoted inline onclick remained in output: %q", rendered)
	}
	if !strings.Contains(rendered, `data-update-url="/api/bulk/update/base_task"`) {
		t.Fatalf("expected board update URL in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `data-update-url="/api/bulk/update/dw_story"`) {
		t.Fatalf("expected card-specific update URL in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `action="/task"`) {
		t.Fatalf("expected board filters to submit to /task, got %q", rendered)
	}
	if !strings.Contains(rendered, `name="priority"`) {
		t.Fatalf("expected priority filter in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `name="work_type"`) {
		t.Fatalf("expected work_type filter in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `name="assigned_to"`) {
		t.Fatalf("expected assigned_to filter in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `id="serverFilterInput"`) {
		t.Fatalf("expected shared query input in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `id="querySuggestions"`) {
		t.Fatalf("expected shared query suggestions container in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `placeholder="Filter tasks"`) {
		t.Fatalf("expected generic board query placeholder in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `const realtimeTables = [`) {
		t.Fatalf("expected realtime table list in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `dw_story_stream`) {
		t.Fatalf("expected descendant task table in realtime source list, got %q", rendered)
	}
	if !strings.Contains(rendered, `new EventSource(`) || !strings.Contains(rendered, `/api/realtime/stream?table=`) {
		t.Fatalf("expected realtime event source setup in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `draggable="true"`) {
		t.Fatalf("expected draggable cards in rendered output, got %q", rendered)
	}
	if strings.Contains(rendered, `WIP `) {
		t.Fatalf("did not expect WIP limit copy in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `Overdue`) {
		t.Fatalf("expected attention badge in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `data-lane-empty hidden`) {
		t.Fatalf("expected populated lane empty state to use hidden attribute, got %q", rendered)
	}
}

func TestTaskBoardTemplateFormatsUnassignedGroupAndShowsAssigneePicker(t *testing.T) {
	tmpl, err := template.ParseGlob(filepath.Join("..", "..", "web", "templates", "*.html"))
	if err != nil {
		t.Fatalf("ParseGlob(main templates) error = %v", err)
	}
	if _, err := tmpl.ParseGlob(filepath.Join("..", "..", "web", "templates", "components", "*.html")); err != nil {
		t.Fatalf("ParseGlob(component templates) error = %v", err)
	}

	data := map[string]any{
		"TableName":                 "base_task",
		"TableRouteName":            "base_task",
		"TableLabel":                "Tasks",
		"TaskBoard":                 true,
		"TaskBoardSummary":          "Tasks assigned to me plus unassigned work from my groups",
		"TaskBoardPath":             "/task",
		"TaskBoardResetURL":         "/task",
		"TaskBoardListURL":          "/t/base_task",
		"TaskBoardUpdateURL":        "/api/bulk/update/base_task",
		"TaskBoardCanUpdate":        true,
		"TaskBoardScope":            "mine",
		"TaskBoardSelectedGroupID":  "",
		"TaskBoardType":             "",
		"TaskBoardTypeOptions":      []taskBoardChoice{{Value: "", Label: "Any Type"}, {Value: "TASK", Label: "TASK"}},
		"TaskBoardPriority":         "",
		"TaskBoardPriorityOptions":  []taskBoardChoice{{Value: "", Label: "Any Priority"}},
		"TaskBoardAssignedTo":       "",
		"TaskBoardAssigneeOptions":  []taskBoardChoice{{Value: "", Label: "Anyone"}},
		"TaskBoardSourceTables":     []string{"base_task"},
		"ServerQuery":               "",
		"QueryColumns":              []tableQueryColumn{{Name: "title", Label: "Title", DataType: "text", InputKind: "text"}},
		"TaskBoardHasActiveFilters": false,
		"TaskBoardHasGroups":        true,
		"TaskBoardGroups":           []taskBoardGroupOption{{ID: "group-1", Label: "Test Group"}},
		"TaskBoardGroupMembers": map[string][]taskBoardUserOption{
			"group-1": {
				{ID: "user-1", Label: "Andy Doyle"},
			},
		},
		"TaskBoardTotalTasks": 1,
		"TaskBoardLanes": []taskBoardLane{
			{
				Value:   "new",
				Label:   "New",
				Count:   1,
				CanDrop: true,
				Cards: []taskBoardCard{
					{
						ID:             "89ba09f0-4296-4d79-8c94-0128b2cfc3f4",
						Href:           "/f/base_task/89ba09f0-4296-4d79-8c94-0128b2cfc3f4",
						UpdateURL:      "/api/bulk/update/base_task",
						Title:          "Example task",
						GroupID:        "group-1",
						GroupLabel:     "Test Group",
						AssignedUserID: "",
						CanDrag:        false,
					},
				},
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "task-board-view", data); err != nil {
		t.Fatalf("ExecuteTemplate(task-board-view) error = %v", err)
	}

	rendered := out.String()
	if !strings.Contains(rendered, `Test Group |`) {
		t.Fatalf("expected formatted group separator in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `>Unassigned</button>`) {
		t.Fatalf("expected unassigned assignment trigger in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `>Andy Doyle</option>`) {
		t.Fatalf("expected assignment picker option in rendered output, got %q", rendered)
	}
	if strings.Contains(rendered, `Unassigned</span>`) {
		t.Fatalf("did not expect an unassigned insight chip in rendered output, got %q", rendered)
	}
}

func TestTaskBoardTemplateShowsEmptyStateOnlyForEmptyLane(t *testing.T) {
	tmpl, err := template.ParseGlob(filepath.Join("..", "..", "web", "templates", "*.html"))
	if err != nil {
		t.Fatalf("ParseGlob(main templates) error = %v", err)
	}
	if _, err := tmpl.ParseGlob(filepath.Join("..", "..", "web", "templates", "components", "*.html")); err != nil {
		t.Fatalf("ParseGlob(component templates) error = %v", err)
	}

	data := map[string]any{
		"TableName":                 "base_task",
		"TableRouteName":            "base_task",
		"TableLabel":                "Tasks",
		"TaskBoard":                 true,
		"TaskBoardSummary":          "Tasks assigned to me",
		"TaskBoardPath":             "/task",
		"TaskBoardResetURL":         "/task",
		"TaskBoardListURL":          "/t/base_task",
		"TaskBoardUpdateURL":        "/api/bulk/update/base_task",
		"TaskBoardCanUpdate":        true,
		"TaskBoardScope":            "mine",
		"TaskBoardSelectedGroupID":  "",
		"TaskBoardType":             "",
		"TaskBoardTypeOptions":      []taskBoardChoice{{Value: "", Label: "Any Type"}, {Value: "TASK", Label: "TASK"}},
		"TaskBoardPriority":         "",
		"TaskBoardPriorityOptions":  []taskBoardChoice{{Value: "", Label: "Any Priority"}},
		"TaskBoardAssignedTo":       "",
		"TaskBoardAssigneeOptions":  []taskBoardChoice{{Value: "", Label: "Anyone"}},
		"TaskBoardSourceTables":     []string{"base_task"},
		"ServerQuery":               "",
		"QueryColumns":              []tableQueryColumn{{Name: "title", Label: "Title", DataType: "text", InputKind: "text"}},
		"TaskBoardHasActiveFilters": false,
		"TaskBoardHasGroups":        false,
		"TaskBoardGroups":           []taskBoardGroupOption{},
		"TaskBoardGroupMembers":     map[string][]taskBoardUserOption{},
		"TaskBoardTotalTasks":       0,
		"TaskBoardLanes": []taskBoardLane{
			{
				Value:   "pending",
				Label:   "Pending",
				Count:   0,
				CanDrop: true,
				Cards:   nil,
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "task-board-view", data); err != nil {
		t.Fatalf("ExecuteTemplate(task-board-view) error = %v", err)
	}

	rendered := out.String()
	if !strings.Contains(rendered, `data-lane-empty>No tasks</div>`) {
		t.Fatalf("expected empty lane to render visible empty state, got %q", rendered)
	}
	if strings.Contains(rendered, `data-lane-empty hidden`) {
		t.Fatalf("did not expect hidden attribute for empty lane, got %q", rendered)
	}
}
