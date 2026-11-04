package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/security"
)

const appRuntimeRequestBodyLimit = 2 << 20

// Indirection over the db calls the service-call handler makes, so tests can
// assert ordering (nothing reads the record store before authorization) without
// a live PostgreSQL instance.
var (
	runtimeResolveApp           = db.ResolveRuntimeApp
	runtimeResolveAppByTable    = db.ResolveRuntimeAppByTable
	runtimeResolveServiceMethod = db.ResolveRuntimeServiceMethod
	runtimeServiceMethodAllowed = db.AppServiceMethodAccessAllowed
	runtimeGetScriptRecord      = db.GetScriptRecord
	runtimeExecuteServiceMethod = db.ExecuteAppServiceMethod
)

func handleAppRuntimeEndpoint(w http.ResponseWriter, r *http.Request) {
	endpoint, found, err := db.ResolveAppRuntimeEndpoint(r.Context(), r.Method, r.URL.Path)
	if err != nil {
		http.Error(w, "Failed to resolve app endpoint", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	userID := auth.UserIDFromRequest(r)
	allowed, err := db.AppEndpointAccessAllowed(r.Context(), endpoint, userID)
	if err != nil {
		http.Error(w, "Failed to authorize app endpoint", http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	payload, input, err := appRuntimeRequestPayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := db.ExecuteAppEndpointScript(
		r.Context(),
		endpoint,
		userID,
		security.RequestIDFromContext(r.Context()),
		input,
		payload,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeAppRuntimeResult(w, result)
}

func handleAppRuntimeServiceCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	payload, input, err := appRuntimeRequestPayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	payloadMap, _ := payload.(map[string]any)
	appName := firstRuntimeString(payloadMap, "app", "scope")
	callName := firstRuntimeString(payloadMap, "call", "method")
	tableName := firstRuntimeString(payloadMap, "table_name", "table")
	recordID := firstRuntimeString(payloadMap, "record_id", "id")
	if appName == "" || callName == "" {
		http.Error(w, "app and call are required", http.StatusBadRequest)
		return
	}

	app, found, err := runtimeResolveApp(r.Context(), appName)
	if err != nil {
		http.Error(w, "Failed to resolve app", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	var methodInput any = input
	if payloadMap != nil {
		if explicitInput, ok := payloadMap["input"]; ok {
			methodInput = explicitInput
		}
	}
	methodPayload := payload
	if payloadMap != nil {
		if explicitPayload, ok := payloadMap["payload"]; ok {
			methodPayload = explicitPayload
		}
	}

	serviceApp, method, found, err := runtimeResolveServiceMethod(r.Context(), app, callName, true)
	if err != nil {
		http.Error(w, "Failed to resolve service method", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "Service method not found", http.StatusNotFound)
		return
	}
	// Authorization runs before anything touches the record store. Previously
	// the record was fetched first (#134), so an unauthorized caller could use
	// the reflected error text as an oracle to enumerate which tables and
	// record ids exist. Any failure below is now reported opaquely.
	allowed, err := runtimeServiceMethodAllowed(r.Context(), serviceApp, method, auth.UserIDFromRequest(r))
	if err != nil {
		http.Error(w, "Failed to authorize service method", http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if tableName != "" {
		if err := validateRuntimeTableName(r.Context(), app, tableName); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
	}

	var record map[string]any
	if tableName != "" && recordID != "" {
		// Reached only after the caller has been authorized for this method.
		// The actor keeps #146's row-level security on the read itself.
		record, err = runtimeGetScriptRecord(r.Context(), tableName, recordID, db.UserScriptActor(auth.UserIDFromRequest(r)))
		if err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
	}

	result, err := runtimeExecuteServiceMethod(r.Context(), db.AppServiceMethodCall{
		App:           app,
		Call:          callName,
		TableName:     tableName,
		UserID:        auth.UserIDFromRequest(r),
		RequestID:     security.RequestIDFromContext(r.Context()),
		Input:         methodInput,
		Payload:       methodPayload,
		Record:        record,
		RequirePublic: true,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeAppRuntimeResult(w, result)
}

// validateRuntimeTableName rejects table names a service call may not read:
// anything that is not a plain identifier, anything on the existing protected
// list, and anything the resolved app does not own.
func validateRuntimeTableName(ctx context.Context, app db.RegisteredApp, tableName string) error {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if !db.IsSafeIdentifier(tableName) {
		return fmt.Errorf("invalid table name")
	}
	if db.IsProtectedTableName(tableName) {
		return fmt.Errorf("protected table")
	}
	owner, found, err := runtimeResolveAppByTable(ctx, tableName)
	if err != nil {
		return err
	}
	if !found || owner.ID != app.ID {
		return fmt.Errorf("table not available to app")
	}
	return nil
}

func appRuntimeRequestPayload(r *http.Request) (any, map[string]any, error) {
	body, err := readAppRuntimeRequestBody(r)
	if err != nil {
		return nil, nil, err
	}
	return appRuntimeRequestPayloadFromBody(r, body)
}

func readAppRuntimeRequestBody(r *http.Request) ([]byte, error) {
	if r == nil || r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, appRuntimeRequestBodyLimit))
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func appRuntimeRequestPayloadFromBody(r *http.Request, body []byte) (any, map[string]any, error) {
	query := make(map[string]any, len(r.URL.Query()))
	for key, values := range r.URL.Query() {
		if len(values) == 1 {
			query[key] = values[0]
			continue
		}
		items := make([]any, 0, len(values))
		for _, value := range values {
			items = append(items, value)
		}
		query[key] = items
	}

	headers := make(map[string]any, len(r.Header))
	for key, values := range r.Header {
		if len(values) == 1 {
			headers[strings.ToLower(key)] = values[0]
			continue
		}
		items := make([]any, 0, len(values))
		for _, value := range values {
			items = append(items, value)
		}
		headers[strings.ToLower(key)] = items
	}

	contentType := strings.TrimSpace(strings.ToLower(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	var payload any
	switch contentType {
	case "application/x-www-form-urlencoded", "multipart/form-data":
		if err := r.ParseForm(); err != nil {
			return nil, nil, err
		}
		formValues := make(map[string]any, len(r.PostForm))
		for key, values := range r.PostForm {
			if len(values) == 1 {
				formValues[key] = values[0]
				continue
			}
			items := make([]any, 0, len(values))
			for _, value := range values {
				items = append(items, value)
			}
			formValues[key] = items
		}
		if len(formValues) > 0 {
			payload = formValues
		}
	default:
		trimmed := strings.TrimSpace(string(body))
		switch {
		case trimmed == "":
		case contentType == "application/json" || json.Valid(body):
			if err := json.Unmarshal(body, &payload); err != nil {
				return nil, nil, err
			}
		default:
			payload = trimmed
		}
	}

	input := map[string]any{
		"method":  r.Method,
		"path":    r.URL.Path,
		"query":   query,
		"headers": headers,
	}
	if payload != nil {
		input["body"] = payload
	}
	return payload, input, nil
}

func writeAppRuntimeResult(w http.ResponseWriter, result db.ScriptExecutionResult) {
	if envelope, ok := result.Result.(map[string]any); ok {
		if writeAppRuntimeEnvelope(w, envelope) {
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":             true,
		"result":         result.Result,
		"logs":           result.Logs,
		"current_record": result.CurrentRecord,
		"duration_ms":    result.DurationMS,
	})
}

func writeAppRuntimeEnvelope(w http.ResponseWriter, envelope map[string]any) bool {
	body, hasBody := envelope["body"]
	if !hasBody {
		return false
	}

	if headers, ok := envelope["headers"].(map[string]any); ok {
		for key, rawValue := range headers {
			value := strings.TrimSpace(fmt.Sprint(rawValue))
			if value == "" {
				continue
			}
			w.Header().Set(key, value)
		}
	}
	status := http.StatusOK
	if rawStatus, ok := envelope["status"]; ok {
		switch value := rawStatus.(type) {
		case float64:
			status = int(value)
		case int:
			status = value
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				status = parsed
			}
		}
	}

	switch value := body.(type) {
	case nil:
		w.WriteHeader(status)
	case string:
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(value))
	default:
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}
	return true
}

func firstRuntimeString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if values == nil {
			return ""
		}
		if value, ok := values[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	return ""
}
