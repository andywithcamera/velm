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
