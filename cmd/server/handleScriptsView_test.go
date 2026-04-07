package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"velm/internal/db"
	"strings"
	"testing"
)

func TestHandleRunAdhocScriptExecutesJavaScript(t *testing.T) {
	body := []byte(`{"code":"async function run(ctx) { console.log(\"hello\"); ctx.log(\"logged\", { x: 1 }); return { ok: true }; }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/scripts/run-adhoc", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handleRunAdhocScript(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	got := rec.Body.String()
	if !strings.Contains(got, "hello") {
		t.Fatalf("body = %q, expected hello", got)
	}
	if !strings.Contains(got, "logged") {
		t.Fatalf("body = %q, expected logged", got)
	}
	if !strings.Contains(got, `"ok":true`) {
		t.Fatalf("body = %q, expected JSON result", got)
	}
}

func TestHandleTestScriptDryRunExecutesSandbox(t *testing.T) {
	payload := map[string]any{
		"script_def_id": 0,
		"code":          `async function run(ctx) { record.resolution = "abc"; return record.resolution; }`,
		"sample_payload": map[string]any{
			"record": map[string]any{
				"resolution": "old",
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/scripts/test", bytes.NewReader(raw))
	rec := httptest.NewRecorder()

	handleTestScriptDryRun(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected ok response, got %#v", resp)
	}
	if got := resp["result"]; got != "abc" {
		t.Fatalf("result = %#v, want %q", got, "abc")
	}
	currentRecord, ok := resp["current_record"].(map[string]any)
	if !ok {
		t.Fatalf("current_record type = %T, want map[string]any", resp["current_record"])
	}
	if got := currentRecord["resolution"]; got != "abc" {
		t.Fatalf("current_record.resolution = %#v, want %q", got, "abc")
	}
}

func TestBuildRunScriptScopeOptionsPrefersSystemByDefault(t *testing.T) {
	apps := []db.RegisteredApp{
		{Name: "sales", Namespace: "sales", Label: "Sales"},
		{Name: "system", Namespace: "", Label: "System"},
	}

	options, selected := buildRunScriptScopeOptions(apps, "")

	if selected != "system" {
		t.Fatalf("selected = %q, want %q", selected, "system")
	}
	if len(options) != 2 {
		t.Fatalf("len(options) = %d, want 2", len(options))
	}
	if !options[1].Selected {
		t.Fatalf("expected system option to be selected: %#v", options[1])
	}
}

func TestBuildRunScriptScopeOptionsHonorsRequestedNamespace(t *testing.T) {
	apps := []db.RegisteredApp{
		{Name: "customer_mgmt", Namespace: "crm", Label: "CRM"},
		{Name: "system", Namespace: "", Label: "System"},
	}

	options, selected := buildRunScriptScopeOptions(apps, "crm")

	if selected != "crm" {
		t.Fatalf("selected = %q, want %q", selected, "crm")
	}
	if len(options) != 2 {
		t.Fatalf("len(options) = %d, want 2", len(options))
	}
	if !options[0].Selected {
		t.Fatalf("expected CRM option to be selected: %#v", options[0])
	}
}

func TestBuildRunScriptScopeOptionsPrefersOOTBBaseByDefault(t *testing.T) {
	apps := []db.RegisteredApp{
		{Name: "sales", Namespace: "sales", Label: "Sales"},
		{Name: "base", Namespace: "", Label: "Base"},
	}

	_, selected := buildRunScriptScopeOptions(apps, "")

	if selected != "base" {
		t.Fatalf("selected = %q, want %q", selected, "base")
	}
}
