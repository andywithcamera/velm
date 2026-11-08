package main

// Task instance spawn API (issue #224). SpawnTaskInstance existed with no
// HTTP caller; instantiating a task definition from outside the web form
// meant scraping HTML. This endpoint is the front door: it resolves a
// definition by slug or id and spawns the instance graph (root plus any
// container children) in one request.
//
// Auth: session or agent bearer token (withAuthOrAgentWrite), same authz
// surface as the other write endpoints (PermissionWrite, app scope).

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

const taskInstanceRequestBodyLimit = 1 << 20

// Indirection over the db calls, so tests can exercise the handler without
// PostgreSQL (same pattern as handleAppRuntime.go).
var (
	runtimeSpawnTaskBySlug     = db.SpawnTaskInstanceBySlug
	runtimeSpawnTaskByID       = db.SpawnTaskInstance
	runtimeDescribeTaskInstace = db.DescribeTaskInstance
)

type taskInstanceRequest struct {
	DefinitionSlug string         `json:"definition_slug"`
	DefinitionID   string         `json:"definition_id"`
	ParentTaskID   string         `json:"parent_task_id"`
	Inputs         map[string]any `json:"inputs"`
}

func handleTaskInstanceCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, taskInstanceRequestBodyLimit))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	var req taskInstanceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}
	req.DefinitionSlug = strings.TrimSpace(req.DefinitionSlug)
	req.DefinitionID = strings.TrimSpace(req.DefinitionID)
	req.ParentTaskID = strings.TrimSpace(req.ParentTaskID)
	if req.DefinitionSlug == "" && req.DefinitionID == "" {
		http.Error(w, "definition_slug or definition_id is required", http.StatusBadRequest)
		return
	}
	if req.Inputs == nil {
		req.Inputs = map[string]any{}
	}

	var taskID string
	if req.DefinitionSlug != "" {
		taskID, err = runtimeSpawnTaskBySlug(r.Context(), db.Pool, req.DefinitionSlug, req.ParentTaskID, userID, req.Inputs)
	} else {
		taskID, err = runtimeSpawnTaskByID(r.Context(), db.Pool, req.DefinitionID, req.ParentTaskID, userID, req.Inputs)
	}
	if err != nil {
		if strings.Contains(err.Error(), "no rows") || strings.Contains(err.Error(), "not active") {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "Failed to spawn task instance", http.StatusInternalServerError)
		return
	}

	root, children, err := runtimeDescribeTaskInstace(r.Context(), db.Pool, taskID)
	if err != nil {
		// The spawn committed; describing is read-only embellishment.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": taskID, "children": []any{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"task":     root,
		"children": children,
	})
}
