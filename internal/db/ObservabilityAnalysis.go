package db

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	derivedObservabilityServiceCall = "observability.ingest_event"

	derivedRequestMetricSource = "request_metric_analysis"
	derivedAuditLogSource      = "audit_log_analysis"
	derivedDataChangeSource    = "data_change_analysis"

	derivedObservabilityTimeout = 2 * time.Second

	requestHealthWarningServerMS = 750
	requestHealthCriticalServer  = 2000
	requestHealthWarningDBMS     = 250
	requestHealthCriticalDBMS    = 1000
)

var (
	observabilityAnalysisUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	observabilityAnalysisIntPattern  = regexp.MustCompile(`^\d+$`)
	observabilityAnalysisHexPattern  = regexp.MustCompile(`(?i)^[0-9a-f]{16,}$`)

	sensitiveDataChangeSeverity = map[string]int{
		"_app":                    2,
		"_group_role":             3,
		"_property":               2,
		"_role":                   2,
		"_role_inheritance":       3,
		"_role_permission":        3,
		"_user_role":              3,
		"base_obs_action":         2,
		"base_obs_default_action": 2,
	}
)

type derivedObservabilityEvent struct {
	RequestID string
	UserID    string
	Payload   map[string]any
}

func emitDerivedRequestMetricObservability(metric RequestMetricRecord) {
	emitDerivedObservabilityBestEffort("request metric", buildDerivedObservabilityFromRequestMetric(metric))
}

func emitDerivedAuditEventObservability(event AuditEvent) {
	emitDerivedObservabilityBestEffort("audit event", buildDerivedObservabilityFromAuditEvent(event))
}

func emitDerivedDataChangeObservability(userID, tableName, recordID, operation string, oldValues, newValues map[string]any) {
	emitDerivedObservabilityBestEffort("data change", buildDerivedObservabilityFromDataChange(userID, tableName, recordID, operation, oldValues, newValues))
}

func emitDerivedObservabilityBestEffort(label string, events []derivedObservabilityEvent) {
	if len(events) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), derivedObservabilityTimeout)
	defer cancel()

	if err := emitDerivedObservability(ctx, events); err != nil {
		log.Printf("derived observability emit failed: source=%s err=%v", label, err)
	}
}

func emitDerivedObservability(ctx context.Context, events []derivedObservabilityEvent) error {
	if len(events) == 0 {
		return nil
	}

	baseApp, ok, err := ResolveRuntimeApp(ctx, "base")
	if err != nil {
		return fmt.Errorf("resolve base app: %w", err)
	}
	if !ok {
		return nil
	}

	for _, event := range events {
		if len(event.Payload) == 0 {
			continue
		}
		if _, err := ExecuteAppServiceMethod(ctx, AppServiceMethodCall{
			App:       baseApp,
			Call:      derivedObservabilityServiceCall,
			UserID:    strings.TrimSpace(event.UserID),
			RequestID: strings.TrimSpace(event.RequestID),
			Payload:   event.Payload,
		}); err != nil {
			return fmt.Errorf("call %s: %w", derivedObservabilityServiceCall, err)
		}
	}

	return nil
}

func buildDerivedObservabilityFromRequestMetric(metric RequestMetricRecord) []derivedObservabilityEvent {
	source := normalizeRequestMetricSource(metric.RequestSource)
	switch source {
	case "asset", "client", "client_beacon", "stream":
		return nil
	}

	routePath := normalizeObservedPath(metric.Path)
	routeKey := buildObservedRouteKey(metric.Method, routePath)
	if routeKey == "" {
		return nil
	}

	serverMS := durationToMS(metric.ServerDuration)
	dbMS := durationToMS(metric.DBDuration)
	severity := requestMetricSeverity(metric.Status, serverMS, dbMS)
	payload := map[string]any{
		"source":                 derivedRequestMetricSource,
		"observable_class":       "http",
		"definition_name":        "HTTP Route Health",
		"definition_description": "Derived health signal from persisted request metrics.",
		"metric_name":            "route_request_health",
		"node":                   routeKey,
		"display_name":           "HTTP " + routeKey,
		"severity":               severity,
		"summary":                requestMetricSummary(routeKey, metric.Status, serverMS, dbMS, severity),
		"occurred_at":            observedAt(metric.FinishedAt, metric.StartedAt),
		"default_thresholds": map[string]any{
			"warning": map[string]any{
				"status_gte":             400,
				"server_duration_ms_gte": requestHealthWarningServerMS,
				"db_duration_ms_gte":     requestHealthWarningDBMS,
			},
			"critical": map[string]any{
				"status_gte":             500,
				"server_duration_ms_gte": requestHealthCriticalServer,
				"db_duration_ms_gte":     requestHealthCriticalDBMS,
			},
		},
		"value": map[string]any{
			"status":             metric.Status,
			"request_source":     source,
			"server_duration_ms": serverMS,
			"db_duration_ms":     dbMS,
			"db_query_count":     maxInt(metric.DBQueryCount, 0),
			"db_slowest_ms":      durationToMS(metric.DBSlowest),
		},
		"entity": map[string]any{
			"name":             "HTTP " + routeKey,
			"entity_type":      "service",
			"source_system":    derivedRequestMetricSource,
			"source_record_id": "route::" + strings.ToLower(routeKey),
		},
	}

	return []derivedObservabilityEvent{{
		RequestID: metric.RequestID,
		UserID:    metric.UserID,
		Payload:   payload,
	}}
}

func buildDerivedObservabilityFromAuditEvent(event AuditEvent) []derivedObservabilityEvent {
	severity, emit := auditEventSeverity(event.Status)
	if !emit {
		return nil
	}

	routePath := normalizeObservedPath(event.Path)
	routeKey := buildObservedRouteKey(event.Method, routePath)
	if routeKey == "" {
		return nil
	}

	payload := map[string]any{
		"source":                 derivedAuditLogSource,
		"observable_class":       "custom",
		"definition_name":        "Mutation Audit Failure",
		"definition_description": "Derived mutation anomaly signal from persisted audit logs.",
		"metric_name":            "mutation_audit_failure",
		"node":                   routeKey,
		"display_name":           "Mutation " + routeKey,
		"severity":               severity,
		"summary":                fmt.Sprintf("Mutation %s returned %d", routeKey, event.Status),
		"value": map[string]any{
			"method":    normalizeHTTPMethod(event.Method),
			"path":      routePath,
			"status":    event.Status,
			"user_role": strings.TrimSpace(event.UserRole),
		},
		"entity": map[string]any{
			"name":             "Mutation " + routeKey,
			"entity_type":      "service",
			"source_system":    derivedAuditLogSource,
			"source_record_id": "mutation::" + strings.ToLower(routeKey),
		},
	}

	return []derivedObservabilityEvent{{
		UserID:  event.UserID,
		Payload: payload,
	}}
}

func buildDerivedObservabilityFromDataChange(userID, tableName, recordID, operation string, oldValues, newValues map[string]any) []derivedObservabilityEvent {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	recordID = strings.TrimSpace(recordID)
	operation = strings.TrimSpace(strings.ToLower(operation))
	if tableName == "" || recordID == "" {
		return nil
	}

	severity, ok := sensitiveDataChangeSeverity[tableName]
	if !ok {
		return nil
	}
	if operation == "delete" && severity < 3 {
		severity = 3
	}

	diff := makeFieldDiff(stringifyValueMap(oldValues), stringifyValueMap(newValues))
	fields := make([]string, 0, len(diff))
	for fieldName := range diff {
		fields = append(fields, fieldName)
	}
	sort.Strings(fields)

	payload := map[string]any{
		"source":                 derivedDataChangeSource,
		"observable_class":       "custom",
		"definition_name":        "Sensitive Table Change",
		"definition_description": "Derived governance signal from persisted data change audit rows.",
		"metric_name":            "sensitive_table_change",
		"node":                   tableName,
		"display_name":           "Sensitive Table " + humanizeIdentifier(strings.TrimPrefix(tableName, "_")),
		"severity":               severity,
		"summary":                sensitiveTableChangeSummary(tableName, operation, recordID, fields),
		"value": map[string]any{
			"table_name":          tableName,
			"record_id":           recordID,
			"operation":           operation,
			"changed_fields":      fields,
			"changed_field_count": len(fields),
		},
		"entity": map[string]any{
			"name":             "Sensitive Table " + humanizeIdentifier(strings.TrimPrefix(tableName, "_")),
			"entity_type":      "configuration",
			"source_system":    derivedDataChangeSource,
			"source_record_id": "table::" + tableName,
		},
	}

	return []derivedObservabilityEvent{{
		UserID:  strings.TrimSpace(userID),
		Payload: payload,
	}}
}

func requestMetricSeverity(status, serverMS, dbMS int) int {
	switch {
	case status >= 500:
		return 4
	case serverMS >= requestHealthCriticalServer:
		return 4
	case dbMS >= requestHealthCriticalDBMS:
		return 4
	case status >= 400:
		return 2
	case serverMS >= requestHealthWarningServerMS:
		return 2
	case dbMS >= requestHealthWarningDBMS:
		return 2
	default:
		return 0
	}
}

func auditEventSeverity(status int) (int, bool) {
	switch {
	case status >= 500:
		return 3, true
	case status >= 400:
		return 2, true
	default:
		return 0, false
	}
}

func requestMetricSummary(routeKey string, status, serverMS, dbMS, severity int) string {
	switch {
	case status >= 500:
		return fmt.Sprintf("%s returned %d in %dms", routeKey, status, serverMS)
	case status >= 400:
		return fmt.Sprintf("%s returned %d in %dms", routeKey, status, serverMS)
	case severity >= 4:
		return fmt.Sprintf("%s was critically slow at %dms (%dms db)", routeKey, serverMS, dbMS)
	case severity >= 2:
		return fmt.Sprintf("%s was slow at %dms (%dms db)", routeKey, serverMS, dbMS)
	default:
		return fmt.Sprintf("%s was healthy at %dms (%dms db)", routeKey, serverMS, dbMS)
	}
}

func sensitiveTableChangeSummary(tableName, operation, recordID string, fields []string) string {
	base := fmt.Sprintf("%s %s changed record %s", humanizeIdentifier(firstNonEmpty(operation, "update")), tableName, recordID)
	if len(fields) == 0 {
		return base
	}
	if len(fields) == 1 {
		return base + " (" + fields[0] + ")"
	}
	if len(fields) == 2 {
		return base + " (" + fields[0] + ", " + fields[1] + ")"
	}
	return base + " (" + fields[0] + ", " + fields[1] + ", +" + fmt.Sprint(len(fields)-2) + " more)"
}

func buildObservedRouteKey(method, path string) string {
	method = normalizeHTTPMethod(method)
	path = normalizeObservedPath(path)
	if path == "" {
		return ""
	}
	if method == "" {
		return path
	}
	return method + " " + path
}

func normalizeObservedPath(path string) string {
	path = normalizeRuntimePath(path)
	if path == "" {
		return "/"
	}
	if path == "/" {
		return path
	}

	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	normalized := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		switch {
		case segment == "":
			continue
		case observabilityAnalysisUUIDPattern.MatchString(segment):
			normalized = append(normalized, ":uuid")
		case observabilityAnalysisIntPattern.MatchString(segment):
			normalized = append(normalized, ":id")
		case observabilityAnalysisHexPattern.MatchString(segment):
			normalized = append(normalized, ":token")
		default:
			normalized = append(normalized, segment)
		}
	}
	if len(normalized) == 0 {
		return "/"
	}
	return "/" + strings.Join(normalized, "/")
}

func observedAt(primary, fallback time.Time) string {
	timestamp := primary
	if timestamp.IsZero() {
		timestamp = fallback
	}
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	return timestamp.UTC().Format(time.RFC3339Nano)
}
