package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"velm/internal/db"
)

func TestAppRuntimeRequestPayloadParsesJSONBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/devworks/board-data?lane=new", bytes.NewBufferString(`{"force":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test", "value")

	payload, input, err := appRuntimeRequestPayload(req)
	if err != nil {
		t.Fatalf("appRuntimeRequestPayload() error = %v", err)
	}

	body, ok := payload.(map[string]any)
	if !ok || body["force"] != true {
		t.Fatalf("expected JSON payload map, got %#v", payload)
	}
	query, ok := input["query"].(map[string]any)
	if !ok || query["lane"] != "new" {
		t.Fatalf("expected query lane=new, got %#v", input["query"])
	}
}

func TestWriteAppRuntimeResultHonorsEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAppRuntimeResult(rec, db.ScriptExecutionResult{
		Result: map[string]any{
			"status": 201,
			"headers": map[string]any{
				"Content-Type": "application/json",
			},
			"body": map[string]any{
				"ok": true,
			},
		},
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response JSON error = %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("body = %#v, want ok=true", body)
	}
}

func TestHandleAppRuntimeServiceCallRequiresAppAndCall(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/app-runtime/call", bytes.NewBufferString(`{"app":"devworks"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handleAppRuntimeServiceCall(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
