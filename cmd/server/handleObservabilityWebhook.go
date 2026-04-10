package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
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
	body, err := readAppRuntimeRequestBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !observabilityWebhookAuthorized(r, secret, body) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="observability-webhook"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	payload, input, err := appRuntimeRequestPayloadFromBody(r, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	payload, err = normalizeObservabilityWebhookPayload(r, payload)
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

func observabilityWebhookAuthorized(r *http.Request, expectedSecret string, body []byte) bool {
	if githubWebhookAuthorized(r, expectedSecret, body) {
		return true
	}
	providedSecret := observabilityWebhookRequestSecret(r)
	if providedSecret == "" || expectedSecret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(providedSecret), []byte(expectedSecret)) == 1
}

func githubWebhookAuthorized(r *http.Request, expectedSecret string, body []byte) bool {
	signature := strings.TrimSpace(strings.ToLower(r.Header.Get("X-Hub-Signature-256")))
	if githubWebhookEvent(r) == "" || signature == "" || expectedSecret == "" {
		return false
	}
	expectedSignature := githubWebhookSignature(expectedSecret, body)
	return subtle.ConstantTimeCompare([]byte(signature), []byte(expectedSignature)) == 1
}

func githubWebhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
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

func githubWebhookEvent(r *http.Request) string {
	if r == nil {
		return ""
	}
	return sanitizeWebhookIdentifier(r.Header.Get("X-GitHub-Event"))
}

func normalizeObservabilityWebhookPayload(r *http.Request, payload any) (any, error) {
	eventType := githubWebhookEvent(r)
	if eventType == "" {
		return payload, nil
	}

	document, ok := payload.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("github webhook payload must be a JSON object")
	}
	return buildGitHubObservabilityPayload(r, eventType, document), nil
}

func buildGitHubObservabilityPayload(r *http.Request, eventType string, payload map[string]any) map[string]any {
	repoName := githubRepositoryFullName(payload)
	if repoName == "" {
		repoName = firstWebhookString(githubNestedString(payload, "organization", "login"), githubNestedString(payload, "sender", "login"), "github")
	}
	action := githubWebhookAction(payload)
	resource := githubWebhookResource(eventType, payload)
	metricName := githubWebhookMetricName(eventType)
	severity := githubWebhookSeverity(eventType, payload)
	summary := githubWebhookSummary(eventType, repoName, resource, payload, severity)

	normalized := map[string]any{
		"source":                 "github",
		"observable_class":       "custom",
		"definition_name":        "GitHub " + humanizeWebhookIdentifier(eventType),
		"definition_description": "Normalized GitHub webhook delivery.",
		"metric_name":            metricName,
		"node":                   repoName,
		"display_name":           "GitHub " + repoName + " " + humanizeWebhookIdentifier(eventType),
		"resource":               resource,
		"severity":               severity,
		"summary":                summary,
		"value": map[string]any{
			"github_event":    eventType,
			"github_action":   action,
			"github_delivery": strings.TrimSpace(r.Header.Get("X-GitHub-Delivery")),
			"repository":      repoName,
			"resource":        resource,
			"status":          githubWebhookStatus(payload),
			"conclusion":      githubWebhookConclusion(payload),
			"ref":             firstWebhookString(strings.TrimSpace(fmt.Sprint(payload["ref"])), githubNestedString(payload, "pull_request", "base", "ref")),
			"sender":          githubNestedString(payload, "sender", "login"),
		},
		"entity": map[string]any{
			"name":             "GitHub " + repoName,
			"entity_type":      "service",
			"source_system":    "github",
			"source_record_id": "repo::" + strings.ToLower(repoName),
		},
		"payload": payload,
	}

	if occurredAt := githubWebhookOccurredAt(payload); occurredAt != "" {
		normalized["occurred_at"] = occurredAt
	}
	if signal := githubWebhookSignal(eventType); signal != "" {
		normalized["golden_signal"] = signal
	}
	return normalized
}

func githubRepositoryFullName(payload map[string]any) string {
	return firstWebhookString(
		githubNestedString(payload, "repository", "full_name"),
		githubNestedString(payload, "repository", "name"),
	)
}

func githubWebhookMetricName(eventType string) string {
	switch eventType {
	case "workflow_run", "workflow_job", "check_run", "check_suite":
		return "github_delivery_health"
	default:
		return "github_" + eventType
	}
}

func githubWebhookResource(eventType string, payload map[string]any) string {
	switch eventType {
	case "workflow_run":
		return firstWebhookString(
			githubNestedString(payload, "workflow_run", "name"),
			githubNestedString(payload, "workflow", "name"),
			"workflow",
		)
	case "workflow_job":
		return firstWebhookString(
			githubNestedString(payload, "workflow_job", "name"),
			githubNestedString(payload, "workflow_job", "workflow_name"),
			"workflow_job",
		)
	case "check_run":
		return firstWebhookString(githubNestedString(payload, "check_run", "name"), "check_run")
	case "check_suite":
		return firstWebhookString(githubNestedString(payload, "check_suite", "head_branch"), "check_suite")
	case "push":
		return firstWebhookString(strings.TrimSpace(fmt.Sprint(payload["ref"])), "push")
	case "pull_request":
		return firstWebhookString(githubNestedString(payload, "pull_request", "base", "ref"), "pull_request")
	case "release":
		return firstWebhookString(githubNestedString(payload, "release", "tag_name"), "release")
	case "ping":
		return "webhook"
	default:
		return firstWebhookString(strings.TrimSpace(fmt.Sprint(payload["action"])), eventType)
	}
}

func githubWebhookSeverity(eventType string, payload map[string]any) int {
	switch eventType {
	case "ping":
		return 0
	case "workflow_run", "workflow_job", "check_run", "check_suite":
		return githubDeliveryStateSeverity(githubWebhookStatus(payload), githubWebhookConclusion(payload))
	case "push":
		if githubWebhookBool(payload["deleted"]) || githubWebhookBool(payload["forced"]) {
			return 2
		}
		return 1
	default:
		switch strings.TrimSpace(strings.ToLower(fmt.Sprint(payload["action"]))) {
		case "deleted", "archived", "disabled", "removed":
			return 2
		default:
			return 1
		}
	}
}

func githubWebhookSummary(eventType, repoName, resource string, payload map[string]any, severity int) string {
	action := strings.TrimSpace(strings.ToLower(githubWebhookAction(payload)))
	switch eventType {
	case "ping":
		return "GitHub webhook ping for " + repoName
	case "workflow_run", "workflow_job", "check_run", "check_suite":
		status := githubWebhookStatus(payload)
		conclusion := githubWebhookConclusion(payload)
		if conclusion != "" {
			return "GitHub " + humanizeWebhookIdentifier(eventType) + " " + resource + " is " + conclusion + " for " + repoName
		}
		if status != "" {
			return "GitHub " + humanizeWebhookIdentifier(eventType) + " " + resource + " is " + status + " for " + repoName
		}
	}

	if action != "" {
		return "GitHub " + humanizeWebhookIdentifier(eventType) + " " + action + " for " + repoName
	}
	if severity >= 2 {
		return "GitHub " + humanizeWebhookIdentifier(eventType) + " warning for " + repoName
	}
	return "GitHub " + humanizeWebhookIdentifier(eventType) + " event for " + repoName
}

func githubWebhookAction(payload map[string]any) string {
	return firstWebhookString(strings.TrimSpace(fmt.Sprint(payload["action"])))
}

func githubWebhookSignal(eventType string) string {
	switch eventType {
	case "workflow_run", "workflow_job", "check_run", "check_suite":
		return "errors"
	default:
		return ""
	}
}

func githubWebhookOccurredAt(payload map[string]any) string {
	return firstWebhookString(
		githubNestedString(payload, "workflow_run", "updated_at"),
		githubNestedString(payload, "workflow_run", "created_at"),
		githubNestedString(payload, "workflow_job", "completed_at"),
		githubNestedString(payload, "workflow_job", "started_at"),
		githubNestedString(payload, "check_run", "completed_at"),
		githubNestedString(payload, "check_run", "started_at"),
		githubNestedString(payload, "check_suite", "updated_at"),
		githubNestedString(payload, "release", "published_at"),
		githubNestedString(payload, "head_commit", "timestamp"),
		strings.TrimSpace(fmt.Sprint(payload["updated_at"])),
		strings.TrimSpace(fmt.Sprint(payload["created_at"])),
	)
}

func githubWebhookStatus(payload map[string]any) string {
	return firstWebhookString(
		githubNestedString(payload, "workflow_run", "status"),
		githubNestedString(payload, "workflow_job", "status"),
		githubNestedString(payload, "check_run", "status"),
		githubNestedString(payload, "check_suite", "status"),
	)
}

func githubWebhookConclusion(payload map[string]any) string {
	return firstWebhookString(
		githubNestedString(payload, "workflow_run", "conclusion"),
		githubNestedString(payload, "workflow_job", "conclusion"),
		githubNestedString(payload, "check_run", "conclusion"),
		githubNestedString(payload, "check_suite", "conclusion"),
	)
}

func githubDeliveryStateSeverity(status, conclusion string) int {
	conclusion = strings.TrimSpace(strings.ToLower(conclusion))
	switch conclusion {
	case "success", "neutral", "skipped":
		return 0
	case "cancelled", "stale":
		return 2
	case "failure", "timed_out", "action_required", "startup_failure":
		return 4
	}

	status = strings.TrimSpace(strings.ToLower(status))
	switch status {
	case "", "completed":
		return 1
	case "queued", "in_progress", "pending", "requested", "waiting":
		return 1
	default:
		return 1
	}
}

func githubNestedString(payload map[string]any, path ...string) string {
	current := any(payload)
	for _, segment := range path {
		document, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = document[segment]
	}
	return strings.TrimSpace(fmt.Sprint(current))
}

func githubWebhookBool(value any) bool {
	boolean, ok := value.(bool)
	return ok && boolean
}

func firstWebhookString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func humanizeWebhookIdentifier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func sanitizeWebhookIdentifier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}

	var builder strings.Builder
	previousUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
			previousUnderscore = false
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
			previousUnderscore = false
		default:
			if previousUnderscore {
				continue
			}
			builder.WriteByte('_')
			previousUnderscore = true
		}
	}

	out := strings.Trim(builder.String(), "_")
	if out == "" {
		return ""
	}
	return out
}
