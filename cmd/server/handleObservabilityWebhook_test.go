package main

import (
	"context"
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
	if !observabilityWebhookAuthorized(req, "secret-value") {
		t.Fatal("expected bearer token secret to authorize")
	}

	req = httptest.NewRequest(http.MethodPost, observabilityWebhookPath, nil)
	req.Header.Set("X-Velm-Webhook-Secret", "header-secret")
	if !observabilityWebhookAuthorized(req, "header-secret") {
		t.Fatal("expected header secret to authorize")
	}

	req = httptest.NewRequest(http.MethodPost, observabilityWebhookPath, nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	if observabilityWebhookAuthorized(req, "header-secret") {
		t.Fatal("expected mismatched secret to fail authorization")
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
