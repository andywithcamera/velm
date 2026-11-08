package main

// Handler-level tests for the task instance spawn API (issue #224). The db
// layer is faked through the same indirection vars the runtime uses; a
// separate integration pass against PostgreSQL covers SpawnTaskInstance
// itself (existing TaskEngine behaviour).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"velm/internal/auth"
	"velm/internal/db"
)

func taskInstanceTestRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/task-instances", strings.NewReader(body))
	ctx := auth.WithUserContext(req.Context(), "user-1", "a@b.c", "A", "admin")
	return req.WithContext(ctx)
}

func TestTaskInstanceCreateBySlug(t *testing.T) {
	origSpawn, origDescribe := runtimeSpawnTaskBySlug, runtimeDescribeTaskInstace
	defer func() { runtimeSpawnTaskBySlug, runtimeDescribeTaskInstace = origSpawn, origDescribe }()

	var capturedSlug, capturedParent, capturedUser string
	runtimeSpawnTaskBySlug = func(ctx context.Context, q db.ScriptQuerier, slug, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
		capturedSlug, capturedParent, capturedUser = slug, parentTaskID, requestedBy
		if len(inputs) != 1 || inputs["issue"] != "42" {
			t.Errorf("inputs not forwarded: %v", inputs)
		}
		return "task-1", nil
	}
	runtimeDescribeTaskInstace = func(ctx context.Context, q db.ScriptQuerier, taskID string) (*db.TaskInstanceSummary, []db.TaskInstanceSummary, error) {
		if taskID != "task-1" {
			t.Errorf("describe got %s", taskID)
		}
		return &db.TaskInstanceSummary{ID: "task-1", Number: "T-000099", Title: "Feature", State: "ready"},
			[]db.TaskInstanceSummary{{ID: "task-2", Number: "T-000100", Title: "Child", State: "ready"}},
			nil
	}

	rec := httptest.NewRecorder()
	handleTaskInstanceCreate(rec, taskInstanceTestRequest(`{"definition_slug":"feature_request","inputs":{"issue":"42"},"parent_task_id":"p-9"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if capturedSlug != "feature_request" || capturedParent != "p-9" || capturedUser != "user-1" {
		t.Fatalf("spawn args wrong: %q %q %q", capturedSlug, capturedParent, capturedUser)
	}
	if !strings.Contains(rec.Body.String(), `"task_id":"task-1"`) || !strings.Contains(rec.Body.String(), "T-000100") {
		t.Fatalf("response missing instance graph: %s", rec.Body.String())
	}
}

func TestTaskInstanceCreateByID(t *testing.T) {
	origSpawn, origDescribe := runtimeSpawnTaskByID, runtimeDescribeTaskInstace
	defer func() { runtimeSpawnTaskByID, runtimeDescribeTaskInstace = origSpawn, origDescribe }()

	runtimeSpawnTaskByID = func(ctx context.Context, q db.ScriptQuerier, definitionID, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
		if definitionID != "def-uuid" {
			t.Errorf("definitionID = %q", definitionID)
		}
		return "task-1", nil
	}
	runtimeDescribeTaskInstace = func(ctx context.Context, q db.ScriptQuerier, taskID string) (*db.TaskInstanceSummary, []db.TaskInstanceSummary, error) {
		return &db.TaskInstanceSummary{ID: taskID, Number: "T-000099", Title: "x", State: "ready"}, nil, nil
	}

	rec := httptest.NewRecorder()
	handleTaskInstanceCreate(rec, taskInstanceTestRequest(`{"definition_id":"def-uuid"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestTaskInstanceCreateValidation(t *testing.T) {
	cases := []struct {
		name, body, wantErr string
	}{
		{"no definition", `{}`, "definition_slug or definition_id is required"},
		{"bad json", `{not json`, "Invalid JSON body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origSpawn := runtimeSpawnTaskBySlug
			runtimeSpawnTaskBySlug = func(ctx context.Context, q db.ScriptQuerier, slug, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
				t.Error("spawn should not be called")
				return "", errors.New("unreachable")
			}
			defer func() { runtimeSpawnTaskBySlug = origSpawn }()
			rec := httptest.NewRecorder()
			handleTaskInstanceCreate(rec, taskInstanceTestRequest(tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.wantErr) {
				t.Fatalf("body %q missing %q", rec.Body.String(), tc.wantErr)
			}
		})
	}
}

func TestTaskInstanceCreateUnknownDefinitionIs400(t *testing.T) {
	origSpawn := runtimeSpawnTaskBySlug
	defer func() { runtimeSpawnTaskBySlug = origSpawn }()
	runtimeSpawnTaskBySlug = func(ctx context.Context, q db.ScriptQuerier, slug, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
		return "", errors.New("resolve task definition by slug \"nope\": no rows in result set")
	}
	rec := httptest.NewRecorder()
	handleTaskInstanceCreate(rec, taskInstanceTestRequest(`{"definition_slug":"nope"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTaskInstanceCreateMethodGate(t *testing.T) {
	rec := httptest.NewRecorder()
	handleTaskInstanceCreate(rec, httptest.NewRequest(http.MethodGet, "/api/task-instances", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("Allow = %q", rec.Header().Get("Allow"))
	}
}
