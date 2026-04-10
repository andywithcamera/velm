package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveObservabilityWebhookSecretPrefersEnv(t *testing.T) {
	t.Setenv(observabilityWebhookSecretEnvVar, "env-secret")

	originalProperties := propertyItems
	propertyItems = map[string]any{
		observabilityWebhookSecretPropertyKey: "property-secret",
	}
	t.Cleanup(func() { propertyItems = originalProperties })

	secret, err := resolveObservabilityWebhookSecret(context.Background())
	if err != nil {
		t.Fatalf("resolveObservabilityWebhookSecret() error = %v", err)
	}
	if secret != "env-secret" {
		t.Fatalf("secret = %q, want %q", secret, "env-secret")
	}
}

func TestResolveObservabilityWebhookSecretFallsBackToPropertyCache(t *testing.T) {
	t.Setenv(observabilityWebhookSecretEnvVar, "")

	originalProperties := propertyItems
	propertyItems = map[string]any{
		observabilityWebhookSecretPropertyKey: "property-secret",
	}
	t.Cleanup(func() { propertyItems = originalProperties })

	secret, err := resolveObservabilityWebhookSecret(context.Background())
	if err != nil {
		t.Fatalf("resolveObservabilityWebhookSecret() error = %v", err)
	}
	if secret != "property-secret" {
		t.Fatalf("secret = %q, want %q", secret, "property-secret")
	}
}

func TestObservabilityWebhookAuthorizedAcceptsBearerAndHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, observabilityWebhookPath, nil)
	req.Header.Set("Authorization", "Bearer secret-value")
	if !observabilityWebhookAuthorized(req, "secret-value", nil) {
		t.Fatal("expected bearer token secret to authorize")
	}

	req = httptest.NewRequest(http.MethodPost, observabilityWebhookPath, nil)
	req.Header.Set("X-Velm-Webhook-Secret", "header-secret")
	if !observabilityWebhookAuthorized(req, "header-secret", nil) {
		t.Fatal("expected header secret to authorize")
	}

	req = httptest.NewRequest(http.MethodPost, observabilityWebhookPath, nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	if observabilityWebhookAuthorized(req, "header-secret", nil) {
		t.Fatal("expected mismatched secret to fail authorization")
	}
}

func TestObservabilityWebhookAuthorizedAcceptsGitHubSignature(t *testing.T) {
	body := []byte(`{"zen":"keep it logically awesome"}`)
	req := httptest.NewRequest(http.MethodPost, observabilityWebhookPath, nil)
	req.Header.Set("X-GitHub-Event", "ping")

	mac := hmac.New(sha256.New, []byte("github-secret"))
	_, _ = mac.Write(body)
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))

	if !observabilityWebhookAuthorized(req, "github-secret", body) {
		t.Fatal("expected github signature to authorize")
	}
	if observabilityWebhookAuthorized(req, "wrong-secret", body) {
		t.Fatal("expected wrong github secret to fail authorization")
	}
}

func TestBuildGitHubObservabilityPayloadPing(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, observabilityWebhookPath, nil)
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-GitHub-Delivery", "delivery-1")

	payload := buildGitHubObservabilityPayload(req, "ping", map[string]any{
		"repository": map[string]any{
			"full_name": "acme/platform",
		},
		"zen": "keep it logically awesome",
	})

	if got := payload["metric_name"]; got != "github_ping" {
		t.Fatalf("metric_name = %#v, want github_ping", got)
	}
	if got := payload["node"]; got != "acme/platform" {
		t.Fatalf("node = %#v, want acme/platform", got)
	}
	if got := payload["severity"]; got != 0 {
		t.Fatalf("severity = %#v, want 0", got)
	}
}

func TestBuildGitHubObservabilityPayloadWorkflowFailure(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, observabilityWebhookPath, nil)
	req.Header.Set("X-GitHub-Event", "workflow_run")

	payload := buildGitHubObservabilityPayload(req, "workflow_run", map[string]any{
		"repository": map[string]any{
			"full_name": "acme/platform",
		},
		"workflow_run": map[string]any{
			"name":       "CI",
			"status":     "completed",
			"conclusion": "failure",
			"updated_at": "2026-04-10T10:00:00Z",
		},
	})

	if got := payload["metric_name"]; got != "github_delivery_health" {
		t.Fatalf("metric_name = %#v, want github_delivery_health", got)
	}
	if got := payload["resource"]; got != "CI" {
		t.Fatalf("resource = %#v, want CI", got)
	}
	if got := payload["severity"]; got != 4 {
		t.Fatalf("severity = %#v, want 4", got)
	}
	if got := payload["golden_signal"]; got != "errors" {
		t.Fatalf("golden_signal = %#v, want errors", got)
	}
}

func TestHandleObservabilityWebhookRejectsUnauthorizedRequests(t *testing.T) {
	t.Setenv(observabilityWebhookSecretEnvVar, "expected-secret")

	req := httptest.NewRequest(http.MethodPost, observabilityWebhookPath, nil)
	rec := httptest.NewRecorder()

	handleObservabilityWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleObservabilityWebhookRejectsWhenNotConfigured(t *testing.T) {
	t.Setenv(observabilityWebhookSecretEnvVar, "")

	originalProperties := propertyItems
	propertyItems = map[string]any{}
	t.Cleanup(func() { propertyItems = originalProperties })

	req := httptest.NewRequest(http.MethodPost, observabilityWebhookPath, nil)
	req.Header.Set("X-Velm-Webhook-Secret", "anything")
	rec := httptest.NewRecorder()

	handleObservabilityWebhook(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
