package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

// handleMCP serves a minimal Model Context Protocol (streamable HTTP) server
// over Velm's task engine (issue #240). Remote agents — Rool Machines among
// them — call tools here with an agent bearer token. Transport: POST-only
// JSON-RPC 2.0 with JSON responses; capabilities advertised: tools.

const mcpProtocolVersion = "2025-06-18"

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var mcpTools = []mcpTool{
	{
		Name: "list_tasks",
		Description: "List recent tasks, newest first. Filter by state: pass \"states\" as an array " +
			"(e.g. {\"states\":[\"ready\"]}) or \"state\" as a single string. Allowed values: pending, " +
			"ready, in_progress, closed, skipped. Omitting the filter returns every state.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"states": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional state filter, e.g. [\"ready\",\"in_progress\"]"},
				"state":  map[string]any{"type": "string", "description": "Optional single-state filter; alias for a one-element states"},
				"limit":  map[string]any{"type": "integer", "description": "Max results, default 20, max 100"},
			},
		},
	},
	{
		Name:        "get_task",
		Description: "Get one task with its direct children. Task ref is a uuid or TASK-n number.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{"type": "string", "description": "Task uuid or TASK-n number"},
			},
			"required": []string{"task"},
		},
	},
	{
		Name:        "add_comment",
		Description: "Add a comment to a task's timeline.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{"type": "string", "description": "Task uuid or TASK-n number"},
				"body": map[string]any{"type": "string"},
			},
			"required": []string{"task", "body"},
		},
	},
	{
		Name:        "start_task",
		Description: "Transition a task to in_progress, running the engine hooks (child promotion, run_if, scripts).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{"type": "string", "description": "Task uuid or TASK-n number"},
			},
			"required": []string{"task"},
		},
	},
	{
		Name:        "complete_task",
		Description: "Transition a task to closed, firing closure hooks and spawn rules.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{"type": "string", "description": "Task uuid or TASK-n number"},
			},
			"required": []string{"task"},
		},
	},
	{
		Name:        "spawn_task",
		Description: "Spawn a task instance from a definition slug. Returns the root and its children.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"definition_slug": map[string]any{"type": "string"},
				"parent_task":     map[string]any{"type": "string", "description": "Optional parent task uuid or TASK-n number"},
				"inputs":          map[string]any{"type": "object", "description": "Input variables for the definition"},
			},
			"required": []string{"definition_slug"},
		},
	},
}

func handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "MCP endpoint accepts POST only", http.StatusMethodNotAllowed)
		return
	}
	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMCP(w, mcpResponse{JSONRPC: "2.0", Error: &mcpError{Code: -32700, Message: "parse error"}})
		return
	}
	if req.JSONRPC != "2.0" {
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpError{Code: -32600, Message: "invalid request"}})
		return
	}

	switch req.Method {
	case "initialize":
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "velm", "title": "Velm Task Engine", "version": "1.0.0"},
		}})
	case "notifications/initialized":
		// Notification: no response.
		w.WriteHeader(http.StatusAccepted)
	case "ping":
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": mcpTools}})
	case "tools/call":
		result, errCode, errMsg := mcpCallTool(r, req.Params)
		if errCode != 0 {
			writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpError{Code: errCode, Message: errMsg}})
			return
		}
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
	default:
		writeMCP(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Error: &mcpError{Code: -32601, Message: "method not found: " + req.Method}})
	}
}

// mcpCallTool dispatches tools/call params to the task engine. All engine
// writes run as the authenticated principal (agent or user).
func mcpCallTool(r *http.Request, params json.RawMessage) (any, int, string) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, -32602, "invalid params"
	}
	var args map[string]any
	if len(p.Arguments) > 0 {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return nil, -32602, "invalid arguments"
		}
	}
	// Unknown arguments used to be dropped silently, so a caller who spelled
	// a filter differently than the schema (state vs states) got the whole
	// backlog back with a successful result and no way to tell. A misspelled
	// filter must not look like "no filter needed".
	if tool := mcpToolByName(p.Name); tool != nil {
		if unknown := firstUnknownArgument(args, tool); unknown != "" {
			return nil, -32602, fmt.Sprintf("unknown argument %q for %s (accepted: %s)",
				unknown, p.Name, strings.Join(acceptedArguments(tool), ", "))
		}
	}

	str := func(key string) string {
		s, _ := args[key].(string)
		return strings.TrimSpace(s)
	}
	userID := auth.UserIDFromRequest(r)
	ctx := r.Context()

	switch p.Name {
	case "list_tasks":
		states, err := mcpStateFilter(args)
		if err != nil {
			return nil, -32602, err.Error()
		}
		limit := 20
		if n, ok := args["limit"].(float64); ok {
			limit = int(n)
		}
		tasks, err := db.ListTaskInstances(ctx, db.Pool, states, limit)
		if err != nil {
			return nil, -32000, err.Error()
		}
		return mcpText(tasks), 0, ""
	case "get_task", "start_task", "complete_task", "add_comment":
		ref := str("task")
		id, err := db.ResolveTaskRef(ctx, db.Pool, ref)
		if err != nil {
			return nil, -32000, err.Error()
		}
		switch p.Name {
		case "get_task":
			root, children, err := db.DescribeTaskInstance(ctx, db.Pool, id)
			if err != nil {
				return nil, -32000, err.Error()
			}
			return mcpText(map[string]any{"task": root, "children": children}), 0, ""
		case "start_task":
			if err := db.StartTask(ctx, id, userID); err != nil {
				return nil, -32000, err.Error()
			}
			return mcpText(map[string]any{"started": ref}), 0, ""
		case "complete_task":
			if err := db.CompleteTask(ctx, id, userID); err != nil {
				return nil, -32000, err.Error()
			}
			return mcpText(map[string]any{"completed": ref}), 0, ""
		case "add_comment":
			body := str("body")
			if body == "" {
				return nil, -32602, "body is required"
			}
			if err := db.AddRecordComment(ctx, "base_task", id, userID, body); err != nil {
				return nil, -32000, err.Error()
			}
			return mcpText(map[string]any{"commented": ref}), 0, ""
		}
	case "spawn_task":
		slug := str("definition_slug")
		if slug == "" {
			return nil, -32602, "definition_slug is required"
		}
		parent := str("parent_task")
		var parentID string
		if parent != "" {
			var err error
			parentID, err = db.ResolveTaskRef(ctx, db.Pool, parent)
			if err != nil {
				return nil, -32000, err.Error()
			}
		}
		inputs, _ := args["inputs"].(map[string]any)
		rootID, err := db.SpawnTaskInstanceBySlug(ctx, db.Pool, slug, parentID, userID, inputs)
		if err != nil {
			return nil, -32000, err.Error()
		}
		root, children, err := db.DescribeTaskInstance(ctx, db.Pool, rootID)
		if err != nil {
			return nil, -32000, err.Error()
		}
		return mcpText(map[string]any{"task": root, "children": children}), 0, ""
	}
	return nil, -32602, "unknown tool: " + p.Name
}

// mcpText wraps a payload as the tool's text content.
func mcpText(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(fmt.Sprintf("%v", v))
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
	}
}

func writeMCP(w http.ResponseWriter, resp mcpResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp) // best-effort error channel; nothing sane to do on write failure
}

// acceptedArguments lists the argument names a tool's schema declares.
func acceptedArguments(tool *mcpTool) []string {
	props, _ := tool.InputSchema["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// firstUnknownArgument returns the first argument key the tool does not
// declare, or "" when every key is known. Keys are visited in sorted order so
// the reported name is stable across runs.
func firstUnknownArgument(args map[string]any, tool *mcpTool) string {
	if len(args) == 0 {
		return ""
	}
	accepted := acceptedArguments(tool)
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		known := false
		for _, a := range accepted {
			if a == k {
				known = true
				break
			}
		}
		if !known {
			return k
		}
	}
	return ""
}

// mcpStateFilter reads the state filter from either accepted spelling (states
// or state, scalar or array) and validates the values against the engine's
// state set. An unrecognised state is an error rather than an empty list:
// "nothing is ready" and "you misspelled ready" must not look alike.
func mcpStateFilter(args map[string]any) ([]string, error) {
	allowed := map[string]bool{}
	for _, s := range db.TaskStates() {
		allowed[s] = true
	}
	var raw []string
	for _, key := range []string{"states", "state"} {
		switch v := args[key].(type) {
		case nil:
		case string:
			raw = append(raw, v)
		case []any:
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("%s must contain strings", key)
				}
				raw = append(raw, s)
			}
		default:
			return nil, fmt.Errorf("%s must be a string or an array of strings", key)
		}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, s := range raw {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if !allowed[s] {
			return nil, fmt.Errorf("unknown state %q (allowed: %s)", s, strings.Join(db.TaskStates(), ", "))
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, nil
}

// mcpToolByName finds a tool definition (used by tests).
func mcpToolByName(name string) *mcpTool {
	for i := range mcpTools {
		if mcpTools[i].Name == name {
			return &mcpTools[i]
		}
	}
	return nil
}
