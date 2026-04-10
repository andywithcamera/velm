package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"net/http"
	"os"
	"strings"

	"velm/internal/db"
	"velm/internal/security"
)

const (
	observabilityWebhookPath              = "/api/observability/webhook"
	observabilityWebhookSecretEnvVar      = "OBSERVABILITY_WEBHOOK_SECRET"
	observabilityWebhookSecretPropertyKey = "observability_webhook_secret"
)

func handleObservabilityWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	secret, err := resolveObservabilityWebhookSecret(r.Context())
	if err != nil {
		http.Error(w, "Failed to load webhook configuration", http.StatusInternalServerError)
		return
	}
	if secret == "" {
		http.Error(w, "Observability webhook is not configured", http.StatusServiceUnavailable)
		return
	}
	if !observabilityWebhookAuthorized(r, secret) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="observability-webhook"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	payload, input, err := appRuntimeRequestPayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	baseApp, found, err := db.ResolveRuntimeApp(r.Context(), "base")
	if err != nil {
		http.Error(w, "Failed to resolve base app", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "Base app not found", http.StatusNotFound)
		return
	}

	result, err := db.ExecuteAppServiceMethod(r.Context(), db.AppServiceMethodCall{
		App:           baseApp,
		Call:          "observability.ingest_event",
		RequestID:     security.RequestIDFromContext(r.Context()),
		Input:         input,
		Payload:       payload,
		RequirePublic: true,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeAppRuntimeResult(w, result)
}

func resolveObservabilityWebhookSecret(ctx context.Context) (string, error) {
	if secret := strings.TrimSpace(os.Getenv(observabilityWebhookSecretEnvVar)); secret != "" {
		return secret, nil
	}

	if db.Pool != nil {
		var secret string
		err := db.Pool.QueryRow(ctx, `SELECT value FROM _property WHERE key = $1 LIMIT 1`, observabilityWebhookSecretPropertyKey).Scan(&secret)
		if err == nil {
			return strings.TrimSpace(secret), nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
	}

	if fallback, ok := propertyItems[observabilityWebhookSecretPropertyKey]; ok {
		if secret, ok := fallback.(string); ok {
			return strings.TrimSpace(secret), nil
		}
	}

	return "", nil
}

func observabilityWebhookAuthorized(r *http.Request, expectedSecret string) bool {
	providedSecret := observabilityWebhookRequestSecret(r)
	if providedSecret == "" || expectedSecret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(providedSecret), []byte(expectedSecret)) == 1
}

func observabilityWebhookRequestSecret(r *http.Request) string {
	if r == nil {
		return ""
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authHeader) > len("Bearer ") && strings.EqualFold(authHeader[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(authHeader[len("Bearer "):])
	}

	return strings.TrimSpace(r.Header.Get("X-Velm-Webhook-Secret"))
}
