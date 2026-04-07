package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"velm/internal/db"
	"strconv"
	"strings"

	"velm/internal/auth"
	"velm/internal/security"
)

type runScriptScopeOption struct {
	Value       string
	Label       string
	Description string
	Selected    bool
}

func handleScriptsView(w http.ResponseWriter, r *http.Request) {
	items, err := db.ListScriptRegistry(r.Context())
	if err != nil {
		http.Error(w, "Failed to load script registry", http.StatusInternalServerError)
		return
	}

	data := newViewData(w, r, "/admin/scripts", "Script Registry", "Admin")
	data["View"] = "script-registry"
	data["ScriptRegistry"] = items

	selectedIDRaw := strings.TrimSpace(r.URL.Query().Get("script_id"))
	if selectedIDRaw != "" {
		if selectedID, err := strconv.ParseInt(selectedIDRaw, 10, 64); err == nil && selectedID != 0 {
			data["ScriptSelectedID"] = selectedID
			for _, item := range items {
				if item.ID == selectedID {
					data["ScriptSelected"] = item
					break
				}
			}
			versions, err := db.ListScriptVersions(r.Context(), selectedID)
			if err == nil {
				data["ScriptVersions"] = versions
			}
		}
	}

	if err := templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, "Error rendering script registry", http.StatusInternalServerError)
	}
}

func handleRunScriptPage(w http.ResponseWriter, r *http.Request) {
	apps, err := db.ListActiveApps(r.Context())
	if err != nil {
		http.Error(w, "Failed to load apps", http.StatusInternalServerError)
		return
	}

	selectedScope := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("scope")))
	scopeOptions, selectedScope := buildRunScriptScopeOptions(apps, selectedScope)

	data := newViewData(w, r, "/admin/run-script", "Run Script", "Admin")
	data["View"] = "run-script"
	data["RunScriptScopeOptions"] = scopeOptions
	data["RunScriptSelectedScope"] = selectedScope
	if err := templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, "Error rendering run script page", http.StatusInternalServerError)
	}
}

func buildRunScriptScopeOptions(apps []db.RegisteredApp, requested string) ([]runScriptScopeOption, string) {
	options := make([]runScriptScopeOption, 0, len(apps))
	requested = strings.TrimSpace(strings.ToLower(requested))
	selected := ""

	for _, app := range apps {
		value := runScriptScopeValue(app)
		if value == "" {
			continue
		}
		if requested != "" && requested == value {
			selected = value
		}

		description := app.Name
		if app.Namespace != "" && app.Namespace != app.Name {
			description = app.Namespace + " namespace"
		}
		options = append(options, runScriptScopeOption{
			Value:       value,
			Label:       app.Label,
			Description: description,
		})
	}

	if selected == "" {
		selected = defaultRunScriptScope(apps)
	}

	for index := range options {
		options[index].Selected = options[index].Value == selected
	}

	return options, selected
}

func defaultRunScriptScope(apps []db.RegisteredApp) string {
	for _, app := range apps {
		if strings.EqualFold(app.Name, "system") || db.IsOOTBBaseApp(app) {
			return runScriptScopeValue(app)
		}
	}
	for _, app := range apps {
		if value := runScriptScopeValue(app); value != "" {
			return value
		}
	}
	return ""
}

func runScriptScopeValue(app db.RegisteredApp) string {
	if scope := strings.TrimSpace(strings.ToLower(app.Namespace)); scope != "" {
		return scope
	}
	return strings.TrimSpace(strings.ToLower(app.Name))
}

func handleTestScriptDryRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	var payload struct {
		ScriptDefID   int64           `json:"script_def_id"`
		Code          string          `json:"code"`
		SamplePayload json.RawMessage `json:"sample_payload"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	code := strings.TrimSpace(payload.Code)
	if code == "" {
		http.Error(w, "code is required", http.StatusBadRequest)
		return
	}
	if len(code) > 200000 {
		http.Error(w, "code too large", http.StatusBadRequest)
		return
	}
	if len(payload.SamplePayload) == 0 || !json.Valid(payload.SamplePayload) {
		http.Error(w, "sample_payload must be valid JSON", http.StatusBadRequest)
		return
	}

	var samplePayload any
	if err := json.Unmarshal(payload.SamplePayload, &samplePayload); err != nil {
		http.Error(w, "sample_payload must be valid JSON", http.StatusBadRequest)
		return
	}

	sum := sha256.Sum256([]byte(code))
	checksum := hex.EncodeToString(sum[:])

	config := db.ScriptRuntimeConfig{Language: "javascript"}
	if payload.ScriptDefID != 0 {
		config, err = db.GetScriptRuntimeConfig(r.Context(), payload.ScriptDefID)
		if err != nil {
			http.Error(w, "Failed to load script definition", http.StatusBadRequest)
			return
		}
	}

	result, execErr := db.ExecuteJavaScript(r.Context(), buildScriptExecutionOptions(
		code,
		config,
		samplePayload,
		auth.UserIDFromRequest(r),
		security.RequestIDFromContext(r.Context()),
		true,
	))

	resp := map[string]any{
		"ok":            true,
		"script_def_id": payload.ScriptDefID,
		"checksum":      checksum,
		"code_size":     len(code),
		"payload_valid": true,
		"message":       "Script executed in JavaScript sandbox.",
	}
	if execErr != nil {
		resp["ok"] = false
		resp["error"] = execErr.Error()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	resp["duration_ms"] = result.DurationMS
	resp["logs"] = result.Logs
	resp["output"] = result.Output
	resp["result"] = result.Result
	if result.CurrentRecord != nil {
		resp["current_record"] = result.CurrentRecord
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func handleScriptScope(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}

	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	result, err := db.GetScriptScope(r.Context(), scope)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func handleRunAdhocScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	var payload struct {
		Code          string          `json:"code"`
		Scope         string          `json:"scope"`
		TableName     string          `json:"table_name"`
		EventName     string          `json:"event_name"`
		TriggerType   string          `json:"trigger_type"`
		SamplePayload json.RawMessage `json:"sample_payload"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	code := strings.TrimSpace(payload.Code)
	if code == "" {
		http.Error(w, "code is required", http.StatusBadRequest)
		return
	}
	if len(code) > 200000 {
		http.Error(w, "code too large", http.StatusBadRequest)
		return
	}

	var samplePayload any
	if len(payload.SamplePayload) > 0 {
		if !json.Valid(payload.SamplePayload) {
			http.Error(w, "sample_payload must be valid JSON", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(payload.SamplePayload, &samplePayload); err != nil {
			http.Error(w, "sample_payload must be valid JSON", http.StatusBadRequest)
			return
		}
	}

	result, err := db.ExecuteJavaScript(r.Context(), buildScriptExecutionOptions(
		code,
		db.ScriptRuntimeConfig{
			Scope:       strings.TrimSpace(payload.Scope),
			TableName:   strings.TrimSpace(payload.TableName),
			EventName:   strings.TrimSpace(payload.EventName),
			TriggerType: strings.TrimSpace(payload.TriggerType),
			Language:    "javascript",
		},
		samplePayload,
		auth.UserIDFromRequest(r),
		security.RequestIDFromContext(r.Context()),
		false,
	))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(result.Output))
}

func buildScriptExecutionOptions(code string, config db.ScriptRuntimeConfig, samplePayload any, userID, requestID string, dryRun bool) db.ScriptExecutionOptions {
	options := db.ScriptExecutionOptions{
		Code:        code,
		AppScope:    strings.TrimSpace(strings.ToLower(config.Scope)),
		TableName:   strings.TrimSpace(strings.ToLower(config.TableName)),
		EventName:   strings.TrimSpace(strings.ToLower(config.EventName)),
		TriggerType: strings.TrimSpace(strings.ToLower(config.TriggerType)),
		Language:    strings.TrimSpace(strings.ToLower(config.Language)),
		UserID:      strings.TrimSpace(userID),
		RequestID:   strings.TrimSpace(requestID),
		Input:       samplePayload,
		Payload:     samplePayload,
		DryRun:      dryRun,
	}

	if payloadMap, ok := samplePayload.(map[string]any); ok {
		if input, exists := payloadMap["input"]; exists {
			options.Input = input
		}
		if payload, exists := payloadMap["payload"]; exists {
			options.Payload = payload
		}
		if record, ok := payloadMap["record"].(map[string]any); ok {
			options.Record = record
		}
		if previous, ok := payloadMap["previousRecord"].(map[string]any); ok {
			options.PreviousRecord = previous
		}
		if options.AppScope == "" {
			if appScope := nestedString(payloadMap, "app", "id"); appScope != "" {
				options.AppScope = strings.TrimSpace(strings.ToLower(appScope))
			}
		}
		if options.TableName == "" {
			if tableName := nestedString(payloadMap, "trigger", "table"); tableName != "" {
				options.TableName = strings.TrimSpace(strings.ToLower(tableName))
			}
		}
		if options.EventName == "" {
			if eventName := nestedString(payloadMap, "trigger", "event"); eventName != "" {
				options.EventName = strings.TrimSpace(strings.ToLower(eventName))
			}
		}
		if options.TriggerType == "" {
			if triggerType := nestedString(payloadMap, "trigger", "type"); triggerType != "" {
				options.TriggerType = strings.TrimSpace(strings.ToLower(triggerType))
			}
		}
	}

	if options.Language == "" {
		options.Language = "javascript"
	}
	return options
}

func nestedString(payload map[string]any, parent, child string) string {
	rawParent, ok := payload[parent]
	if !ok {
		return ""
	}

	parentMap, ok := rawParent.(map[string]any)
	if !ok {
		return ""
	}

	value, _ := parentMap[child].(string)
	return value
}
