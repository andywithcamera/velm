package db

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

type AppRuntimeEndpoint struct {
	App      RegisteredApp
	Endpoint AppDefinitionEndpoint
}

type AppServiceMethodCall struct {
	App           RegisteredApp
	Call          string
	TableName     string
	EventName     string
	TriggerType   string
	UserID        string
	RequestID     string
	Input         any
	Payload       any
	Record        map[string]any
	Previous      map[string]any
	RequirePublic bool
}

type resolvedRuntimeTrigger struct {
	App     RegisteredApp
	Trigger AppDefinitionTrigger
}

func runtimeDefinitionForApp(app RegisteredApp) *AppDefinition {
	if app.Definition != nil {
		return app.Definition
	}
	return app.DraftDefinition
}

func runtimeDependencyApps(apps []RegisteredApp, ownerApp RegisteredApp) map[string]RegisteredApp {
	definition := runtimeDefinitionForApp(ownerApp)
	dependencyApps := make(map[string]RegisteredApp)
	if definition == nil {
		return dependencyApps
	}
	for _, dependency := range definition.Dependencies {
		if dependencyApp, ok := findRegisteredAppByNameOrNamespace(apps, dependency); ok {
			dependencyApps[dependencyApp.Name] = dependencyApp
		}
	}
	return dependencyApps
}

func appScopedRoleName(app RegisteredApp, roleName string) string {
	roleName = strings.TrimSpace(strings.ToLower(roleName))
	if roleName == "" {
		return ""
	}
	scope := strings.TrimSpace(strings.ToLower(app.Namespace))
	if scope == "" {
		scope = strings.TrimSpace(strings.ToLower(app.Name))
	}
	if scope == "" {
		return roleName
	}
	return scope + "." + roleName
}

func appRoleCandidates(app RegisteredApp, roles []string) []string {
	seen := map[string]bool{}
	names := make([]string, 0, allocHintMul(len(roles), 2))
	for _, role := range roles {
		role = strings.TrimSpace(strings.ToLower(role))
		if role == "" {
			continue
		}
		for _, candidate := range []string{appScopedRoleName(app, role), role} {
			candidate = strings.TrimSpace(strings.ToLower(candidate))
			if candidate == "" || seen[candidate] {
				continue
			}
			seen[candidate] = true
			names = append(names, candidate)
		}
	}
	return names
}

func syncPublishedAppRolesTx(ctx context.Context, tx pgx.Tx, app RegisteredApp, definition *AppDefinition) error {
	if definition == nil {
		return nil
	}
	for _, role := range definition.Roles {
		roleName := appScopedRoleName(app, role.Name)
		if roleName == "" {
			continue
		}
		description := strings.TrimSpace(role.Description)
		if description == "" {
			description = strings.TrimSpace(role.Label)
		}
		if description == "" {
			description = fmt.Sprintf("Application role %s for %s", roleName, firstNonEmpty(definition.Label, app.Label, app.Name))
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO _role (name, description, is_system, priority)
			VALUES ($1, $2, FALSE, 200)
			ON CONFLICT (name)
			DO UPDATE SET
				description = EXCLUDED.description,
				_updated_at = NOW()
		`, roleName, description); err != nil {
			return fmt.Errorf("sync app role %q: %w", roleName, err)
		}
	}
	return nil
}

func UserHasAnyRole(ctx context.Context, userID, appID string, roleNames []string) (bool, error) {
	userID = strings.TrimSpace(userID)
	appID = strings.TrimSpace(appID)
	if userID == "" || len(roleNames) == 0 {
		return false, nil
	}

	names := make([]string, 0, len(roleNames))
	seen := map[string]bool{}
	for _, roleName := range roleNames {
		roleName = strings.TrimSpace(strings.ToLower(roleName))
		if roleName == "" || seen[roleName] {
			continue
		}
		seen[roleName] = true
		names = append(names, roleName)
	}
	if len(names) == 0 {
		return false, nil
	}

	const query = `
WITH RECURSIVE seed_roles AS (
	SELECT ur.user_id, ur.role_id
	FROM _user_role ur
	WHERE ur.user_id = $1
	  AND (($2 = '' AND ur.app_id = '') OR ($2 <> '' AND (ur.app_id = '' OR ur.app_id = $2)))
	UNION
	SELECT gm.user_id, gr.role_id
	FROM _group_membership gm
	JOIN _group_role gr ON gr.group_id = gm.group_id
	WHERE gm.user_id = $1
	  AND (($2 = '' AND gr.app_id = '') OR ($2 <> '' AND (gr.app_id = '' OR gr.app_id = $2)))
),
effective_roles AS (
	SELECT sr.user_id, sr.role_id
	FROM seed_roles sr
	UNION
	SELECT er.user_id, ri.inherits_role_id
	FROM effective_roles er
	JOIN _role_inheritance ri ON ri.role_id = er.role_id
)
SELECT EXISTS (
	SELECT 1
	FROM effective_roles er
	JOIN _role r ON r._id = er.role_id
	WHERE er.user_id = $1
	  AND r.name = ANY($3)
)`

	var allowed bool
	if err := Pool.QueryRow(ctx, query, userID, appID, names).Scan(&allowed); err != nil {
		return false, err
	}
	return allowed, nil
}

func ResolveAppRuntimeEndpoint(ctx context.Context, method, path string) (AppRuntimeEndpoint, bool, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return AppRuntimeEndpoint{}, false, err
	}
	endpoint := resolveAppRuntimeEndpointWithApps(apps, method, path)
	if endpoint.App.Name == "" {
		return AppRuntimeEndpoint{}, false, nil
	}
	return endpoint, true, nil
}

func ResolveRuntimeApp(ctx context.Context, name string) (RegisteredApp, bool, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return RegisteredApp{}, false, err
	}
	app, ok := findRegisteredAppByNameOrNamespace(apps, name)
	if !ok {
		return RegisteredApp{}, false, nil
	}
	return app, true, nil
}

func resolveAppRuntimeEndpointWithApps(apps []RegisteredApp, method, path string) AppRuntimeEndpoint {
	method = normalizeHTTPMethod(method)
	path = normalizeRuntimePath(path)
	for _, app := range apps {
		definition := runtimeDefinitionForApp(app)
		if definition == nil {
			continue
		}
		for _, endpoint := range definition.Endpoints {
			if !endpoint.Enabled {
				continue
			}
			if normalizeHTTPMethod(endpoint.Method) != method {
				continue
			}
			if normalizeRuntimePath(endpoint.Path) != path {
				continue
			}
			return AppRuntimeEndpoint{App: app, Endpoint: endpoint}
		}
	}
	return AppRuntimeEndpoint{}
}

func ExecuteAppEndpointScript(ctx context.Context, endpoint AppRuntimeEndpoint, userID, requestID string, input, payload any) (ScriptExecutionResult, error) {
	result, err := ExecuteAppServiceMethod(ctx, AppServiceMethodCall{
		App:           endpoint.App,
		Call:          endpoint.Endpoint.Call,
		TriggerType:   "endpoint",
		UserID:        userID,
		RequestID:     requestID,
		Input:         input,
		Payload:       payload,
		RequirePublic: false,
	})
	if err != nil {
		return ScriptExecutionResult{}, err
	}
	return result, nil
}

func AppEndpointAccessAllowed(ctx context.Context, endpoint AppRuntimeEndpoint, userID string) (bool, error) {
	roleNames := appRoleCandidates(endpoint.App, endpoint.Endpoint.Roles)
	if len(roleNames) == 0 {
		return true, nil
	}
	return UserHasAnyRole(ctx, userID, endpoint.App.ID, roleNames)
}

func ResolveRuntimeServiceMethod(ctx context.Context, currentApp RegisteredApp, call string, requirePublic bool) (RegisteredApp, AppDefinitionMethod, bool, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return RegisteredApp{}, AppDefinitionMethod{}, false, err
	}
	app, method, ok := resolveRuntimeServiceMethodWithApps(apps, currentApp, call, requirePublic)
	return app, method, ok, nil
}

func AppServiceMethodAccessAllowed(ctx context.Context, app RegisteredApp, method AppDefinitionMethod, userID string) (bool, error) {
	roleNames := appRoleCandidates(app, method.Roles)
	if len(roleNames) == 0 {
		return true, nil
	}
	return UserHasAnyRole(ctx, userID, app.ID, roleNames)
}

func ExecuteAppServiceMethod(ctx context.Context, call AppServiceMethodCall) (ScriptExecutionResult, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return ScriptExecutionResult{}, err
	}
	return executeAppServiceMethodWithQuerier(ctx, Pool, apps, call)
}

func executeAppServiceMethodWithQuerier(ctx context.Context, querier scriptQuerier, apps []RegisteredApp, call AppServiceMethodCall) (ScriptExecutionResult, error) {
	currentApp := call.App
	if currentApp.Name == "" && currentApp.Namespace == "" {
		return ScriptExecutionResult{}, fmt.Errorf("app is required")
	}

	serviceApp, method, ok := resolveRuntimeServiceMethodWithApps(apps, currentApp, call.Call, call.RequirePublic)
	if !ok {
		return ScriptExecutionResult{}, fmt.Errorf("service method %q not found", call.Call)
	}
	scopeName := firstNonEmpty(serviceApp.Name, serviceApp.Namespace)
	scope, err := GetScriptScope(ctx, scopeName)
	if err != nil {
		return ScriptExecutionResult{}, err
	}

	return executeJavaScriptWithQuerier(ctx, querier, ScriptExecutionOptions{
		Code:           strings.TrimSpace(method.Script),
		AppScope:       scopeName,
		TableName:      strings.TrimSpace(strings.ToLower(call.TableName)),
		EventName:      strings.TrimSpace(strings.ToLower(call.EventName)),
		TriggerType:    strings.TrimSpace(strings.ToLower(call.TriggerType)),
		Language:       firstNonEmpty(method.Language, "javascript"),
		UserID:         strings.TrimSpace(call.UserID),
		RequestID:      strings.TrimSpace(call.RequestID),
		Input:          call.Input,
		Payload:        call.Payload,
		Record:         cloneValueMap(call.Record),
		PreviousRecord: cloneValueMap(call.Previous),
		Scope:          scope,
	})
}

func RunAppBeforeWriteTriggersWithQuerier(ctx context.Context, querier scriptQuerier, tableName, eventName, userID, requestID string, record, previousRecord map[string]any) (map[string]any, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	eventName = strings.TrimSpace(strings.ToLower(eventName))
	if tableName == "" || eventName == "" {
		return cloneValueMap(record), nil
	}

	apps, err := ListActiveApps(ctx)
	if err != nil {
		return nil, err
	}
	triggers := resolveRuntimeTriggersWithApps(apps, tableName, eventName)
	if len(triggers) == 0 {
		return cloneValueMap(record), nil
	}

	currentRecord := cloneValueMap(record)
	previous := cloneValueMap(previousRecord)
	for _, resolved := range triggers {
		if !runtimeTriggerConditionPasses(resolved.Trigger, currentRecord) {
			continue
		}
		result, err := executeAppServiceMethodWithQuerier(ctx, querier, apps, AppServiceMethodCall{
			App:         resolved.App,
			Call:        resolved.Trigger.Call,
			TableName:   tableName,
			EventName:   eventName,
			TriggerType: "record",
			UserID:      userID,
			RequestID:   requestID,
			Record:      currentRecord,
			Previous:    previous,
		})
		if err != nil {
			return nil, err
		}
		if len(result.CurrentRecord) > 0 {
			currentRecord = result.CurrentRecord
		}
	}
	return currentRecord, nil
}

func resolveRuntimeTriggersWithApps(apps []RegisteredApp, tableName, eventName string) []resolvedRuntimeTrigger {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	eventName = strings.TrimSpace(strings.ToLower(eventName))
	if tableName == "" || eventName == "" {
		return nil
	}

	currentApp, ok := resolveRuntimeAppByTableWithApps(apps, tableName)
	if !ok {
		return nil
	}
	definition := runtimeDefinitionForApp(currentApp)
	if definition == nil {
		return nil
	}

	items := make([]resolvedRuntimeTrigger, 0, allocHintSum(len(definition.Triggers), 4))
	for _, trigger := range definition.Triggers {
		if !trigger.Enabled {
			continue
		}
		if strings.TrimSpace(strings.ToLower(trigger.Table)) != tableName {
			continue
		}
		if strings.TrimSpace(strings.ToLower(trigger.Event)) != eventName {
			continue
		}
		items = append(items, resolvedRuntimeTrigger{App: currentApp, Trigger: trigger})
	}

	for _, table := range definition.Tables {
		if strings.TrimSpace(strings.ToLower(table.Name)) != tableName {
			continue
		}
		for _, trigger := range table.Triggers {
			if !trigger.Enabled {
				continue
			}
			if strings.TrimSpace(strings.ToLower(trigger.Event)) != eventName {
				continue
			}
			if strings.TrimSpace(trigger.Table) == "" {
				trigger.Table = table.Name
			}
			items = append(items, resolvedRuntimeTrigger{App: currentApp, Trigger: trigger})
		}
		break
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Trigger.Order == items[j].Trigger.Order {
			return items[i].Trigger.Name < items[j].Trigger.Name
		}
		return items[i].Trigger.Order < items[j].Trigger.Order
	})
	return items
}

func resolveRuntimeAppByTableWithApps(apps []RegisteredApp, tableName string) (RegisteredApp, bool) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	for _, app := range apps {
		definition := runtimeDefinitionForApp(app)
		if definition == nil {
			continue
		}
		for _, table := range definition.Tables {
			if strings.TrimSpace(strings.ToLower(table.Name)) == tableName {
				return app, true
			}
		}
	}
	return RegisteredApp{}, false
}

func resolveRuntimeServiceMethodWithApps(apps []RegisteredApp, currentApp RegisteredApp, call string, requirePublic bool) (RegisteredApp, AppDefinitionMethod, bool) {
	serviceName, methodName, ok := strings.Cut(strings.TrimSpace(strings.ToLower(call)), ".")
	if !ok || serviceName == "" || methodName == "" {
		return RegisteredApp{}, AppDefinitionMethod{}, false
	}

	if method, ok := findRuntimeServiceMethod(currentApp, serviceName, methodName); ok {
		if !requirePublic || strings.TrimSpace(strings.ToLower(method.Visibility)) == "public" || strings.TrimSpace(strings.ToLower(method.Visibility)) == "" {
			return currentApp, method, true
		}
		return RegisteredApp{}, AppDefinitionMethod{}, false
	}

	for _, dependencyApp := range runtimeDependencyApps(apps, currentApp) {
		method, ok := findRuntimeServiceMethod(dependencyApp, serviceName, methodName)
		if !ok {
			continue
		}
		if strings.TrimSpace(strings.ToLower(method.Visibility)) != "public" {
			continue
		}
		return dependencyApp, method, true
	}
	return RegisteredApp{}, AppDefinitionMethod{}, false
}

func findRuntimeServiceMethod(app RegisteredApp, serviceName, methodName string) (AppDefinitionMethod, bool) {
	definition := runtimeDefinitionForApp(app)
	if definition == nil {
		return AppDefinitionMethod{}, false
	}
	for _, service := range definition.Services {
		if strings.TrimSpace(strings.ToLower(service.Name)) != serviceName {
			continue
		}
		for _, method := range service.Methods {
			if strings.TrimSpace(strings.ToLower(method.Name)) == methodName {
				return method, true
			}
		}
	}
	return AppDefinitionMethod{}, false
}

func runtimeTriggerConditionPasses(trigger AppDefinitionTrigger, record map[string]any) bool {
	condition := strings.TrimSpace(trigger.Condition)
	if condition == "" {
		return true
	}
	values := make(map[string]string, len(record))
	for key, value := range record {
		switch typed := value.(type) {
		case nil:
			values[key] = ""
		case string:
			values[key] = typed
		default:
			values[key] = fmt.Sprint(typed)
		}
	}
	ok, err := evaluateBooleanExpression(condition, values, "")
	return err == nil && ok
}

func normalizeRuntimePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path != "/" {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func cloneValueMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return map[string]any{}
	}
	clone := make(map[string]any, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func roleNamesContain(names []string, target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	return target != "" && slices.Contains(names, target)
}
