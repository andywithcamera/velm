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

func TestBuildTaskBoardLanesIncludesCanonicalStatesAlways(t *testing.T) {
	// Only "ready" is defined by the column; the canonical four still
	// render, in workflow order, droppable, even when empty.
	choices := []taskBoardChoice{
		{Value: "ready", Label: "Ready"},
	}
	items := []taskBoardItem{
		{State: normalizeTaskBoardLaneValue("pending"), Card: taskBoardCard{ID: "1"}},
		{State: normalizeTaskBoardLaneValue("pending"), Card: taskBoardCard{ID: "2"}},
		{State: normalizeTaskBoardLaneValue("closed"), Card: taskBoardCard{ID: "3"}},
	}

	lanes := buildTaskBoardLanes(choices, items)
	wantOrder := []string{"pending", "ready", "in_progress", "closed"}
	if len(lanes) != len(wantOrder) {
		t.Fatalf("len(buildTaskBoardLanes()) = %d, want %d", len(lanes), len(wantOrder))
	}
	for i, want := range wantOrder {
		if lanes[i].Value != want {
			t.Fatalf("lane[%d] = %q, want %q", i, lanes[i].Value, want)
		}
		if !lanes[i].CanDrop {
			t.Fatalf("lane[%d] (%s) should be droppable: %+v", i, want, lanes[i])
		}
	}
	if lanes[0].Count != 2 || lanes[1].Count != 0 || lanes[2].Count != 0 || lanes[3].Count != 1 {
		t.Fatalf("counts = %d/%d/%d/%d, want 2/0/0/1", lanes[0].Count, lanes[1].Count, lanes[2].Count, lanes[3].Count)
	}
	if lanes[1].Label != "Ready" {
		t.Fatalf("custom label lost: %q", lanes[1].Label)
	}
}

func TestBuildTaskBoardLanesKeepsCustomStatesAfterWorkflow(t *testing.T) {
	choices := []taskBoardChoice{
		{Value: "blocked", Label: "Blocked"},
	}
	lanes := buildTaskBoardLanes(choices, nil)
	wantOrder := []string{"pending", "ready", "in_progress", "blocked", "closed"}
	if len(lanes) != len(wantOrder) {
		t.Fatalf("len = %d, want %d", len(lanes), len(wantOrder))
	}
	for i, want := range wantOrder {
		if lanes[i].Value != want {
			t.Fatalf("lane[%d] = %q, want %q", i, lanes[i].Value, want)
		}
	}
}

func TestTaskBoardShowsStateOmitsClosedStates(t *testing.T) {
	if !taskBoardShowsState("closed") {
		t.Fatal("expected closed lane to be visible on /task")
	}
	if taskBoardShowsState("done") {
		t.Fatal("expected done to be hidden from /task")
	}
	if taskBoardShowsState("cancelled") {
		t.Fatal("expected cancelled to be hidden from /task")
	}
	if taskBoardShowsState("skipped") {
		t.Fatal("expected skipped to be hidden from /task")
	}
	if !taskBoardShowsState("in_progress") {
		t.Fatal("expected in_progress to remain visible on /task")
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

func TestTaskBoardTemplateRendersMobileLaneSwitcherAndMoveControl(t *testing.T) {
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
		"TaskBoardTypeOptions":      []taskBoardChoice{{Value: "", Label: "Any Type"}},
		"TaskBoardPriority":         "",
		"TaskBoardPriorityOptions":  []taskBoardChoice{{Value: "", Label: "Any Priority"}},
		"TaskBoardAssignedTo":       "",
		"TaskBoardAssigneeOptions":  []taskBoardChoice{{Value: "", Label: "Anyone"}},
		"TaskBoardSourceTables":     []string{"base_task"},
		"TaskBoardHasActiveFilters": false,
		"TaskBoardHasGroups":        false,
		"TaskBoardGroups":           []taskBoardGroupOption{},
		"TaskBoardGroupMembers":     map[string][]taskBoardUserOption{},
		"TaskBoardTotalTasks":       2,
		"TaskBoardLanes": []taskBoardLane{
			{Value: "pending", Label: "Pending", Count: 0, CanDrop: true},
			{
				Value:   "ready",
				Label:   "Ready",
				Count:   1,
				CanDrop: true,
				Cards: []taskBoardCard{
					{
						ID:        "3f2a1c00-0000-4000-8000-000000000001",
						Href:      "/f/dw_story/3f2a1c00-0000-4000-8000-000000000001",
						UpdateURL: "/api/bulk/update/dw_story",
						Title:     "Move me on a phone",
						CanDrag:   true,
					},
				},
			},
			{Value: "done", Label: "Done", Count: 1, CanDrop: true},
			{Value: "legacy_state", Label: "Legacy State", Count: 0, CanDrop: false},
		},
	}

	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "task-board-view", data); err != nil {
		t.Fatalf("ExecuteTemplate(task-board-view) error = %v", err)
	}
	rendered := out.String()

	// The lane count is a CSS custom property so the responsive layer can
	// switch to one lane at a time without fighting an inline grid declaration.
	if !strings.Contains(rendered, `style="--lane-count: 4"`) {
		t.Fatalf("expected lane count as a CSS custom property, got %q", rendered)
	}
	if strings.Contains(rendered, `style="grid-template-columns:`) {
		t.Fatalf("inline grid-template-columns cannot be overridden by stylesheets, got %q", rendered)
	}

	// Mobile lane switcher: one button per lane, labelled and counted.
	if !strings.Contains(rendered, `data-lane-switcher`) {
		t.Fatalf("expected mobile lane switcher in rendered output, got %q", rendered)
	}
	if !strings.Contains(rendered, `data-lane-target="ready"`) {
		t.Fatalf("expected a switcher button per lane, got %q", rendered)
	}
	if !strings.Contains(rendered, `data-lane-count-chip`) {
		t.Fatalf("expected live lane counts in the switcher, got %q", rendered)
	}
	// First non-empty lane is the one shown on a phone.
	if !strings.Contains(rendered, `class="ui-card ui-task-board-lane is-droppable is-mobile-active"`) {
		t.Fatalf("expected the first non-empty lane to be the active mobile lane, got %q", rendered)
	}

	// Card move control: reachable without drag.
	if !strings.Contains(rendered, `class="ui-input ui-task-board-move-select"`) {
		t.Fatalf("expected a per-card move control, got %q", rendered)
	}
	if !strings.Contains(rendered, `id="taskBoardMove-3f2a1c00-0000-4000-8000-000000000001"`) {
		t.Fatalf("expected the move control to be labelled per card, got %q", rendered)
	}
	if !strings.Contains(rendered, `<option value="ready" selected>Ready</option>`) {
		t.Fatalf("expected the move control to preselect the card's lane, got %q", rendered)
	}
	if !strings.Contains(rendered, `<option value="done">Move to Done</option>`) {
		t.Fatalf("expected every droppable lane as a move target, got %q", rendered)
	}
	// The closed control shows the card's current lane as state; every other
	// option is worded as the action it performs.
	if strings.Contains(rendered, `<option value="ready" selected>Move to Ready</option>`) {
		t.Fatalf("expected the current lane option to stay unworded, got %q", rendered)
	}
	if strings.Contains(rendered, `<option value="legacy_state"`) {
		t.Fatalf("expected non-droppable lanes to stay out of the move control, got %q", rendered)
	}

	// Drag remains the desktop fast path: one card attribute plus the script selector.
	if got := strings.Count(rendered, `draggable="true"`); got != 2 {
		t.Fatalf("expected drag to remain available, got %d occurrences", got)
	}
}

func TestTaskBoardTemplateOmitsMoveControlWithoutUpdatePermission(t *testing.T) {
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
		"TaskBoardPath":             "/task",
		"TaskBoardResetURL":         "/task",
		"TaskBoardListURL":          "/t/base_task",
		"TaskBoardUpdateURL":        "/api/bulk/update/base_task",
		"TaskBoardCanUpdate":        false,
		"TaskBoardScope":            "mine",
		"TaskBoardTypeOptions":      []taskBoardChoice{{Value: "", Label: "Any Type"}},
		"TaskBoardPriorityOptions":  []taskBoardChoice{{Value: "", Label: "Any Priority"}},
		"TaskBoardAssigneeOptions":  []taskBoardChoice{{Value: "", Label: "Anyone"}},
		"TaskBoardSourceTables":     []string{"base_task"},
		"TaskBoardHasActiveFilters": false,
		"TaskBoardHasGroups":        false,
		"TaskBoardGroups":           []taskBoardGroupOption{},
		"TaskBoardGroupMembers":     map[string][]taskBoardUserOption{},
		"TaskBoardTotalTasks":       1,
		"TaskBoardLanes": []taskBoardLane{
			{
				Value:   "ready",
				Label:   "Ready",
				Count:   1,
				CanDrop: true,
				Cards: []taskBoardCard{
					{ID: "3f2a1c00-0000-4000-8000-000000000002", Href: "/f/base_task/3f2a1c00-0000-4000-8000-000000000002", Title: "Read only", CanDrag: true},
				},
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "task-board-view", data); err != nil {
		t.Fatalf("ExecuteTemplate(task-board-view) error = %v", err)
	}
	rendered := out.String()

	if strings.Contains(rendered, `class="ui-input ui-task-board-move-select"`) {
		t.Fatalf("did not expect a move control without update permission, got %q", rendered)
	}
	// One occurrence is the script's own selector; none may be on a card.
	if got := strings.Count(rendered, `draggable="true"`); got != 1 {
		t.Fatalf("did not expect draggable cards without update permission, got %d occurrences", got)
	}
	// The switcher is a view control, so it stays for read-only users.
	if !strings.Contains(rendered, `data-lane-switcher`) {
		t.Fatalf("expected the lane switcher for read-only viewers, got %q", rendered)
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
