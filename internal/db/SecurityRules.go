package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type TableSecurityEvaluator struct {
	app       RegisteredApp
	tableName string
	roleGate  []string
	rules     []AppDefinitionSecurityRule
	userRoles map[string]bool
}

type TableSecuritySavePreview struct {
	Operation     string
	SaveAllowed   bool
	RecordAllowed bool
	ChangedFields []string
	BlockedFields []string
}

func LoadTableSecurityEvaluator(ctx context.Context, tableName, userID string) (*TableSecurityEvaluator, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		return nil, nil
	}
	if protectedTableNames[tableName] {
		return nil, nil
	}

	app, ok, err := ResolveRuntimeAppByTable(ctx, tableName)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	table, ok := findRuntimeTable(app, tableName)
	if !ok {
		return nil, nil
	}

	return loadSecurityEvaluatorForConfig(ctx, app, tableName, table.Security, userID)
}

func LoadFormSecurityEvaluator(ctx context.Context, tableName, formName, userID string) (*TableSecurityEvaluator, error) {
	app, table, form, ok, err := FindYAMLFormByTable(ctx, tableName, formName)
	if err != nil || !ok {
		return nil, err
	}
	return loadSecurityEvaluatorForConfig(ctx, app, table.Name, form.Security, userID)
}

func LoadPageSecurityEvaluator(ctx context.Context, slug, userID string) (*TableSecurityEvaluator, RegisteredApp, AppDefinitionPage, bool, error) {
	app, page, ok, err := FindRuntimePageBySlug(ctx, slug)
	if err != nil || !ok {
		return nil, RegisteredApp{}, AppDefinitionPage{}, ok, err
	}
	evaluator, err := loadSecurityEvaluatorForConfig(ctx, app, "", page.Security, userID)
	if err != nil {
		return nil, RegisteredApp{}, AppDefinitionPage{}, true, err
	}
	return evaluator, app, page, true, nil
}

func loadSecurityEvaluatorForConfig(ctx context.Context, app RegisteredApp, tableName string, security AppDefinitionSecurity, userID string) (*TableSecurityEvaluator, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	userID = strings.TrimSpace(userID)

	roleSet := map[string]bool{}
	if userID != "" {
		roleNames, err := listEffectiveRoleNames(ctx, userID, app.ID)
		if err != nil {
			return nil, err
		}
		roleSet = make(map[string]bool, len(roleNames))
		for _, roleName := range roleNames {
			roleSet[roleName] = true
		}
	}

	rules := make([]AppDefinitionSecurityRule, 0, len(security.Rules))
	for _, rule := range security.Rules {
		if securityRuleTableName(rule, tableName) != tableName {
			continue
		}
		rules = append(rules, rule)
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Order == rules[j].Order {
			return rules[i].Name < rules[j].Name
		}
		return rules[i].Order < rules[j].Order
	})

	roleGate := append([]string(nil), security.Roles...)
	if len(roleGate) == 0 && len(rules) == 0 {
		return nil, nil
	}

	return &TableSecurityEvaluator{
		app:       app,
		tableName: tableName,
		roleGate:  roleGate,
		rules:     rules,
		userRoles: roleSet,
	}, nil
}

func (e *TableSecurityEvaluator) HasOperationRules(operation string) bool {
	if e == nil {
		return false
	}
	operation = normalizeSecurityOperation(operation)
	for _, rule := range e.rules {
		if rule.Operation == operation {
			return true
		}
	}
	return false
}

func (e *TableSecurityEvaluator) HasReadAccessControls() bool {
	if e == nil {
		return false
	}
	return len(e.roleGate) > 0 || e.HasOperationRules("R")
}

func (e *TableSecurityEvaluator) AllowsRecord(operation string, record map[string]any) bool {
	if e == nil {
		return true
	}
	operation = normalizeSecurityOperation(operation)
	if operation == "R" && e.hasAdminReadBypass() {
		return true
	}
	if !e.allowsRoleGate() {
		return false
	}
	hasScopeRules, allowed := e.scopeDecision(operation, "", record)
	if !hasScopeRules {
		return true
	}
	return allowed
}

func (e *TableSecurityEvaluator) AllowsField(operation, field string, record map[string]any) bool {
	if e == nil {
		return true
	}
	operation = normalizeSecurityOperation(operation)
	if operation == "R" && e.hasAdminReadBypass() {
		return true
	}
	if !e.AllowsRecord(operation, record) {
		return false
	}
	return e.allowsFieldRules(operation, field, record)
}

func (e *TableSecurityEvaluator) AllowsFields(operation string, fields []string, record map[string]any) bool {
	if e == nil {
		return true
	}
	operation = normalizeSecurityOperation(operation)
	if operation == "R" && e.hasAdminReadBypass() {
		return true
	}
	if !e.AllowsRecord(operation, record) {
		return false
	}
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(strings.ToLower(field))
		if field == "" || seen[field] {
			continue
		}
		seen[field] = true
		if !e.allowsFieldRules(operation, field, record) {
			return false
		}
	}
	return true
}

func FilterReadableRecord(evaluator *TableSecurityEvaluator, record map[string]any) (map[string]any, bool) {
	if len(record) == 0 {
		if evaluator != nil && !evaluator.AllowsRecord("R", record) {
			return nil, false
		}
		return map[string]any{}, true
	}
	if evaluator != nil && !evaluator.AllowsRecord("R", record) {
		return nil, false
	}
	filtered := make(map[string]any, len(record))
	for key, value := range record {
		filtered[key] = value
	}
	if evaluator == nil {
		return filtered, true
	}
	for key := range filtered {
		if evaluator.AllowsField("R", key, record) {
			continue
		}
		filtered[key] = nil
	}
	return filtered, true
}

func (e *TableSecurityEvaluator) allowsFieldRules(operation, field string, record map[string]any) bool {
	field = strings.TrimSpace(strings.ToLower(field))
	if field == "" {
		return true
	}
	hasScopeRules, allowed := e.scopeDecision(operation, field, record)
	if !hasScopeRules {
		return true
	}
	return allowed
}

func (e *TableSecurityEvaluator) rulesFor(operation, field string) []AppDefinitionSecurityRule {
	operation = normalizeSecurityOperation(operation)
	field = strings.TrimSpace(strings.ToLower(field))

	rules := make([]AppDefinitionSecurityRule, 0, len(e.rules))
	for _, rule := range e.rules {
		if rule.Operation != operation {
			continue
		}
		ruleField := strings.TrimSpace(strings.ToLower(rule.Field))
		if field == "" {
			if ruleField != "" {
				continue
			}
		} else if ruleField != field {
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

func (e *TableSecurityEvaluator) scopeDecision(operation, field string, record map[string]any) (bool, bool) {
	rules := e.rulesFor(operation, field)
	if len(rules) == 0 {
		return false, true
	}
	values := securityRecordValues(record)
	for i := range rules {
		if !e.userHasRole(rules[i].Role) {
			continue
		}
		if !securityRuleConditionPasses(rules[i].Condition, values, field) {
			continue
		}
		return true, securityRuleAllows(rules[i])
	}
	return true, false
}

func (e *TableSecurityEvaluator) userHasRole(role string) bool {
	for _, candidate := range appRoleCandidates(e.app, []string{role}) {
		if e.userRoles[candidate] {
			return true
		}
	}
	return false
}

func (e *TableSecurityEvaluator) allowsRoleGate() bool {
	if e == nil || len(e.roleGate) == 0 {
		return true
	}
	for _, candidate := range appRoleCandidates(e.app, e.roleGate) {
		if e.userRoles[candidate] {
			return true
		}
	}
	return false
}

func (e *TableSecurityEvaluator) hasAdminReadBypass() bool {
	if e == nil {
		return false
	}
	return e.userRoles["admin"]
}

func listEffectiveRoleNames(ctx context.Context, userID, appID string) ([]string, error) {
	const query = effectiveUserRolesByAppCTE + `
		SELECT DISTINCT LOWER(TRIM(r.name)) AS name
		FROM effective_roles er
		JOIN _role r ON r._id = er.role_id
		WHERE er.user_id = $1
		ORDER BY name ASC
	`

	rows, err := Pool.Query(ctx, query, userID, strings.TrimSpace(appID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var roleName string
		if err := rows.Scan(&roleName); err != nil {
			return nil, err
		}
		if roleName != "" {
			roles = append(roles, roleName)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return roles, nil
}

func ListEffectiveRoleNames(ctx context.Context, userID, appID string) ([]string, error) {
	return listEffectiveRoleNames(ctx, userID, appID)
}

func findRuntimeTable(app RegisteredApp, tableName string) (AppDefinitionTable, bool) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	definition := runtimeDefinitionForApp(app)
	if definition == nil {
		return AppDefinitionTable{}, false
	}
	for _, table := range definition.Tables {
		if strings.TrimSpace(strings.ToLower(table.Name)) == tableName {
			return table, true
		}
	}
	return AppDefinitionTable{}, false
}

func securityRuleTableName(rule AppDefinitionSecurityRule, fallback string) string {
	tableName := strings.TrimSpace(strings.ToLower(rule.Table))
	if tableName != "" {
		return tableName
	}
	return strings.TrimSpace(strings.ToLower(fallback))
}

func securityRuleAllows(rule AppDefinitionSecurityRule) bool {
	return normalizeSecurityEffect(rule.Effect) != "deny"
}

func securityRuleConditionPasses(condition string, values map[string]string, field string) bool {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return true
	}
	ok, err := evaluateBooleanExpression(condition, values, strings.TrimSpace(field))
	return err == nil && ok
}

func securityRecordValues(record map[string]any) map[string]string {
	if len(record) == 0 {
		return map[string]string{}
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
	return values
}

func MergeRecordSnapshot(base map[string]any, formData map[string]string, nullColumns map[string]bool) map[string]any {
	if len(base) == 0 && len(formData) == 0 {
		return map[string]any{}
	}

	maxInt := int(^uint(0) >> 1)
	var merged map[string]any
	if len(base) > maxInt-len(formData) {
		merged = make(map[string]any)
	} else {
		merged = make(map[string]any, len(base)+len(formData))
	}

	for key, value := range base {
		merged[key] = value
	}
	for key, value := range formData {
		if nullColumns[key] {
			merged[key] = nil
			continue
		}
		merged[key] = value
	}
	return merged
}

func ChangedRecordFields(columns []Column, oldSnapshot map[string]any, formData map[string]string, nullColumns map[string]bool) []string {
	currentValues := comparableSnapshotValues(columns, oldSnapshot)
	fields := make([]string, 0, len(formData))
	for fieldName, submittedValue := range formData {
		if strings.HasPrefix(fieldName, "_") {
			continue
		}
		expectedValue := submittedValue
		if nullColumns[fieldName] {
			expectedValue = ""
		}
		if currentValues[fieldName] == expectedValue {
			continue
		}
		fields = append(fields, fieldName)
	}
	sort.Strings(fields)
	return fields
}

func StringMapToAny(values map[string]string) map[string]any {
	if len(values) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func BuildTableSecuritySavePreview(columns []Column, evaluator *TableSecurityEvaluator, isCreate bool, oldSnapshot map[string]any, formData map[string]string, nullColumns map[string]bool) TableSecuritySavePreview {
	preview := TableSecuritySavePreview{
		Operation:     "U",
		SaveAllowed:   true,
		RecordAllowed: true,
		ChangedFields: ChangedRecordFields(columns, oldSnapshot, formData, nullColumns),
	}
	if isCreate {
		preview.Operation = "C"
	}
	if evaluator == nil {
		return preview
	}
	if !isCreate && len(preview.ChangedFields) == 0 {
		return preview
	}
	proposedRecord := MergeRecordSnapshot(oldSnapshot, formData, nullColumns)
	if !evaluator.AllowsRecord(preview.Operation, proposedRecord) {
		preview.SaveAllowed = false
		preview.RecordAllowed = false
		return preview
	}
	blockedFields := make([]string, 0, len(preview.ChangedFields))
	for _, field := range preview.ChangedFields {
		if evaluator.AllowsField(preview.Operation, field, proposedRecord) {
			continue
		}
		blockedFields = append(blockedFields, field)
	}
	preview.BlockedFields = blockedFields
	preview.SaveAllowed = len(blockedFields) == 0
	return preview
}

func MergeSecuritySavePreviews(previews ...TableSecuritySavePreview) TableSecuritySavePreview {
	merged := TableSecuritySavePreview{
		SaveAllowed:   true,
		RecordAllowed: true,
	}
	if len(previews) == 0 {
		return merged
	}

	seenChanged := map[string]bool{}
	seenBlocked := map[string]bool{}
	for _, preview := range previews {
		if merged.Operation == "" && preview.Operation != "" {
			merged.Operation = preview.Operation
		}
		merged.SaveAllowed = merged.SaveAllowed && preview.SaveAllowed
		merged.RecordAllowed = merged.RecordAllowed && preview.RecordAllowed
		for _, field := range preview.ChangedFields {
			if seenChanged[field] {
				continue
			}
			seenChanged[field] = true
			merged.ChangedFields = append(merged.ChangedFields, field)
		}
		for _, field := range preview.BlockedFields {
			if seenBlocked[field] {
				continue
			}
			seenBlocked[field] = true
			merged.BlockedFields = append(merged.BlockedFields, field)
		}
	}
	sort.Strings(merged.ChangedFields)
	sort.Strings(merged.BlockedFields)
	return merged
}
