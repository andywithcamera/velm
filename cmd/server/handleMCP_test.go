package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMCPInitializeAndToolsList exercises the transport without a database:
// initialize returns the protocol version and tools/list advertises all six
// task tools.
func TestMCPInitializeAndToolsList(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		body     string
		wantCode int
		wantSub  string
	}{
		{"initialize", "POST", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, 200, `"protocolVersion"`},
		{"tools_list", "POST", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, 200, `"spawn_task"`},
		{"ping", "POST", `{"jsonrpc":"2.0","id":3,"method":"ping"}`, 200, `"result":{}`},
		{"wrong_method", "GET", "", 405, "POST only"},
		{"bad_jsonrpc", "POST", `{"jsonrpc":"1.0","id":4,"method":"ping"}`, 200, `"code":-32600`},
		{"unknown_method", "POST", `{"jsonrpc":"2.0","id":5,"method":"bogus"}`, 200, `"code":-32601`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/mcp", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handleMCP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantSub) {
				t.Fatalf("body %q missing %q", rec.Body.String(), tc.wantSub)
			}
		})
	}
}

// TestMCPToolSchemas ensures every tool advertises a valid object schema with
// declared required fields matching what mcpCallTool reads.
func TestMCPToolSchemas(t *testing.T) {
	for _, tool := range mcpTools {
		if tool.InputSchema["type"] != "object" {
			t.Errorf("%s: schema type not object", tool.Name)
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		if len(props) == 0 {
			t.Errorf("%s: no properties", tool.Name)
		}
		if req, ok := tool.InputSchema["required"].([]string); ok {
			for _, r := range req {
				if _, ok := props[r]; !ok {
					t.Errorf("%s: required %q not in properties", tool.Name, r)
				}
			}
		}
	}
	if mcpToolByName("nope") != nil {
		t.Error("mcpToolByName returned a tool for an unknown name")
	}
}

// TestMCPUnknownTool verifies tools/call with an unknown tool errors cleanly.
func TestMCPUnknownTool(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"nope","arguments":{}}}`
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	// No auth context: the handler runs as part of the authed chain in prod;
	// mcpCallTool must fail on the unknown tool before touching the user id.
	var params json.RawMessage = json.RawMessage(`{"name":"nope","arguments":{}}`)
	_, code, msg := mcpCallTool(req, params)
	if code != -32602 || msg != "unknown tool: nope" {
		t.Fatalf("got (%d,%q), want (-32602, unknown tool: nope)", code, msg)
	}
}

// TestMCPStateFilter covers both accepted spellings and the value validation.
// The filter itself lives in SQL; this asserts the argument contract in front
// of it, which is where the state=ready call used to be dropped on the floor.
func TestMCPStateFilter(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		want    []string
		wantErr string
	}{
		{"absent", map[string]any{}, []string{}, ""},
		{"states_array", map[string]any{"states": []any{"ready", "in_progress"}}, []string{"ready", "in_progress"}, ""},
		{"state_scalar", map[string]any{"state": "ready"}, []string{"ready"}, ""},
		{"state_and_states", map[string]any{"state": "closed", "states": []any{"ready"}}, []string{"ready", "closed"}, ""},
		{"case_and_space", map[string]any{"state": "  READY "}, []string{"ready"}, ""},
		{"duplicate", map[string]any{"states": []any{"ready", "ready"}}, []string{"ready"}, ""},
		{"unknown_state", map[string]any{"state": "redy"}, nil, `unknown state "redy"`},
		{"non_string_element", map[string]any{"states": []any{1}}, nil, "states must contain strings"},
		{"wrong_type", map[string]any{"state": 42}, nil, "state must be a string or an array of strings"},
		{"only_limits", map[string]any{"limit": float64(5)}, []string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mcpStateFilter(tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestMCPEveryToolRejectsUnknownArguments guards the silent-drop regression:
// an argument the schema does not declare must be named back to the caller.
func TestMCPEveryToolRejectsUnknownArguments(t *testing.T) {
	for _, tool := range mcpTools {
		t.Run(tool.Name, func(t *testing.T) {
			params := map[string]any{"name": tool.Name, "arguments": map[string]any{"stauts": "ready"}}
			raw, err := json.Marshal(params)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			req := httptest.NewRequest("POST", "/mcp", strings.NewReader(""))
			_, code, msg := mcpCallTool(req, raw)
			if code != -32602 {
				t.Fatalf("code = %d, want -32602 (msg %q)", code, msg)
			}
			if !strings.Contains(msg, `unknown argument "stauts"`) {
				t.Fatalf("msg = %q, want it to name the unknown argument", msg)
			}
			if !strings.Contains(msg, tool.Name) {
				t.Fatalf("msg = %q, want it to name the tool", msg)
			}
		})
	}
}

// TestMCPDeclaredArgumentsAreAccepted is the other half: every argument the
// schema advertises must pass validation. Called with no database, a declared
// tool that got past validation fails later on the nil pool — which is fine,
// as long as the failure is not "unknown argument".
func TestMCPDeclaredArgumentsAreAccepted(t *testing.T) {
	for _, tool := range mcpTools {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		for name := range props {
			if got := firstUnknownArgument(map[string]any{name: "x"}, &tool); got != "" {
				t.Errorf("%s: declared argument %q reported as unknown", tool.Name, name)
			}
		}
		if got := firstUnknownArgument(map[string]any{"state": "ready"}, mcpToolByName("list_tasks")); got != "" {
			t.Errorf("list_tasks: state alias reported as unknown (%q)", got)
		}
	}
}
