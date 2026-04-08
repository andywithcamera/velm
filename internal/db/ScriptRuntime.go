package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
)

const DefaultScriptExecutionTimeout = 2 * time.Second

type ScriptRuntimeConfig struct {
	Scope       string
	TriggerType string
	TableName   string
	EventName   string
	Language    string
}

type ScriptExecutionOptions struct {
	Code           string
	AppScope       string
	TableName      string
	EventName      string
	TriggerType    string
	Language       string
	UserID         string
	RequestID      string
	Input          any
	Payload        any
	Record         map[string]any
	PreviousRecord map[string]any
	Timeout        time.Duration
	DryRun         bool
	Scope          ScriptScope
}

type ScriptExecutionLog struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type ScriptExecutionResult struct {
	Result        any                  `json:"result"`
	Logs          []ScriptExecutionLog `json:"logs"`
	Output        string               `json:"output"`
	CurrentRecord map[string]any       `json:"current_record,omitempty"`
	DurationMS    int64                `json:"duration_ms"`
}

type scriptRuntimeTimeout struct {
	Timeout time.Duration
}

type scriptRuntimeContext struct {
	ctx              context.Context
	vm               *goja.Runtime
	db               scriptQuerier
	scope            ScriptScope
	modelDescriptors map[string]scriptModelDescriptor
	appScope         string
	currentTableName string
	userID           string
	requestID        string
	logs             []ScriptExecutionLog
}

func GetScriptRuntimeConfig(ctx context.Context, scriptID int64) (ScriptRuntimeConfig, error) {
	if scriptID == 0 {
		return ScriptRuntimeConfig{}, fmt.Errorf("invalid script definition")
	}

	entry, err := GetScriptRegistryEntry(ctx, scriptID)
	if err != nil {
		return ScriptRuntimeConfig{}, err
	}

	return ScriptRuntimeConfig{
		Scope:       strings.TrimSpace(strings.ToLower(entry.AppName)),
		TriggerType: "service",
		TableName:   "",
		EventName:   "",
		Language:    strings.TrimSpace(strings.ToLower(entry.Language)),
	}, nil
}

func ExecuteJavaScript(ctx context.Context, opts ScriptExecutionOptions) (ScriptExecutionResult, error) {
	return executeJavaScriptWithQuerier(ctx, Pool, opts)
}

func executeJavaScriptWithQuerier(ctx context.Context, querier scriptQuerier, opts ScriptExecutionOptions) (ScriptExecutionResult, error) {
	code := strings.TrimSpace(opts.Code)
	if code == "" {
		return ScriptExecutionResult{}, fmt.Errorf("script code is required")
	}

	language := strings.TrimSpace(strings.ToLower(opts.Language))
	if language != "" && language != "javascript" {
		return ScriptExecutionResult{}, fmt.Errorf("unsupported script language %q", language)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultScriptExecutionTimeout
	}

	startedAt := time.Now()
	scope, err := resolveScriptExecutionScope(ctx, opts)
	if err != nil {
		return ScriptExecutionResult{}, err
	}

	runtimeCtx := &scriptRuntimeContext{
		ctx:       ctx,
		vm:        goja.New(),
		db:        querier,
		scope:     scope,
		appScope:  strings.TrimSpace(strings.ToLower(opts.AppScope)),
		userID:    strings.TrimSpace(opts.UserID),
		requestID: strings.TrimSpace(opts.RequestID),
	}
	if runtimeCtx.db == nil {
		runtimeCtx.db = Pool
	}

	if runtimeCtx.appScope == "" && scope.CurrentApp.Name != "" {
		runtimeCtx.appScope = scope.CurrentApp.Name
	}

	if opts.DryRun && Pool != nil {
		tx, err := Pool.Begin(ctx)
		if err != nil {
			return ScriptExecutionResult{}, fmt.Errorf("begin script sandbox transaction: %w", err)
		}
		runtimeCtx.db = tx
		defer func() { _ = tx.Rollback(ctx) }()
	}

	currentTableName, err := runtimeCtx.resolveConfiguredTableName(strings.TrimSpace(strings.ToLower(opts.TableName)))
	if err != nil {
		return ScriptExecutionResult{}, err
	}
	runtimeCtx.currentTableName = currentTableName

	timer := time.AfterFunc(timeout, func() {
		runtimeCtx.vm.Interrupt(scriptRuntimeTimeout{Timeout: timeout})
	})
	defer timer.Stop()

	if err := runtimeCtx.installGlobals(opts); err != nil {
		return ScriptExecutionResult{}, err
	}

	if _, err := runtimeCtx.vm.RunString(code); err != nil {
		return ScriptExecutionResult{}, convertScriptRuntimeError(err)
	}

	runFn, ok := goja.AssertFunction(runtimeCtx.vm.Get("run"))
	if !ok {
		return ScriptExecutionResult{}, fmt.Errorf("script must define run(ctx)")
	}

	ctxValue := runtimeCtx.vm.Get("ctx")
	resultValue, err := runFn(goja.Undefined(), ctxValue)
	if err != nil {
		return ScriptExecutionResult{}, convertScriptRuntimeError(err)
	}

	exportedResult, err := runtimeCtx.resolveResultValue(resultValue)
	if err != nil {
		return ScriptExecutionResult{}, err
	}

	var currentRecord map[string]any
	if recordValue := runtimeCtx.vm.Get("record"); recordValue != nil && !goja.IsUndefined(recordValue) && !goja.IsNull(recordValue) {
		if object := recordValue.ToObject(runtimeCtx.vm); object != nil {
			if exported, ok := normalizeScriptExportValue(object.Export()).(map[string]any); ok {
				currentRecord = exported
			}
		}
	}

	result := ScriptExecutionResult{
		Result:        normalizeScriptExportValue(exportedResult),
		Logs:          runtimeCtx.logs,
		CurrentRecord: currentRecord,
		DurationMS:    time.Since(startedAt).Milliseconds(),
	}
	result.Output = buildScriptOutput(result.Logs, result.Result)
	return result, nil
}

func resolveScriptExecutionScope(ctx context.Context, opts ScriptExecutionOptions) (ScriptScope, error) {
	if opts.Scope.CurrentApp.Name != "" || len(opts.Scope.Objects) > 0 || len(opts.Scope.DependencyApps) > 0 {
		return opts.Scope, nil
	}

	appScope := strings.TrimSpace(strings.ToLower(opts.AppScope))
	if appScope == "" || appScope == "global" {
		return ScriptScope{}, nil
	}
	return GetScriptScope(ctx, appScope)
}

func (runtimeCtx *scriptRuntimeContext) installGlobals(opts ScriptExecutionOptions) error {
	ctxObject := runtimeCtx.vm.NewObject()
	jobsObject := runtimeCtx.vm.NewObject()
	if err := runtimeCtx.defineFunction(jobsObject, "enqueue", runtimeCtx.ctxJobsEnqueue); err != nil {
		return err
	}

	notificationsObject := runtimeCtx.vm.NewObject()
	if err := runtimeCtx.defineFunction(notificationsObject, "send", runtimeCtx.ctxNotificationsSend); err != nil {
		return err
	}

	servicesObject := runtimeCtx.vm.NewObject()
	if err := runtimeCtx.defineFunction(servicesObject, "call", runtimeCtx.ctxServicesCall); err != nil {
		return err
	}

	appObject := runtimeCtx.vm.NewObject()
	appID := runtimeCtx.currentAppID()
	_ = appObject.Set("id", appID)

	userObject := runtimeCtx.vm.NewObject()
	_ = userObject.Set("id", runtimeCtx.userID)

	requestObject := runtimeCtx.vm.NewObject()
	_ = requestObject.Set("id", runtimeCtx.requestID)

	triggerObject := runtimeCtx.vm.NewObject()
	_ = triggerObject.Set("table", runtimeCtx.currentTableName)
	_ = triggerObject.Set("event", strings.TrimSpace(opts.EventName))
	_ = triggerObject.Set("type", strings.TrimSpace(opts.TriggerType))

	if err := runtimeCtx.defineFunction(ctxObject, "log", runtimeCtx.ctxLog); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(ctxObject, "now", runtimeCtx.ctxNow); err != nil {
		return err
	}
	_ = ctxObject.Set("jobs", jobsObject)
	_ = ctxObject.Set("notifications", notificationsObject)
	_ = ctxObject.Set("services", servicesObject)
	_ = ctxObject.Set("app", appObject)
	_ = ctxObject.Set("user", userObject)
	_ = ctxObject.Set("request", requestObject)
	_ = ctxObject.Set("trigger", triggerObject)

	inputValue := runtimeCtx.toValue(opts.Input)
	payloadValue := runtimeCtx.toValue(opts.Payload)

	_ = runtimeCtx.vm.Set("input", inputValue)
	_ = runtimeCtx.vm.Set("payload", payloadValue)
	_ = runtimeCtx.vm.Set("app", appObject)
	_ = runtimeCtx.vm.Set("user", userObject)
	_ = runtimeCtx.vm.Set("trigger", triggerObject)
	_ = runtimeCtx.vm.Set("ctx", ctxObject)

	consoleObject := runtimeCtx.vm.NewObject()
	if err := runtimeCtx.defineFunction(consoleObject, "log", runtimeCtx.consoleLog); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(consoleObject, "info", runtimeCtx.consoleLog); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(consoleObject, "warn", runtimeCtx.consoleLog); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(consoleObject, "error", runtimeCtx.consoleLog); err != nil {
		return err
	}
	_ = runtimeCtx.vm.Set("console", consoleObject)

	return runtimeCtx.installScopeModels(opts.Record, opts.PreviousRecord)
}

func (runtimeCtx *scriptRuntimeContext) installScopeObjects(recordObject *goja.Object) error {
	if runtimeCtx.scope.CurrentApp.Name == "" {
		return nil
	}

	reserved := map[string]bool{
		"app":            true,
		"console":        true,
		"ctx":            true,
		"input":          true,
		"payload":        true,
		"previousRecord": true,
		"record":         true,
		"run":            true,
		"trigger":        true,
		"user":           true,
	}

	namespaces := map[string]*goja.Object{}
	for _, object := range runtimeCtx.scope.Objects {
		if object.App.Name == runtimeCtx.scope.CurrentApp.Name {
			if reserved[object.Alias] {
				continue
			}

			if runtimeCtx.currentTableName != "" && recordObject != nil && object.TableName == runtimeCtx.currentTableName {
				_ = runtimeCtx.vm.Set(object.Alias, recordObject)
				continue
			}

			tableObject, err := runtimeCtx.newTableObject(object.TableName)
			if err != nil {
				return err
			}
			_ = runtimeCtx.vm.Set(object.Alias, tableObject)
			continue
		}

		if reserved[object.App.Name] {
			continue
		}

		namespaceObject, ok := namespaces[object.App.Name]
		if !ok {
			namespaceObject = runtimeCtx.vm.NewObject()
			namespaces[object.App.Name] = namespaceObject
			_ = runtimeCtx.vm.Set(object.App.Name, namespaceObject)
		}

		tableObject, err := runtimeCtx.newTableObject(object.TableName)
		if err != nil {
			return err
		}
		_ = namespaceObject.Set(object.Alias, tableObject)
	}

	return nil
}

func (runtimeCtx *scriptRuntimeContext) newTableObject(tableName string) (*goja.Object, error) {
	object := runtimeCtx.vm.NewObject()
	_ = object.Set("tableName", tableName)
	if err := runtimeCtx.defineFunction(object, "get", runtimeCtx.boundTableGet(tableName)); err != nil {
		return nil, err
	}
	if err := runtimeCtx.defineFunction(object, "list", runtimeCtx.boundTableList(tableName)); err != nil {
		return nil, err
	}
	if err := runtimeCtx.defineFunction(object, "listIDs", runtimeCtx.boundTableListIDs(tableName)); err != nil {
		return nil, err
	}
	if err := runtimeCtx.defineFunction(object, "create", runtimeCtx.boundTableCreate(tableName)); err != nil {
		return nil, err
	}
	if err := runtimeCtx.defineFunction(object, "update", runtimeCtx.boundTableUpdate(tableName)); err != nil {
		return nil, err
	}
	if err := runtimeCtx.defineFunction(object, "bulkPatch", runtimeCtx.boundTableBulkPatch(tableName)); err != nil {
		return nil, err
	}
	if err := runtimeCtx.defineFunction(object, "updateWhere", runtimeCtx.boundTableUpdateWhere(tableName)); err != nil {
		return nil, err
	}
	if err := runtimeCtx.defineFunction(object, "deleteWhere", runtimeCtx.boundTableDeleteWhere(tableName)); err != nil {
		return nil, err
	}
	return object, nil
}

func (runtimeCtx *scriptRuntimeContext) ctxRecordsGet(call goja.FunctionCall) goja.Value {
	tableName := runtimeCtx.mustResolveTableArgument(call.Argument(0))
	id := runtimeCtx.mustStringArgument(call.Argument(1), "record id")
	record, err := getScriptRecordWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return goja.Null()
		}
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(record)
}

func (runtimeCtx *scriptRuntimeContext) ctxRecordsList(call goja.FunctionCall) goja.Value {
	tableName := runtimeCtx.mustResolveTableArgument(call.Argument(0))
	query := runtimeCtx.mustQueryArgument(call.Argument(1))
	items, err := listScriptRecordsWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, query)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(items)
}

func (runtimeCtx *scriptRuntimeContext) ctxRecordsListIDs(call goja.FunctionCall) goja.Value {
	tableName := runtimeCtx.mustResolveTableArgument(call.Argument(0))
	query := runtimeCtx.mustQueryArgument(call.Argument(1))
	ids, err := listScriptRecordIDsWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, query)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(ids)
}

func (runtimeCtx *scriptRuntimeContext) ctxRecordsCreate(call goja.FunctionCall) goja.Value {
	tableName := runtimeCtx.mustResolveTableArgument(call.Argument(0))
	values := runtimeCtx.mustMapArgument(call.Argument(1), "values")
	record, err := createScriptRecordWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, values, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(record)
}

func (runtimeCtx *scriptRuntimeContext) ctxRecordsUpdate(call goja.FunctionCall) goja.Value {
	tableName := runtimeCtx.mustResolveTableArgument(call.Argument(0))
	id := runtimeCtx.mustStringArgument(call.Argument(1), "record id")
	patch := runtimeCtx.mustMapArgument(call.Argument(2), "patch")
	record, err := updateScriptRecordWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, id, patch, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(record)
}

func (runtimeCtx *scriptRuntimeContext) ctxRecordsBulkPatch(call goja.FunctionCall) goja.Value {
	tableName := runtimeCtx.mustResolveTableArgument(call.Argument(0))
	ids := runtimeCtx.mustStringSliceArgument(call.Argument(1), "ids")
	patch := runtimeCtx.mustMapArgument(call.Argument(2), "patch")
	result, err := updateScriptRecordsWhereWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, ScriptRecordQuery{IDs: ids}, patch, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(map[string]any{
		"ids":      result.IDs,
		"affected": result.Affected,
	})
}

func (runtimeCtx *scriptRuntimeContext) ctxRecordsUpdateWhere(call goja.FunctionCall) goja.Value {
	tableName := runtimeCtx.mustResolveTableArgument(call.Argument(0))
	query := runtimeCtx.mustQueryArgument(call.Argument(1))
	patch := runtimeCtx.mustMapArgument(call.Argument(2), "patch")
	result, err := updateScriptRecordsWhereWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, query, patch, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(map[string]any{
		"ids":      result.IDs,
		"affected": result.Affected,
	})
}

func (runtimeCtx *scriptRuntimeContext) ctxRecordsDeleteWhere(call goja.FunctionCall) goja.Value {
	tableName := runtimeCtx.mustResolveTableArgument(call.Argument(0))
	query := runtimeCtx.mustQueryArgument(call.Argument(1))
	result, err := deleteScriptRecordsWhereWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, query, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(map[string]any{
		"ids":      result.IDs,
		"affected": result.Affected,
	})
}

func (runtimeCtx *scriptRuntimeContext) ctxJobsEnqueue(call goja.FunctionCall) goja.Value {
	panic(runtimeCtx.vm.NewGoError(fmt.Errorf("ctx.jobs.enqueue is not implemented")))
}

func (runtimeCtx *scriptRuntimeContext) ctxNotificationsSend(call goja.FunctionCall) goja.Value {
	request := runtimeCtx.mustMapArgument(call.Argument(0), "notification")
	normalized, err := NormalizeScriptNotificationRequest(request, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}

	inputs := make([]UserNotificationCreateInput, 0, len(normalized.UserIDs))
	for _, userID := range normalized.UserIDs {
		inputs = append(inputs, UserNotificationCreateInput{
			UserID:    userID,
			Title:     normalized.Title,
			Body:      normalized.Body,
			Href:      normalized.Href,
			Level:     normalized.Level,
			CreatedBy: runtimeCtx.userID,
		})
	}

	items, err := createUserNotificationsWithQuerier(runtimeCtx.ctx, runtimeCtx.db, inputs)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}

	notifications := make([]map[string]any, 0, len(items))
	for _, item := range items {
		payload := map[string]any{
			"id":        item.ID,
			"userID":    item.UserID,
			"title":     item.Title,
			"body":      item.Body,
			"href":      item.Href,
			"level":     item.Level,
			"createdAt": item.CreatedAt.Format(time.RFC3339Nano),
			"isRead":    item.IsRead,
		}
		if item.IsRead {
			payload["readAt"] = item.ReadAt.Format(time.RFC3339Nano)
		}
		notifications = append(notifications, payload)
	}

	return runtimeCtx.vm.ToValue(map[string]any{
		"created":       len(notifications),
		"notifications": notifications,
	})
}

func (runtimeCtx *scriptRuntimeContext) ctxServicesCall(call goja.FunctionCall) goja.Value {
	currentApp, ok := runtimeCtx.currentRegisteredApp()
	if !ok {
		panic(runtimeCtx.vm.NewGoError(fmt.Errorf("service calls require an application scope")))
	}

	methodCall := runtimeCtx.mustStringArgument(call.Argument(0), "service call")
	var input any
	if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
		input = normalizeScriptExportValue(call.Argument(1).Export())
	}
	var payload any
	if len(call.Arguments) > 2 && !goja.IsUndefined(call.Argument(2)) && !goja.IsNull(call.Argument(2)) {
		payload = normalizeScriptExportValue(call.Argument(2).Export())
	}

	apps, err := ListActiveApps(runtimeCtx.ctx)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	result, err := executeAppServiceMethodWithQuerier(runtimeCtx.ctx, runtimeCtx.db, apps, AppServiceMethodCall{
		App:         currentApp,
		Call:        methodCall,
		TableName:   runtimeCtx.currentTableName,
		TriggerType: "service",
		UserID:      runtimeCtx.userID,
		RequestID:   runtimeCtx.requestID,
		Input:       input,
		Payload:     payload,
		Record:      runtimeCtx.exportRuntimeRecord("record"),
		Previous:    runtimeCtx.exportRuntimeRecord("previousRecord"),
	})
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.toValue(result.Result)
}

func (runtimeCtx *scriptRuntimeContext) ctxLog(call goja.FunctionCall) goja.Value {
	message := runtimeCtx.formatValue(call.Argument(0))
	var data any
	if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
		data = normalizeScriptExportValue(call.Argument(1).Export())
	}
	runtimeCtx.logs = append(runtimeCtx.logs, ScriptExecutionLog{
		Level:   "log",
		Message: message,
		Data:    data,
	})
	return goja.Undefined()
}

func (runtimeCtx *scriptRuntimeContext) ctxNow(call goja.FunctionCall) goja.Value {
	return runtimeCtx.vm.ToValue(time.Now().Format(time.RFC3339Nano))
}

func (runtimeCtx *scriptRuntimeContext) consoleLog(call goja.FunctionCall) goja.Value {
	parts := make([]string, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		parts = append(parts, runtimeCtx.formatValue(arg))
	}
	message := strings.TrimSpace(strings.Join(parts, " "))
	runtimeCtx.logs = append(runtimeCtx.logs, ScriptExecutionLog{
		Level:   "console",
		Message: message,
	})
	return goja.Undefined()
}

func (runtimeCtx *scriptRuntimeContext) boundTableGet(tableName string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		record, err := getScriptRecordWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, runtimeCtx.mustStringArgument(call.Argument(0), "record id"))
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				return goja.Null()
			}
			panic(runtimeCtx.vm.NewGoError(err))
		}
		return runtimeCtx.vm.ToValue(record)
	}
}

func (runtimeCtx *scriptRuntimeContext) boundTableList(tableName string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		query := runtimeCtx.mustQueryArgument(call.Argument(0))
		items, err := listScriptRecordsWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, query)
		if err != nil {
			panic(runtimeCtx.vm.NewGoError(err))
		}
		return runtimeCtx.vm.ToValue(items)
	}
}

func (runtimeCtx *scriptRuntimeContext) boundTableListIDs(tableName string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		query := runtimeCtx.mustQueryArgument(call.Argument(0))
		ids, err := listScriptRecordIDsWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, query)
		if err != nil {
			panic(runtimeCtx.vm.NewGoError(err))
		}
		return runtimeCtx.vm.ToValue(ids)
	}
}

func (runtimeCtx *scriptRuntimeContext) boundTableCreate(tableName string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		values := runtimeCtx.mustMapArgument(call.Argument(0), "values")
		record, err := createScriptRecordWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, values, runtimeCtx.userID)
		if err != nil {
			panic(runtimeCtx.vm.NewGoError(err))
		}
		return runtimeCtx.vm.ToValue(record)
	}
}

func (runtimeCtx *scriptRuntimeContext) boundTableUpdate(tableName string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		id := runtimeCtx.mustStringArgument(call.Argument(0), "record id")
		patch := runtimeCtx.mustMapArgument(call.Argument(1), "patch")
		record, err := updateScriptRecordWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, id, patch, runtimeCtx.userID)
		if err != nil {
			panic(runtimeCtx.vm.NewGoError(err))
		}
		return runtimeCtx.vm.ToValue(record)
	}
}

func (runtimeCtx *scriptRuntimeContext) boundTableBulkPatch(tableName string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		ids := runtimeCtx.mustStringSliceArgument(call.Argument(0), "ids")
		patch := runtimeCtx.mustMapArgument(call.Argument(1), "patch")
		result, err := updateScriptRecordsWhereWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, ScriptRecordQuery{IDs: ids}, patch, runtimeCtx.userID)
		if err != nil {
			panic(runtimeCtx.vm.NewGoError(err))
		}
		return runtimeCtx.vm.ToValue(map[string]any{
			"ids":      result.IDs,
			"affected": result.Affected,
		})
	}
}

func (runtimeCtx *scriptRuntimeContext) boundTableUpdateWhere(tableName string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		query := runtimeCtx.mustQueryArgument(call.Argument(0))
		patch := runtimeCtx.mustMapArgument(call.Argument(1), "patch")
		result, err := updateScriptRecordsWhereWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, query, patch, runtimeCtx.userID)
		if err != nil {
			panic(runtimeCtx.vm.NewGoError(err))
		}
		return runtimeCtx.vm.ToValue(map[string]any{
			"ids":      result.IDs,
			"affected": result.Affected,
		})
	}
}

func (runtimeCtx *scriptRuntimeContext) boundTableDeleteWhere(tableName string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		query := runtimeCtx.mustQueryArgument(call.Argument(0))
		result, err := deleteScriptRecordsWhereWithQuerier(runtimeCtx.ctx, runtimeCtx.db, tableName, query, runtimeCtx.userID)
		if err != nil {
			panic(runtimeCtx.vm.NewGoError(err))
		}
		return runtimeCtx.vm.ToValue(map[string]any{
			"ids":      result.IDs,
			"affected": result.Affected,
		})
	}
}

func (runtimeCtx *scriptRuntimeContext) resolveConfiguredTableName(tableName string) (string, error) {
	if tableName == "" {
		return "", nil
	}
	return runtimeCtx.resolveTableName(tableName)
}

func (runtimeCtx *scriptRuntimeContext) resolveTableName(raw string) (string, error) {
	name := strings.TrimSpace(strings.ToLower(raw))
	if name == "" {
		return "", fmt.Errorf("table name is required")
	}

	if runtimeCtx.scope.CurrentApp.Name != "" {
		if resolved, ok := resolveScriptTableNameInScope(runtimeCtx.scope, name); ok {
			return resolved, nil
		}
		return "", fmt.Errorf("table %q is not available in script scope", raw)
	}

	if !IsSafeIdentifier(name) {
		return "", fmt.Errorf("invalid table name")
	}
	if view := GetView(name); view.Table == nil || view.Table.ID == "" {
		return "", fmt.Errorf("table %q not found", name)
	}
	return name, nil
}

func resolveScriptTableNameInScope(scope ScriptScope, raw string) (string, bool) {
	name := strings.TrimSpace(strings.ToLower(raw))
	if name == "" {
		return "", false
	}

	if ref, err := ResolveScriptScopePath(scope, name); err == nil && ref.ColumnName == "" {
		return ref.TableName, true
	}

	for _, object := range scope.Objects {
		if object.TableName == name {
			return object.TableName, true
		}
	}
	return "", false
}

func (runtimeCtx *scriptRuntimeContext) mustResolveTableArgument(value goja.Value) string {
	tableName, err := runtimeCtx.resolveTableName(runtimeCtx.mustStringArgument(value, "table name"))
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return tableName
}

func (runtimeCtx *scriptRuntimeContext) mustStringArgument(value goja.Value, label string) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		panic(runtimeCtx.vm.NewGoError(fmt.Errorf("%s is required", label)))
	}
	text := strings.TrimSpace(value.String())
	if text == "" {
		panic(runtimeCtx.vm.NewGoError(fmt.Errorf("%s is required", label)))
	}
	return text
}

func (runtimeCtx *scriptRuntimeContext) mustMapArgument(value goja.Value, label string) map[string]any {
	result, err := scriptValueToMap(value)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(fmt.Errorf("%s must be an object", label)))
	}
	if result == nil {
		panic(runtimeCtx.vm.NewGoError(fmt.Errorf("%s must be an object", label)))
	}
	return result
}

func (runtimeCtx *scriptRuntimeContext) mustStringSliceArgument(value goja.Value, label string) []string {
	result, err := scriptValueToStringSlice(value)
	if err != nil || len(result) == 0 {
		panic(runtimeCtx.vm.NewGoError(fmt.Errorf("%s must be a non-empty array", label)))
	}
	return result
}

func (runtimeCtx *scriptRuntimeContext) mustQueryArgument(value goja.Value) ScriptRecordQuery {
	query, err := scriptValueToQuery(value)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return query
}

func (runtimeCtx *scriptRuntimeContext) resolveResultValue(value goja.Value) (any, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}

	exported := value.Export()
	if promise, ok := exported.(*goja.Promise); ok {
		switch promise.State() {
		case goja.PromiseStateFulfilled:
			return normalizeScriptExportValue(promise.Result().Export()), nil
		case goja.PromiseStateRejected:
			return nil, fmt.Errorf("script rejected promise: %s", runtimeCtx.formatValue(promise.Result()))
		default:
			return nil, fmt.Errorf("script returned a pending promise; asynchronous timers and network APIs are not available")
		}
	}

	return normalizeScriptExportValue(exported), nil
}

func (runtimeCtx *scriptRuntimeContext) defineFunction(object *goja.Object, name string, fn func(goja.FunctionCall) goja.Value) error {
	return object.DefineDataProperty(name, runtimeCtx.vm.ToValue(fn), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
}

func (runtimeCtx *scriptRuntimeContext) newDataObject(data map[string]any) *goja.Object {
	if len(data) == 0 {
		return nil
	}

	object := runtimeCtx.vm.NewObject()
	for key, value := range data {
		_ = object.Set(key, normalizeScriptExportValue(value))
	}
	return object
}

func (runtimeCtx *scriptRuntimeContext) toValue(value any) goja.Value {
	if value == nil {
		return goja.Undefined()
	}
	return runtimeCtx.vm.ToValue(normalizeScriptExportValue(value))
}

func (runtimeCtx *scriptRuntimeContext) formatValue(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}

	exported := normalizeScriptExportValue(value.Export())
	switch v := exported.(type) {
	case string:
		return v
	default:
		return scriptFormatValue(v)
	}
}

func (runtimeCtx *scriptRuntimeContext) currentAppID() string {
	if runtimeCtx.scope.CurrentApp.Name != "" {
		return runtimeCtx.scope.CurrentApp.Name
	}
	if runtimeCtx.appScope != "" {
		return runtimeCtx.appScope
	}
	return "global"
}

func (runtimeCtx *scriptRuntimeContext) currentRegisteredApp() (RegisteredApp, bool) {
	name := strings.TrimSpace(strings.ToLower(runtimeCtx.scope.CurrentApp.Name))
	if name == "" {
		name = strings.TrimSpace(strings.ToLower(runtimeCtx.appScope))
	}
	if name == "" {
		return RegisteredApp{}, false
	}
	apps, err := ListActiveApps(runtimeCtx.ctx)
	if err != nil {
		return RegisteredApp{}, false
	}
	return findRegisteredAppByNameOrNamespace(apps, name)
}

func (runtimeCtx *scriptRuntimeContext) exportRuntimeRecord(globalName string) map[string]any {
	value := runtimeCtx.vm.Get(globalName)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	object := value.ToObject(runtimeCtx.vm)
	if object == nil {
		return nil
	}
	exported, ok := normalizeScriptExportValue(object.Export()).(map[string]any)
	if !ok {
		return nil
	}
	return exported
}

func scriptValueToMap(value goja.Value) (map[string]any, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}

	exported := normalizeScriptExportValue(value.Export())
	result, ok := exported.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object")
	}
	return result, nil
}

func scriptValueToStringSlice(value goja.Value) ([]string, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, fmt.Errorf("expected array")
	}

	exported := normalizeScriptExportValue(value.Export())
	rawItems, ok := exported.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array")
	}

	items := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		text := strings.TrimSpace(fmt.Sprint(item))
		if text == "" {
			continue
		}
		items = append(items, text)
	}
	return items, nil
}

func scriptValueToQuery(value goja.Value) (ScriptRecordQuery, error) {
	raw, err := scriptValueToMap(value)
	if err != nil {
		return ScriptRecordQuery{}, err
	}
	if raw == nil {
		return ScriptRecordQuery{}, nil
	}

	query := ScriptRecordQuery{}
	if idsValue, ok := raw["ids"]; ok {
		rawIDs, ok := normalizeScriptExportValue(idsValue).([]any)
		if !ok {
			return ScriptRecordQuery{}, fmt.Errorf("query.ids must be an array")
		}
		query.IDs = make([]string, 0, len(rawIDs))
		for _, item := range rawIDs {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text == "" {
				continue
			}
			query.IDs = append(query.IDs, text)
		}
	}
	if equalsValue, ok := raw["equals"]; ok {
		equalsMap, ok := normalizeScriptExportValue(equalsValue).(map[string]any)
		if !ok {
			return ScriptRecordQuery{}, fmt.Errorf("query.equals must be an object")
		}
		query.Equals = equalsMap
	}
	if filtersValue, ok := raw["filters"]; ok {
		rawFilters, ok := normalizeScriptExportValue(filtersValue).([]any)
		if !ok {
			return ScriptRecordQuery{}, fmt.Errorf("query.filters must be an array")
		}
		query.Filters = make([]ScriptRecordFilter, 0, len(rawFilters))
		for _, item := range rawFilters {
			filterMap, ok := normalizeScriptExportValue(item).(map[string]any)
			if !ok {
				return ScriptRecordQuery{}, fmt.Errorf("query.filters must contain objects")
			}
			column := strings.TrimSpace(fmt.Sprint(filterMap["column"]))
			if column == "" {
				return ScriptRecordQuery{}, fmt.Errorf("query.filters[].column is required")
			}
			operator := strings.TrimSpace(fmt.Sprint(filterMap["operator"]))
			if operator == "" {
				operator = "="
			}
			query.Filters = append(query.Filters, ScriptRecordFilter{
				Column:   column,
				Operator: operator,
				Value:    normalizeScriptExportValue(filterMap["value"]),
			})
		}
	}
	if groupsValue, ok := raw["groups"]; ok {
		rawGroups, ok := normalizeScriptExportValue(groupsValue).([]any)
		if !ok {
			return ScriptRecordQuery{}, fmt.Errorf("query.groups must be an array")
		}
		query.Groups = make([]ScriptRecordFilterGroup, 0, len(rawGroups))
		for _, item := range rawGroups {
			groupMap, ok := normalizeScriptExportValue(item).(map[string]any)
			if !ok {
				return ScriptRecordQuery{}, fmt.Errorf("query.groups must contain objects")
			}
			mode := strings.TrimSpace(fmt.Sprint(groupMap["mode"]))
			rawFilters, ok := normalizeScriptExportValue(groupMap["filters"]).([]any)
			if !ok {
				return ScriptRecordQuery{}, fmt.Errorf("query.groups[].filters must be an array")
			}
			filters := make([]ScriptRecordFilter, 0, len(rawFilters))
			for _, rawFilter := range rawFilters {
				filterMap, ok := normalizeScriptExportValue(rawFilter).(map[string]any)
				if !ok {
					return ScriptRecordQuery{}, fmt.Errorf("query.groups[].filters must contain objects")
				}
				column := strings.TrimSpace(fmt.Sprint(filterMap["column"]))
				if column == "" {
					return ScriptRecordQuery{}, fmt.Errorf("query.groups[].filters[].column is required")
				}
				operator := strings.TrimSpace(fmt.Sprint(filterMap["operator"]))
				if operator == "" {
					operator = "="
				}
				filters = append(filters, ScriptRecordFilter{
					Column:   column,
					Operator: operator,
					Value:    normalizeScriptExportValue(filterMap["value"]),
				})
			}
			query.Groups = append(query.Groups, ScriptRecordFilterGroup{
				Mode:    mode,
				Filters: filters,
			})
		}
	}
	orderByValue, hasOrderBy := raw["orderBy"]
	if !hasOrderBy {
		orderByValue, hasOrderBy = raw["order_by"]
	}
	if hasOrderBy {
		rawOrders, ok := normalizeScriptExportValue(orderByValue).([]any)
		if !ok {
			return ScriptRecordQuery{}, fmt.Errorf("query.orderBy must be an array")
		}
		query.OrderBy = make([]ScriptRecordOrder, 0, len(rawOrders))
		for _, item := range rawOrders {
			orderMap, ok := normalizeScriptExportValue(item).(map[string]any)
			if !ok {
				return ScriptRecordQuery{}, fmt.Errorf("query.orderBy must contain objects")
			}
			column := strings.TrimSpace(fmt.Sprint(orderMap["column"]))
			if column == "" {
				return ScriptRecordQuery{}, fmt.Errorf("query.orderBy[].column is required")
			}
			direction := strings.TrimSpace(fmt.Sprint(orderMap["direction"]))
			query.OrderBy = append(query.OrderBy, ScriptRecordOrder{
				Column:    column,
				Direction: direction,
			})
		}
	}
	if limitValue, ok := raw["limit"]; ok {
		switch v := normalizeScriptExportValue(limitValue).(type) {
		case int:
			query.Limit = v
		case int64:
			query.Limit = int(v)
		case float64:
			query.Limit = int(v)
		case json.Number:
			if parsed, err := v.Int64(); err == nil {
				query.Limit = int(parsed)
			}
		default:
			return ScriptRecordQuery{}, fmt.Errorf("query.limit must be a number")
		}
	}
	if includeDeletedValue, ok := raw["includeDeleted"]; ok {
		boolValue, ok := normalizeScriptExportValue(includeDeletedValue).(bool)
		if !ok {
			return ScriptRecordQuery{}, fmt.Errorf("query.includeDeleted must be a boolean")
		}
		query.IncludeDeleted = boolValue
	}
	return query, nil
}

func normalizeScriptExportValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = normalizeScriptExportValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeScriptExportValue(item)
		}
		return out
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano)
	default:
		return value
	}
}

func buildScriptOutput(logs []ScriptExecutionLog, result any) string {
	lines := make([]string, 0, allocHintSum(len(logs), 1))
	for _, entry := range logs {
		line := strings.TrimSpace(entry.Message)
		if line == "" && entry.Data != nil {
			line = scriptFormatValue(entry.Data)
		} else if line != "" && entry.Data != nil {
			line = line + " " + scriptFormatValue(entry.Data)
		}
		if line != "" {
			lines = append(lines, line)
		}
	}

	resultText := strings.TrimSpace(scriptFormatValue(result))
	if resultText != "" && resultText != "null" {
		lines = append(lines, resultText)
	}

	if len(lines) == 0 {
		return "No output"
	}
	return strings.Join(lines, "\n")
}

func scriptFormatValue(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		normalized := normalizeScriptExportValue(value)
		if raw, err := json.Marshal(normalized); err == nil {
			return string(raw)
		}
		return fmt.Sprint(normalized)
	}
}

func convertScriptRuntimeError(err error) error {
	var interrupt *goja.InterruptedError
	if errors.As(err, &interrupt) {
		if value, ok := interrupt.Value().(scriptRuntimeTimeout); ok {
			return fmt.Errorf("script execution timed out after %s", value.Timeout)
		}
		return fmt.Errorf("script execution interrupted")
	}

	var exception *goja.Exception
	if errors.As(err, &exception) {
		return fmt.Errorf("script execution failed: %s", exception.String())
	}

	return err
}
