package db

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeObservedPath(t *testing.T) {
	got := normalizeObservedPath("/api/save/base_task/42/550e8400-e29b-41d4-a716-446655440000/deadbeefdeadbeef")
	want := "/api/save/base_task/:id/:uuid/:token"
	if got != want {
		t.Fatalf("expected normalized path %q, got %q", want, got)
	}
}

func TestBuildDerivedObservabilityFromRequestMetricHealthy(t *testing.T) {
	events := buildDerivedObservabilityFromRequestMetric(RequestMetricRecord{
		RequestID:      "req-1",
		Method:         "GET",
		Path:           "/api/save/base_task/550e8400-e29b-41d4-a716-446655440000",
		RequestSource:  "api",
		Status:         200,
		ServerDuration: 120 * time.Millisecond,
		DBDuration:     20 * time.Millisecond,
	})
	if len(events) != 1 {
		t.Fatalf("expected one derived event, got %d", len(events))
	}

	payload := events[0].Payload
	if got := payload["metric_name"]; got != "route_request_health" {
		t.Fatalf("expected route_request_health metric, got %#v", got)
	}
	if got := payload["severity"]; got != 0 {
		t.Fatalf("expected healthy severity 0, got %#v", got)
	}
	if got := payload["node"]; got != "GET /api/save/base_task/:uuid" {
		t.Fatalf("expected normalized route node, got %#v", got)
	}
	summary, _ := payload["summary"].(string)
	if !strings.Contains(summary, "healthy") {
		t.Fatalf("expected healthy summary, got %q", summary)
	}
}

func TestBuildDerivedObservabilityFromRequestMetricCritical(t *testing.T) {
	events := buildDerivedObservabilityFromRequestMetric(RequestMetricRecord{
		RequestID:      "req-2",
		Method:         "POST",
		Path:           "/api/base/observability/ingest",
		RequestSource:  "api",
		Status:         503,
		ServerDuration: 180 * time.Millisecond,
	})
	if len(events) != 1 {
		t.Fatalf("expected one derived event, got %d", len(events))
	}
	if got := events[0].Payload["severity"]; got != 4 {
		t.Fatalf("expected critical severity 4, got %#v", got)
	}
	summary, _ := events[0].Payload["summary"].(string)
	if !strings.Contains(summary, "503") {
		t.Fatalf("expected status in summary, got %q", summary)
	}
}

func TestBuildDerivedObservabilityFromAuditEvent(t *testing.T) {
	if events := buildDerivedObservabilityFromAuditEvent(AuditEvent{
		Method: "POST",
		Path:   "/api/save/base_task",
		Status: 204,
	}); len(events) != 0 {
		t.Fatalf("expected no derived audit event for healthy mutation, got %d", len(events))
	}

	events := buildDerivedObservabilityFromAuditEvent(AuditEvent{
		UserID: "user-1",
		Method: "DELETE",
		Path:   "/api/apps/42",
		Status: 403,
	})
	if len(events) != 1 {
		t.Fatalf("expected one derived audit event, got %d", len(events))
	}
	if got := events[0].Payload["severity"]; got != 2 {
		t.Fatalf("expected warning severity 2, got %#v", got)
	}
	if got := events[0].Payload["node"]; got != "DELETE /api/apps/:id" {
		t.Fatalf("expected normalized audit route, got %#v", got)
	}
}

func TestBuildDerivedObservabilityFromDataChange(t *testing.T) {
	if events := buildDerivedObservabilityFromDataChange("user-1", "base_task", "rec-1", "update", map[string]any{"state": "new"}, map[string]any{"state": "closed"}); len(events) != 0 {
		t.Fatalf("expected ordinary table changes to be ignored, got %d events", len(events))
	}

	events := buildDerivedObservabilityFromDataChange(
		"user-1",
		"_role_permission",
		"rec-2",
		"update",
		map[string]any{"permission": "read", "role_id": "a"},
		map[string]any{"permission": "write", "role_id": "a"},
	)
	if len(events) != 1 {
		t.Fatalf("expected one sensitive data-change event, got %d", len(events))
	}

	payload := events[0].Payload
	if got := payload["severity"]; got != 3 {
		t.Fatalf("expected warning severity 3 for permission change, got %#v", got)
	}
	if got := payload["node"]; got != "_role_permission" {
		t.Fatalf("expected table node _role_permission, got %#v", got)
	}
	summary, _ := payload["summary"].(string)
	if !strings.Contains(summary, "_role_permission") {
		t.Fatalf("expected table name in summary, got %q", summary)
	}
}
