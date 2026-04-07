package db

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const yamlTableIDPrefix = "yaml:table:"

type AppDefinition struct {
	Name          string                       `yaml:"name"`
	Namespace     string                       `yaml:"namespace"`
	Label         string                       `yaml:"label"`
	Description   string                       `yaml:"description"`
	Dependencies  []string                     `yaml:"dependencies"`
	Roles         []AppDefinitionRole          `yaml:"roles"`
	Tables        []AppDefinitionTable         `yaml:"tables"`
	Forms         []AppDefinitionAssetForm     `yaml:"forms,omitempty"`
	Pages         []AppDefinitionPage          `yaml:"pages"`
	ClientScripts []AppDefinitionClientScript  `yaml:"client_scripts"`
	Seeds         []AppDefinitionSeed          `yaml:"seeds"`
	Documentation []AppDefinitionDocumentation `yaml:"documentation"`
	Services      []AppDefinitionService       `yaml:"services"`
	Endpoints     []AppDefinitionEndpoint      `yaml:"endpoints"`
	Triggers      []AppDefinitionTrigger       `yaml:"triggers"`
	Schedules     []AppDefinitionSchedule      `yaml:"schedules"`
}

type AppDefinitionTable struct {
	Name          string                     `yaml:"name"`
	Extends       string                     `yaml:"extends"`
	Extensible    bool                       `yaml:"extensible"`
	LabelSingular string                     `yaml:"label_singular"`
	LabelPlural   string                     `yaml:"label_plural"`
	Description   string                     `yaml:"description"`
	DisplayField  string                     `yaml:"display_field"`
	Columns       []AppDefinitionColumn      `yaml:"columns"`
	Forms         []AppDefinitionForm        `yaml:"forms"`
	Lists         []AppDefinitionList        `yaml:"lists"`
	DataPolicies  []AppDefinitionDataPolicy  `yaml:"data_policies"`
	Triggers      []AppDefinitionTrigger     `yaml:"triggers"`
	RelatedLists  []AppDefinitionRelatedList `yaml:"related_lists"`
	Security      AppDefinitionSecurity      `yaml:"security"`
}

type AppDefinitionColumn struct {
	Name              string         `yaml:"name"`
	Label             string         `yaml:"label"`
	DataType          string         `yaml:"data_type"`
	IsNullable        bool           `yaml:"is_nullable"`
	DefaultValue      string         `yaml:"default_value"`
	Prefix            string         `yaml:"prefix"`
	ValidationRegex   string         `yaml:"validation_regex"`
	ValidationExpr    string         `yaml:"validation_expr"`
	ConditionExpr     string         `yaml:"condition_expr"`
	ValidationMessage string         `yaml:"validation_message"`
	ReferenceTable    string         `yaml:"reference_table"`
	Choices           []ChoiceOption `yaml:"choices"`
}

type AppDefinitionScript struct {
	Name          string                      `yaml:"name"`
	Label         string                      `yaml:"label"`
	Description   string                      `yaml:"description"`
	Language      string                      `yaml:"language"`
	Code          string                      `yaml:"code"`
	Roles         []string                    `yaml:"roles"`
	Endpoint      AppDefinitionScriptEndpoint `yaml:"endpoint"`
	Scope         string                      `yaml:"scope"`
	TriggerType   string                      `yaml:"trigger_type"`
	TableName     string                      `yaml:"table_name"`
	EventName     string                      `yaml:"event_name"`
	ConditionExpr string                      `yaml:"condition_expr"`
	Enabled       bool                        `yaml:"enabled"`
	Status        string                      `yaml:"status"`
	Script        string                      `yaml:"script"`
}

type AppDefinitionClientScript struct {
	Name        string `yaml:"name"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
	Table       string `yaml:"table"`
	Event       string `yaml:"event"`
	Field       string `yaml:"field"`
	Language    string `yaml:"language"`
	Script      string `yaml:"script"`
	Enabled     bool   `yaml:"enabled"`
}

type AppDefinitionPage struct {
	Name           string                `yaml:"name"`
	Slug           string                `yaml:"slug"`
	Label          string                `yaml:"label"`
	Description    string                `yaml:"description"`
	SearchKeywords string                `yaml:"search_keywords,omitempty"`
	EditorMode     string                `yaml:"editor_mode"`
	Status         string                `yaml:"status"`
	Content        string                `yaml:"content"`
	Actions        []AppDefinitionAction `yaml:"actions"`
	Security       AppDefinitionSecurity `yaml:"security"`
}

type AppDefinitionRole struct {
	Name        string `yaml:"name"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
}

type AppDefinitionForm struct {
	Name        string                `yaml:"name"`
	Label       string                `yaml:"label"`
	Description string                `yaml:"description,omitempty"`
	Fields      []string              `yaml:"fields"`
	Actions     []AppDefinitionAction `yaml:"actions,omitempty"`
	Security    AppDefinitionSecurity `yaml:"security,omitempty"`
}

type AppDefinitionAssetForm struct {
	Name        string                `yaml:"name"`
	Table       string                `yaml:"table"`
	Label       string                `yaml:"label"`
	Description string                `yaml:"description"`
	Fields      []string              `yaml:"fields"`
	Layout      []string              `yaml:"layout"`
	Actions     []AppDefinitionAction `yaml:"actions"`
	Security    AppDefinitionSecurity `yaml:"security"`
}

type AppDefinitionList struct {
	Name    string   `yaml:"name"`
	Label   string   `yaml:"label"`
	Columns []string `yaml:"columns"`
}

type AppDefinitionDataPolicy struct {
	Name        string `yaml:"name"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
	Condition   string `yaml:"condition"`
	Action      string `yaml:"action"`
	Enabled     bool   `yaml:"enabled"`
}

type AppDefinitionRelatedList struct {
	Name           string   `yaml:"name"`
	Label          string   `yaml:"label"`
	Table          string   `yaml:"table"`
	ReferenceField string   `yaml:"reference_field"`
	Columns        []string `yaml:"columns"`
}

type AppDefinitionAction struct {
	Name  string   `yaml:"name"`
	Label string   `yaml:"label"`
	Call  string   `yaml:"call"`
	Roles []string `yaml:"roles"`
}

type AppDefinitionSecurityRule struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Order       int    `yaml:"order" json:"order"`
	Effect      string `yaml:"effect,omitempty" json:"effect,omitempty"`
	Operation   string `yaml:"operation" json:"operation"`
	Table       string `yaml:"table" json:"table"`
	Field       string `yaml:"field" json:"field"`
	Condition   string `yaml:"condition" json:"condition"`
	Role        string `yaml:"role" json:"role"`
}

type AppDefinitionSecurity struct {
	Roles []string                    `yaml:"roles" json:"roles"`
	Notes string                      `yaml:"notes" json:"notes"`
	Rules []AppDefinitionSecurityRule `yaml:"rules" json:"rules"`
}

type AppDefinitionScriptEndpoint struct {
	Enabled bool     `yaml:"enabled"`
	Method  string   `yaml:"method"`
	Path    string   `yaml:"path"`
	Roles   []string `yaml:"roles"`
}

type AppDefinitionDocumentation struct {
	Name        string   `yaml:"name"`
	Label       string   `yaml:"label"`
	Description string   `yaml:"description"`
	Category    string   `yaml:"category"`
	Visibility  string   `yaml:"visibility"`
	Content     string   `yaml:"content"`
	Related     []string `yaml:"related"`
}

type AppDefinitionService struct {
	Name        string                `yaml:"name"`
	Label       string                `yaml:"label"`
	Description string                `yaml:"description"`
	Methods     []AppDefinitionMethod `yaml:"methods"`
}

type AppDefinitionMethod struct {
	Name        string   `yaml:"name"`
	Label       string   `yaml:"label"`
	Description string   `yaml:"description"`
	Visibility  string   `yaml:"visibility"`
	Language    string   `yaml:"language"`
	Roles       []string `yaml:"roles"`
	Script      string   `yaml:"script"`
}

type AppDefinitionEndpoint struct {
	Name        string   `yaml:"name"`
	Label       string   `yaml:"label"`
	Description string   `yaml:"description"`
	Method      string   `yaml:"method"`
	Path        string   `yaml:"path"`
	Call        string   `yaml:"call"`
	Roles       []string `yaml:"roles"`
	Enabled     bool     `yaml:"enabled"`
}

type AppDefinitionTrigger struct {
	Name        string `yaml:"name"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
	Event       string `yaml:"event"`
	Table       string `yaml:"table"`
	Condition   string `yaml:"condition"`
	Call        string `yaml:"call"`
	Mode        string `yaml:"mode"`
	Order       int    `yaml:"order"`
	Enabled     bool   `yaml:"enabled"`
}

type AppDefinitionSchedule struct {
	Name        string `yaml:"name"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
	Cron        string `yaml:"cron"`
	Call        string `yaml:"call"`
	Enabled     bool   `yaml:"enabled"`
}

type AppDefinitionSeed struct {
	Table string           `yaml:"table"`
	Rows  []map[string]any `yaml:"rows"`
}

type RegisteredApp struct {
	ID                      string
	Name                    string
	Namespace               string
	Label                   string
	Description             string
	Status                  string
	DefinitionYAML          string
	PublishedDefinitionYAML string
	DefinitionVersion       int64
	PublishedVersion        int64
	Definition              *AppDefinition
	DraftDefinition         *AppDefinition
}

func ParseAppDefinition(content string) (*AppDefinition, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil
	}

	var legacy struct {
		Scripts []AppDefinitionScript `yaml:"scripts"`
	}
	if err := yaml.Unmarshal([]byte(content), &legacy); err == nil && len(legacy.Scripts) > 0 {
		return nil, fmt.Errorf("top-level scripts are no longer supported; migrate them to client_scripts, endpoints, and services.methods")
	}

	var definition AppDefinition
	if err := yaml.Unmarshal([]byte(content), &definition); err != nil {
		return nil, fmt.Errorf("parse app definition yaml: %w", err)
	}
	if err := normalizeAppDefinition(&definition); err != nil {
		return nil, err
	}
	return &definition, nil
}

func GetActiveAppByName(ctx context.Context, appName string) (RegisteredApp, error) {
	appName = strings.TrimSpace(strings.ToLower(appName))
	if appName == "" {
		return RegisteredApp{}, fmt.Errorf("app name is required")
	}

	apps, err := ListActiveApps(ctx)
	if err != nil {
		return RegisteredApp{}, err
	}
	if app, ok := findRegisteredAppByNameOrNamespace(apps, appName); ok {
		return app, nil
	}
	return RegisteredApp{}, fmt.Errorf("app not found")
}

func CreateApp(ctx context.Context, namespace, label, description, userID string) error {
	namespace = strings.TrimSpace(strings.ToLower(namespace))
	label = strings.TrimSpace(label)
	description = strings.TrimSpace(description)
	if namespace == "" {
		return fmt.Errorf(`field "namespace" is required`)
	}
	if label == "" {
		label = strings.ToUpper(namespace)
	}

	app := RegisteredApp{
		Name:        namespace,
		Namespace:   namespace,
		Label:       label,
		Description: description,
		Status:      "active",
	}
	definition := &AppDefinition{
		Name:        namespace,
		Namespace:   namespace,
		Label:       label,
		Description: description,
	}
	if err := prepareDefinitionForApp(app, definition); err != nil {
		return err
	}
	if err := validateAppDefinitionForApp(ctx, app, definition); err != nil {
		return err
	}

	content, err := yaml.Marshal(definition)
	if err != nil {
		return fmt.Errorf("marshal app definition yaml: %w", err)
	}

	_, err = Pool.Exec(ctx, `
		INSERT INTO _app (
			name,
			namespace,
			label,
			description,
			status,
			definition_yaml,
			published_definition_yaml,
			definition_version,
			published_version,
			_created_by,
			_updated_by
		)
		VALUES (
			$1, $2, $3, $4, 'active',
			$5, '',
			1, 0,
			NULLIF($6, '')::uuid,
			NULLIF($6, '')::uuid
		)
	`, namespace, namespace, label, description, string(content), strings.TrimSpace(userID))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("app %q already exists", namespace)
		}
		return fmt.Errorf("create app: %w", err)
	}
	return nil
}

func SaveAppDefinition(ctx context.Context, appName string, definition *AppDefinition) error {
	app, err := GetActiveAppByName(ctx, appName)
	if err != nil {
		return err
	}
	if appUsesOOTBLandingPage(app) {
		ensureSystemLandingPage(definition)
	}
	if err := prepareDefinitionForApp(app, definition); err != nil {
		return err
	}
	if err := validateAppDefinitionForApp(ctx, app, definition); err != nil {
		return err
	}

	content, err := yaml.Marshal(definition)
	if err != nil {
		return fmt.Errorf("marshal app definition yaml: %w", err)
	}

	commandTag, err := Pool.Exec(ctx, `
		UPDATE _app
		SET definition_yaml = $2,
			definition_version = CASE
				WHEN COALESCE(definition_yaml, '') = $2 THEN definition_version
				ELSE GREATEST(definition_version, published_version) + 1
			END,
			_updated_at = NOW()
		WHERE name = $1 OR namespace = $1
	`, app.Name, string(content))
	if err != nil {
		return fmt.Errorf("save app definition yaml: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}
	return nil
}

func PublishAppDefinition(ctx context.Context, appName string) error {
	app, err := GetActiveAppByName(ctx, appName)
	if err != nil {
		return err
	}

	definition := app.DraftDefinition
	if definition == nil {
		definition = app.Definition
	}
	if definition == nil {
		return fmt.Errorf("definition is required")
	}
	definition = cloneAppDefinition(definition)
	publishedDefinition := cloneAppDefinition(app.Definition)
	if appUsesOOTBLandingPage(app) {
		ensureSystemLandingPage(definition)
	}
	if err := prepareDefinitionForApp(app, definition); err != nil {
		return err
	}
	if err := validateAppDefinitionForApp(ctx, app, definition); err != nil {
		return err
	}

	content, err := yaml.Marshal(definition)
	if err != nil {
		return fmt.Errorf("marshal app definition yaml: %w", err)
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin publish app definition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := reconcileDefinitionSchemaTx(ctx, tx, app, publishedDefinition, definition); err != nil {
		return err
	}
	if appOwnsPhysicalSchema(app) {
		if err := applyDefinitionSchemaTx(ctx, tx, definition); err != nil {
			return err
		}
	}

	commandTag, err := tx.Exec(ctx, `
		UPDATE _app
		SET label = $2,
			description = $3,
			published_definition_yaml = $4,
			published_version = GREATEST(definition_version, 1),
			_updated_at = NOW()
		WHERE name = $1 OR namespace = $1
	`, app.Name, definition.Label, definition.Description, string(content))
	if err != nil {
		return fmt.Errorf("publish app definition yaml: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}
	if err := syncPublishedAppRolesTx(ctx, tx, app, definition); err != nil {
		return err
	}
	if err := syncPublishedAppPagesTx(ctx, tx, app, publishedDefinition, definition); err != nil {
		return err
	}
	if appUsesOOTBLandingPage(app) {
		if err := syncSystemLandingPageAndRouteTx(ctx, tx, definition); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publish app definition: %w", err)
	}
	if err := EnsurePublishedAppSeeds(ctx, app.Name); err != nil {
		return err
	}
	return nil
}

func prepareDefinitionForApp(app RegisteredApp, definition *AppDefinition) error {
	if definition == nil {
		return fmt.Errorf("definition is required")
	}
	if err := normalizeAppDefinition(definition); err != nil {
		return err
	}

	if definition.Name == "" {
		definition.Name = app.Name
	}
	if definition.Namespace == "" {
		definition.Namespace = app.Namespace
	}
	if definition.Name != app.Name {
		return fmt.Errorf("app name is immutable")
	}
	if definition.Namespace != app.Namespace {
		return fmt.Errorf("app namespace is immutable")
	}
	return nil
}

func cloneAppDefinition(definition *AppDefinition) *AppDefinition {
	if definition == nil {
		return nil
	}
	content, err := yaml.Marshal(definition)
	if err != nil {
		copy := *definition
		return &copy
	}
	cloned, err := ParseAppDefinition(string(content))
	if err != nil {
		copy := *definition
		return &copy
	}
	return cloned
}

func CloneAppDefinition(definition *AppDefinition) *AppDefinition {
	return cloneAppDefinition(definition)
}

func UpsertAppFormDefinition(ctx context.Context, appName, tableName, formName, label string, fields []string) error {
	app, err := GetActiveAppByName(ctx, appName)
	if err != nil {
		return err
	}

	definition := cloneAppDefinition(app.DraftDefinition)
	if definition == nil {
		definition = cloneAppDefinition(app.Definition)
	}
	if definition == nil {
		definition = &AppDefinition{
			Name:        app.Name,
			Namespace:   app.Namespace,
			Label:       app.Label,
			Description: app.Description,
		}
	}

	tableName = strings.TrimSpace(strings.ToLower(tableName))
	formName = strings.TrimSpace(strings.ToLower(formName))
	label = strings.TrimSpace(label)
	if formName == "" {
		formName = "default"
	}
	if label == "" {
		label = humanizeIdentifier(formName)
	}

	table, index, err := ensureDefinitionTable(ctx, definition, tableName)
	if err != nil {
		return err
	}

	allowed := map[string]bool{}
	for _, column := range table.Columns {
		allowed[column.Name] = true
	}

	normalizedFields := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(strings.ToLower(field))
		if field == "" || seen[field] || !allowed[field] {
			continue
		}
		seen[field] = true
		normalizedFields = append(normalizedFields, field)
	}

	formIndex := -1
	for i, form := range table.Forms {
		if form.Name == formName {
			formIndex = i
			break
		}
	}
	form := AppDefinitionForm{
		Name:   formName,
		Label:  label,
		Fields: normalizedFields,
	}
	if formIndex >= 0 {
		table.Forms[formIndex] = form
	} else {
		table.Forms = append(table.Forms, form)
	}
	definition.Tables[index] = *table

	return SaveAppDefinition(ctx, app.Name, definition)
}

func ResolveTableForms(table AppDefinitionTable) []AppDefinitionForm {
	if len(table.Forms) > 0 {
		return table.Forms
	}

	fields := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		if strings.HasPrefix(column.Name, "_") {
			continue
		}
		fields = append(fields, column.Name)
	}
	return []AppDefinitionForm{{
		Name:   "default",
		Label:  "Default Form",
		Fields: fields,
	}}
}

func ResolveTableLists(table AppDefinitionTable) []AppDefinitionList {
	if len(table.Lists) > 0 {
		return table.Lists
	}

	columns := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		if strings.HasPrefix(column.Name, "_") {
			continue
		}
		columns = append(columns, column.Name)
	}
	return []AppDefinitionList{{
		Name:    "default",
		Label:   "Default List",
		Columns: columns,
	}}
}

func ResolveDefaultTableList(table AppDefinitionTable) AppDefinitionList {
	lists := ResolveTableLists(table)
	for _, list := range lists {
		if list.Name == "default" {
			return list
		}
	}
	return lists[0]
}

func ResolveDefaultTableForm(table AppDefinitionTable) AppDefinitionForm {
	forms := ResolveTableForms(table)
	for _, form := range forms {
		if form.Name == "default" {
			return form
		}
	}
	return forms[0]
}

func ResolveTableForm(table AppDefinitionTable, formName string) (AppDefinitionForm, bool) {
	forms := ResolveTableForms(table)
	if len(forms) == 0 {
		return AppDefinitionForm{}, false
	}

	formName = strings.TrimSpace(strings.ToLower(formName))
	if formName == "" {
		return ResolveDefaultTableForm(table), true
	}

	for _, form := range forms {
		if strings.TrimSpace(strings.ToLower(form.Name)) == formName {
			return form, true
		}
	}
	return AppDefinitionForm{}, false
}

func FindYAMLFormByTable(ctx context.Context, tableName, formName string) (RegisteredApp, AppDefinitionTable, AppDefinitionForm, bool, error) {
	app, table, ok, err := FindYAMLTableByName(ctx, tableName)
	if err != nil || !ok {
		return RegisteredApp{}, AppDefinitionTable{}, AppDefinitionForm{}, ok, err
	}
	form, found := ResolveTableForm(table, formName)
	return app, table, form, found, nil
}

func FindYAMLDefaultFormByTable(ctx context.Context, tableName string) (RegisteredApp, AppDefinitionTable, AppDefinitionForm, bool, error) {
	return FindYAMLFormByTable(ctx, tableName, "")
}

func FindYAMLDefaultListByTable(ctx context.Context, tableName string) (RegisteredApp, AppDefinitionTable, AppDefinitionList, bool, error) {
	app, table, ok, err := FindYAMLTableByName(ctx, tableName)
	if err != nil || !ok {
		return RegisteredApp{}, AppDefinitionTable{}, AppDefinitionList{}, ok, err
	}
	return app, table, ResolveDefaultTableList(table), true, nil
}

func ListActiveApps(ctx context.Context) ([]RegisteredApp, error) {
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		if apps, err, ok := cache.cachedActiveApps(); ok {
			return apps, err
		}
	}

	apps, err := listActiveAppsUncached(ctx)
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		cache.storeActiveApps(apps, err)
	}
	return apps, err
}

func listActiveAppsUncached(ctx context.Context) ([]RegisteredApp, error) {
	rows, err := Pool.Query(ctx, `
		SELECT
			_id::text,
			name,
			namespace,
			label,
			COALESCE(description, ''),
			status,
			COALESCE(definition_yaml, ''),
			COALESCE(published_definition_yaml, ''),
			COALESCE(definition_version, 0),
			COALESCE(published_version, 0)
		FROM _app
		WHERE _deleted_at IS NULL
		  AND status = 'active'
		ORDER BY label ASC, name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list registered apps: %w", err)
	}
	defer rows.Close()

	apps := make([]RegisteredApp, 0, 8)
	for rows.Next() {
		var app RegisteredApp
		if err := rows.Scan(
			&app.ID,
			&app.Name,
			&app.Namespace,
			&app.Label,
			&app.Description,
			&app.Status,
			&app.DefinitionYAML,
			&app.PublishedDefinitionYAML,
			&app.DefinitionVersion,
			&app.PublishedVersion,
		); err != nil {
			return nil, fmt.Errorf("scan registered apps: %w", err)
		}

		app.Name = strings.TrimSpace(strings.ToLower(app.Name))
		app.Namespace = strings.TrimSpace(strings.ToLower(app.Namespace))
		app.Label = strings.TrimSpace(app.Label)
		if app.Label == "" {
			app.Label = strings.ToUpper(app.Name)
		}

		if strings.TrimSpace(app.PublishedDefinitionYAML) != "" {
			definition, err := ParseAppDefinition(app.PublishedDefinitionYAML)
			if err != nil {
				return nil, fmt.Errorf("app %s published definition: %w", app.Name, err)
			}
			app.Definition = definition
		}

		if strings.TrimSpace(app.DefinitionYAML) != "" {
			definition, err := ParseAppDefinition(app.DefinitionYAML)
			if err == nil {
				app.DraftDefinition = definition
			} else if app.Definition == nil {
				return nil, fmt.Errorf("app %s draft definition: %w", app.Name, err)
			}
		}

		if app.DraftDefinition == nil {
			app.DraftDefinition = cloneAppDefinition(app.Definition)
			if strings.TrimSpace(app.DefinitionYAML) == "" && strings.TrimSpace(app.PublishedDefinitionYAML) != "" {
				app.DefinitionYAML = app.PublishedDefinitionYAML
			}
		}

		if app.Definition != nil {
			if label := strings.TrimSpace(app.Definition.Label); label != "" {
				app.Label = label
			}
			if description := strings.TrimSpace(app.Definition.Description); description != "" {
				app.Description = description
			}
		}

		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate registered apps: %w", err)
	}

	return apps, nil
}

func FindYAMLTableByName(ctx context.Context, tableName string) (RegisteredApp, AppDefinitionTable, bool, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		return RegisteredApp{}, AppDefinitionTable{}, false, nil
	}

	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		if app, table, ok, indexed := cache.cachedYAMLTableByName(tableName); indexed {
			return app, table, ok, nil
		}
	}

	apps, err := ListActiveApps(ctx)
	if err != nil {
		return RegisteredApp{}, AppDefinitionTable{}, false, err
	}
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		if app, table, ok, indexed := cache.cachedYAMLTableByName(tableName); indexed {
			return app, table, ok, nil
		}
	}
	for _, app := range apps {
		if app.Definition == nil {
			continue
		}
		for _, table := range app.Definition.Tables {
			if table.Name == tableName {
				return app, table, true, nil
			}
		}
	}

	return RegisteredApp{}, AppDefinitionTable{}, false, nil
}

func FindYAMLTableByID(ctx context.Context, tableID string) (RegisteredApp, AppDefinitionTable, bool, error) {
	appName, tableName, ok := parseYAMLTableID(tableID)
	if !ok {
		return RegisteredApp{}, AppDefinitionTable{}, false, nil
	}

	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		if app, table, ok, indexed := cache.cachedYAMLTableByID(yamlTableID(appName, tableName)); indexed {
			return app, table, ok, nil
		}
	}

	apps, err := ListActiveApps(ctx)
	if err != nil {
		return RegisteredApp{}, AppDefinitionTable{}, false, err
	}
	if cache := requestMetadataCacheFromContext(ctx); cache != nil {
		if app, table, ok, indexed := cache.cachedYAMLTableByID(yamlTableID(appName, tableName)); indexed {
			return app, table, ok, nil
		}
	}
	for _, app := range apps {
		if app.Name != appName || app.Definition == nil {
			continue
		}
		for _, table := range app.Definition.Tables {
			if table.Name == tableName {
				return app, table, true, nil
			}
		}
	}

	return RegisteredApp{}, AppDefinitionTable{}, false, nil
}

func BuildYAMLBuilderTable(app RegisteredApp, table AppDefinitionTable) BuilderTableSummary {
	return BuildYAMLBuilderTableWithContext(context.Background(), app, table)
}

func BuildYAMLBuilderTableWithContext(ctx context.Context, app RegisteredApp, table AppDefinitionTable) BuilderTableSummary {
	columnCount := len(BuildYAMLBuilderColumnsWithContext(ctx, app, table))
	return BuilderTableSummary{
		ID:            yamlTableID(app.Name, table.Name),
		Name:          table.Name,
		LabelSingular: table.LabelSingular,
		LabelPlural:   table.LabelPlural,
		Description:   table.Description,
		ColumnCount:   columnCount,
	}
}

func BuildYAMLBuilderColumns(app RegisteredApp, table AppDefinitionTable) []BuilderColumnSummary {
	return BuildYAMLBuilderColumnsWithContext(context.Background(), app, table)
}

func BuildYAMLBuilderColumnsWithContext(ctx context.Context, app RegisteredApp, table AppDefinitionTable) []BuilderColumnSummary {
	resolvedColumns := resolveDefinitionColumnsBestEffortWithContext(ctx, app, table)
	columns := make([]BuilderColumnSummary, 0, len(resolvedColumns)+len(recordSystemColumnTemplates))
	if definitionUsesImplicitSystemColumns(app.Name, app.Namespace, table) {
		columns = append(columns, buildSystemBuilderColumns(app.Name, table.Name)...)
	}
	for _, column := range resolvedColumns {
		columns = append(columns, BuilderColumnSummary{
			ID:                yamlColumnID(app.Name, table.Name, column.Name),
			Name:              column.Name,
			Label:             column.Label,
			DataType:          column.DataType,
			IsNullable:        column.IsNullable,
			DefaultValue:      column.DefaultValue,
			ValidationRule:    column.ValidationRegex,
			ValidationExpr:    column.ValidationExpr,
			ConditionExpr:     column.ConditionExpr,
			ValidationMessage: column.ValidationMessage,
			ReferenceTable:    column.ReferenceTable,
			Choices:           append([]ChoiceOption(nil), column.Choices...),
			Prefix:            column.Prefix,
		})
	}
	return columns
}

func BuildYAMLTable(app RegisteredApp, table AppDefinitionTable) Table {
	return Table{
		ID:             yamlTableID(app.Name, table.Name),
		NAME:           table.Name,
		CREATED_AT:     time.Time{},
		CREATED_BY:     "",
		UPDATED_AT:     time.Time{},
		UPDATED_BY:     "",
		LABEL_SINGULAR: table.LabelSingular,
		LABEL_PLURAL:   table.LabelPlural,
		DESCRIPTION:    table.Description,
		DISPLAY_FIELD:  resolveDefinitionDisplayField(table),
	}
}

func BuildYAMLColumns(app RegisteredApp, table AppDefinitionTable) []Column {
	return BuildYAMLColumnsWithContext(context.Background(), app, table)
}

func BuildYAMLColumnsWithContext(ctx context.Context, app RegisteredApp, table AppDefinitionTable) []Column {
	resolvedColumns := resolveDefinitionColumnsBestEffortWithContext(ctx, app, table)
	columns := make([]Column, 0, len(resolvedColumns)+len(recordSystemColumnTemplates))
	if definitionUsesImplicitSystemColumns(app.Name, app.Namespace, table) {
		columns = append(columns, buildSystemYAMLColumns(app.Name, table.Name)...)
	}
	for _, column := range resolvedColumns {
		item := Column{
			ID:               yamlColumnID(app.Name, table.Name, column.Name),
			NAME:             column.Name,
			CREATED_AT:       time.Time{},
			CREATED_BY:       "",
			UPDATED_AT:       time.Time{},
			UPDATED_BY:       "",
			LABEL:            column.Label,
			DATA_TYPE:        column.DataType,
			IS_NULLABLE:      column.IsNullable,
			DEFAULT_VALUE:    nullableString(column.DefaultValue),
			IS_HIDDEN:        strings.HasPrefix(column.Name, "_"),
			IS_READONLY:      IsSystemColumnName(column.Name),
			VALIDATION_REGEX: nullableString(column.ValidationRegex),
			VALIDATION_EXPR:  nullableString(column.ValidationExpr),
			CONDITION_EXPR:   nullableString(column.ConditionExpr),
			VALIDATION_MSG:   nullableString(column.ValidationMessage),
			REFERENCE_TABLE:  nullableString(column.ReferenceTable),
			PREFIX:           nullableString(column.Prefix),
			CHOICES:          append([]ChoiceOption(nil), column.Choices...),
			TABLE_ID:         yamlTableID(app.Name, table.Name),
		}
		columns = append(columns, item)
	}
	return columns
}

func ResolveDefinitionColumns(ctx context.Context, app RegisteredApp, table AppDefinitionTable) ([]AppDefinitionColumn, error) {
	if strings.TrimSpace(table.Extends) == "" {
		return append([]AppDefinitionColumn(nil), table.Columns...), nil
	}

	apps := []RegisteredApp{app}
	if Pool != nil {
		activeApps, err := ListActiveApps(ctx)
		if err != nil {
			return nil, err
		}
		apps = mergeRegisteredApps(activeApps, app)
	}
	return resolveDefinitionColumnsWithApps(apps, app, table, map[string]bool{})
}

func resolveDefinitionColumnsBestEffort(app RegisteredApp, table AppDefinitionTable) []AppDefinitionColumn {
	return resolveDefinitionColumnsBestEffortWithContext(context.Background(), app, table)
}

func resolveDefinitionColumnsBestEffortWithContext(ctx context.Context, app RegisteredApp, table AppDefinitionTable) []AppDefinitionColumn {
	if strings.TrimSpace(table.Extends) == "" {
		return append([]AppDefinitionColumn(nil), table.Columns...)
	}

	resolved, err := ResolveDefinitionColumns(ctx, app, table)
	if err != nil || len(resolved) == 0 {
		return append([]AppDefinitionColumn(nil), table.Columns...)
	}
	return resolved
}

func resolveDefinitionColumnsWithApps(apps []RegisteredApp, ownerApp RegisteredApp, table AppDefinitionTable, visited map[string]bool) ([]AppDefinitionColumn, error) {
	tableName := strings.TrimSpace(strings.ToLower(table.Name))
	if tableName == "" {
		return nil, fmt.Errorf("table name is required")
	}
	key := strings.TrimSpace(strings.ToLower(ownerApp.Name)) + ":" + tableName
	if visited[key] {
		return nil, fmt.Errorf("cyclic table inheritance detected at %q", tableName)
	}
	visited[key] = true
	defer delete(visited, key)

	if strings.TrimSpace(table.Extends) == "" {
		return append([]AppDefinitionColumn(nil), table.Columns...), nil
	}

	currentDefinition := effectiveRegisteredAppDefinition(ownerApp)
	if currentDefinition == nil {
		return nil, fmt.Errorf("definition not available for app %q", ownerApp.Name)
	}

	dependencyApps := dependencyAppsForRegisteredApp(apps, ownerApp)
	parentApp, parentTable, ok := resolveValidationTable(ownerApp, currentDefinition, dependencyApps, table.Extends)
	if !ok {
		return nil, fmt.Errorf("extended table %q not found", table.Extends)
	}

	inherited, err := resolveDefinitionColumnsWithApps(apps, parentApp, parentTable, visited)
	if err != nil {
		return nil, err
	}

	columns := make([]AppDefinitionColumn, 0, len(inherited)+len(table.Columns))
	columns = append(columns, inherited...)
	columns = append(columns, table.Columns...)
	return applyDerivedTaskAutoNumberPrefix(apps, ownerApp, table, columns), nil
}

func effectiveRegisteredAppDefinition(app RegisteredApp) *AppDefinition {
	if app.DraftDefinition != nil {
		return app.DraftDefinition
	}
	return app.Definition
}

func mergeRegisteredApps(apps []RegisteredApp, app RegisteredApp) []RegisteredApp {
	merged := make([]RegisteredApp, 0, len(apps)+1)
	replaced := false
	name := strings.TrimSpace(strings.ToLower(app.Name))
	namespace := strings.TrimSpace(strings.ToLower(app.Namespace))
	for _, candidate := range apps {
		if strings.EqualFold(candidate.Name, name) || (namespace != "" && strings.EqualFold(candidate.Namespace, namespace)) {
			merged = append(merged, app)
			replaced = true
			continue
		}
		merged = append(merged, candidate)
	}
	if !replaced {
		merged = append(merged, app)
	}
	return merged
}

func dependencyAppsForRegisteredApp(apps []RegisteredApp, ownerApp RegisteredApp) map[string]RegisteredApp {
	dependencyApps := make(map[string]RegisteredApp)
	definition := effectiveRegisteredAppDefinition(ownerApp)
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

func yamlScriptSyntheticID(appName, scriptName string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(appName + ":" + scriptName))
	return -1 * int64(h.Sum32()+1)
}

func SyntheticYAMLScriptID(appName, scriptName string) int64 {
	return yamlScriptSyntheticID(appName, scriptName)
}

func SyntheticYAMLMethodID(appName, serviceName, methodName string) int64 {
	return yamlScriptSyntheticID(appName, strings.TrimSpace(strings.ToLower(serviceName+"."+methodName)))
}

func yamlTableID(appName, tableName string) string {
	return yamlTableIDPrefix + appName + ":" + tableName
}

func yamlColumnID(appName, tableName, columnName string) string {
	return "yaml:column:" + appName + ":" + tableName + ":" + columnName
}

func parseYAMLTableID(input string) (appName string, tableName string, ok bool) {
	if !strings.HasPrefix(input, yamlTableIDPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(input, yamlTableIDPrefix)
	appName, tableName, ok = strings.Cut(rest, ":")
	if !ok || appName == "" || tableName == "" {
		return "", "", false
	}
	return appName, tableName, true
}

func nullableString(value string) sql.NullString {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func normalizeAppDefinition(definition *AppDefinition) error {
	definition.Name = strings.TrimSpace(strings.ToLower(definition.Name))
	definition.Namespace = strings.TrimSpace(strings.ToLower(definition.Namespace))
	definition.Label = strings.TrimSpace(definition.Label)
	definition.Description = strings.TrimSpace(definition.Description)
	if definition.Namespace != "" && len(definition.Namespace) > 8 {
		return fmt.Errorf("app namespace must be 1-8 characters")
	}

	normalizedDependencies := make([]string, 0, len(definition.Dependencies))
	seenDependencies := map[string]bool{}
	for _, dependency := range definition.Dependencies {
		dependency = strings.TrimSpace(strings.ToLower(dependency))
		if dependency == "" || seenDependencies[dependency] {
			continue
		}
		if !IsSafeIdentifier(dependency) {
			return fmt.Errorf("invalid dependency app name %q in yaml definition", dependency)
		}
		if dependency == definition.Name || dependency == definition.Namespace {
			continue
		}
		seenDependencies[dependency] = true
		normalizedDependencies = append(normalizedDependencies, dependency)
	}
	definition.Dependencies = normalizedDependencies

	normalizedRoles := make([]AppDefinitionRole, 0, len(definition.Roles))
	seenRoles := map[string]bool{}
	for _, role := range definition.Roles {
		role.Name = normalizeLooseIdentifier(role.Name)
		role.Label = strings.TrimSpace(role.Label)
		role.Description = strings.TrimSpace(role.Description)
		if role.Name == "" {
			continue
		}
		if !IsSafeIdentifier(role.Name) {
			return fmt.Errorf("invalid role name %q in yaml definition", role.Name)
		}
		if seenRoles[role.Name] {
			return fmt.Errorf("duplicate role name %q in yaml definition", role.Name)
		}
		seenRoles[role.Name] = true
		if role.Label == "" {
			role.Label = humanizeIdentifier(role.Name)
		}
		normalizedRoles = append(normalizedRoles, role)
	}
	definition.Roles = normalizedRoles

	seenTables := map[string]bool{}
	for i := range definition.Tables {
		table := &definition.Tables[i]
		table.Name = strings.TrimSpace(strings.ToLower(table.Name))
		table.Extends = strings.TrimSpace(strings.ToLower(table.Extends))
		if table.Name == "" {
			return fmt.Errorf("table name is required")
		}
		if !IsSafeIdentifier(table.Name) {
			return fmt.Errorf("invalid table name %q in yaml definition", table.Name)
		}
		if err := validateAppDefinitionTableName(definition.Name, definition.Namespace, table.Name); err != nil {
			return err
		}
		if seenTables[table.Name] {
			return fmt.Errorf("duplicate table name %q in yaml definition", table.Name)
		}
		seenTables[table.Name] = true
		table.LabelSingular = strings.TrimSpace(table.LabelSingular)
		table.LabelPlural = strings.TrimSpace(table.LabelPlural)
		table.Description = strings.TrimSpace(table.Description)
		table.DisplayField = NormalizeDisplayFieldName(table.DisplayField)
		if table.LabelSingular == "" {
			table.LabelSingular = humanizeIdentifier(table.Name)
		}
		if table.LabelPlural == "" {
			table.LabelPlural = table.LabelSingular + "s"
		}

		seenColumns := map[string]bool{}
		for j := range table.Columns {
			column := &definition.Tables[i].Columns[j]
			column.Name = strings.TrimSpace(strings.ToLower(column.Name))
			column.Label = strings.TrimSpace(column.Label)
			column.DataType = normalizeDataType(column.DataType)
			column.DefaultValue = strings.TrimSpace(column.DefaultValue)
			column.Prefix = normalizeAutoNumberPrefix(column.Prefix)
			column.ValidationRegex = strings.TrimSpace(column.ValidationRegex)
			column.ValidationExpr = strings.TrimSpace(column.ValidationExpr)
			column.ConditionExpr = strings.TrimSpace(column.ConditionExpr)
			column.ValidationMessage = strings.TrimSpace(column.ValidationMessage)
			column.ReferenceTable = strings.TrimSpace(strings.ToLower(column.ReferenceTable))
			column.Choices = normalizeChoiceOptions(column.Choices)
			if column.Name == "" {
				return fmt.Errorf("column name is required on table %q", table.Name)
			}
			if !IsSafeIdentifier(column.Name) {
				return fmt.Errorf("invalid column name %q on table %q", column.Name, table.Name)
			}
			if seenColumns[column.Name] {
				return fmt.Errorf("duplicate column name %q on table %q", column.Name, table.Name)
			}
			seenColumns[column.Name] = true
			if column.Label == "" {
				column.Label = humanizeIdentifier(column.Name)
			}
			if column.DataType == "" {
				column.DataType = "text"
			}
			if err := validateBuilderColumnDataType(column.DataType); err != nil {
				return fmt.Errorf("column %q on table %q: %w", column.Name, table.Name, err)
			}
			if IsAutoNumberDataType(column.DataType) {
				if err := validateAutoNumberPrefix(column.Prefix); err != nil {
					return fmt.Errorf("column %q on table %q: %w", column.Name, table.Name, err)
				}
			} else if column.Prefix != "" {
				return fmt.Errorf("column %q on table %q can only declare prefix with data_type autnumber", column.Name, table.Name)
			}
		}
		if table.DisplayField == "" {
			table.DisplayField = inferDefinitionDisplayField(*table)
		}
		if table.DisplayField != "" && !IsSafeIdentifier(table.DisplayField) {
			return fmt.Errorf("invalid display field %q on table %q", table.DisplayField, table.Name)
		}

		seenForms := map[string]bool{}
		for j := range table.Forms {
			form := &definition.Tables[i].Forms[j]
			form.Name = strings.TrimSpace(strings.ToLower(form.Name))
			form.Label = strings.TrimSpace(form.Label)
			form.Description = strings.TrimSpace(form.Description)
			if form.Name == "" {
				form.Name = "default"
			}
			if !IsSafeIdentifier(form.Name) {
				return fmt.Errorf("invalid form name %q on table %q", form.Name, table.Name)
			}
			if seenForms[form.Name] {
				return fmt.Errorf("duplicate form name %q on table %q", form.Name, table.Name)
			}
			seenForms[form.Name] = true
			if form.Label == "" {
				form.Label = humanizeIdentifier(form.Name)
			}
			normalizedFields := make([]string, 0, len(form.Fields))
			seenFields := map[string]bool{}
			for _, field := range form.Fields {
				field = strings.TrimSpace(strings.ToLower(field))
				if field == "" || seenFields[field] {
					continue
				}
				seenFields[field] = true
				normalizedFields = append(normalizedFields, field)
			}
			form.Fields = normalizedFields
			form.Actions = normalizeAppDefinitionActions(form.Actions)
			form.Security = normalizeAppDefinitionSecurity(form.Security)
		}

		seenLists := map[string]bool{}
		for j := range table.Lists {
			list := &definition.Tables[i].Lists[j]
			list.Name = strings.TrimSpace(strings.ToLower(list.Name))
			list.Label = strings.TrimSpace(list.Label)
			if list.Name == "" {
				list.Name = "default"
			}
			if !IsSafeIdentifier(list.Name) {
				return fmt.Errorf("invalid list name %q on table %q", list.Name, table.Name)
			}
			if seenLists[list.Name] {
				return fmt.Errorf("duplicate list name %q on table %q", list.Name, table.Name)
			}
			seenLists[list.Name] = true
			if list.Label == "" {
				list.Label = humanizeIdentifier(list.Name)
			}
			normalizedColumns := make([]string, 0, len(list.Columns))
			seenListColumns := map[string]bool{}
			for _, column := range list.Columns {
				column = strings.TrimSpace(strings.ToLower(column))
				if column == "" || seenListColumns[column] {
					continue
				}
				seenListColumns[column] = true
				normalizedColumns = append(normalizedColumns, column)
			}
			list.Columns = normalizedColumns
		}

		seenPolicies := map[string]bool{}
		for j := range table.DataPolicies {
			policy := &definition.Tables[i].DataPolicies[j]
			policy.Name = normalizeLooseIdentifier(policy.Name)
			policy.Label = strings.TrimSpace(policy.Label)
			policy.Description = strings.TrimSpace(policy.Description)
			policy.Condition = strings.TrimSpace(policy.Condition)
			policy.Action = strings.TrimSpace(policy.Action)
			if policy.Name == "" {
				return fmt.Errorf("data policy name is required on table %q", table.Name)
			}
			if !IsSafeIdentifier(policy.Name) {
				return fmt.Errorf("invalid data policy name %q on table %q", policy.Name, table.Name)
			}
			if seenPolicies[policy.Name] {
				return fmt.Errorf("duplicate data policy name %q on table %q", policy.Name, table.Name)
			}
			seenPolicies[policy.Name] = true
			if policy.Label == "" {
				policy.Label = humanizeIdentifier(policy.Name)
			}
		}

		seenTableTriggers := map[string]bool{}
		for j := range table.Triggers {
			trigger := &definition.Tables[i].Triggers[j]
			trigger.Name = normalizeLooseIdentifier(trigger.Name)
			trigger.Label = strings.TrimSpace(trigger.Label)
			trigger.Description = strings.TrimSpace(trigger.Description)
			trigger.Event = strings.TrimSpace(strings.ToLower(trigger.Event))
			trigger.Table = table.Name
			trigger.Condition = strings.TrimSpace(trigger.Condition)
			trigger.Call = strings.TrimSpace(strings.ToLower(trigger.Call))
			trigger.Mode = strings.TrimSpace(strings.ToLower(trigger.Mode))
			if trigger.Name == "" {
				return fmt.Errorf("trigger name is required on table %q", table.Name)
			}
			if !IsSafeIdentifier(trigger.Name) {
				return fmt.Errorf("invalid trigger name %q on table %q", trigger.Name, table.Name)
			}
			if seenTableTriggers[trigger.Name] {
				return fmt.Errorf("duplicate trigger name %q on table %q", trigger.Name, table.Name)
			}
			seenTableTriggers[trigger.Name] = true
			if trigger.Label == "" {
				trigger.Label = humanizeIdentifier(trigger.Name)
			}
			if trigger.Mode == "" {
				trigger.Mode = "async"
			}
		}

		seenRelatedLists := map[string]bool{}
		for j := range table.RelatedLists {
			related := &definition.Tables[i].RelatedLists[j]
			related.Name = normalizeLooseIdentifier(related.Name)
			related.Label = strings.TrimSpace(related.Label)
			related.Table = strings.TrimSpace(strings.ToLower(related.Table))
			related.ReferenceField = strings.TrimSpace(strings.ToLower(related.ReferenceField))
			if related.Name == "" {
				return fmt.Errorf("related list name is required on table %q", table.Name)
			}
			if !IsSafeIdentifier(related.Name) {
				return fmt.Errorf("invalid related list name %q on table %q", related.Name, table.Name)
			}
			if seenRelatedLists[related.Name] {
				return fmt.Errorf("duplicate related list name %q on table %q", related.Name, table.Name)
			}
			seenRelatedLists[related.Name] = true
			if related.Label == "" {
				related.Label = humanizeIdentifier(related.Name)
			}
			related.Columns = normalizeIdentifierList(related.Columns)
		}

		table.Security = normalizeAppDefinitionSecurity(table.Security)
	}

	seenForms := map[string]bool{}
	for i := range definition.Forms {
		form := &definition.Forms[i]
		form.Name = normalizeLooseIdentifier(form.Name)
		form.Table = strings.TrimSpace(strings.ToLower(form.Table))
		form.Label = strings.TrimSpace(form.Label)
		form.Description = strings.TrimSpace(form.Description)
		if form.Name == "" {
			return fmt.Errorf("form name is required")
		}
		if !IsSafeIdentifier(form.Name) {
			return fmt.Errorf("invalid form name %q in yaml definition", form.Name)
		}
		if seenForms[form.Name] {
			return fmt.Errorf("duplicate form name %q in yaml definition", form.Name)
		}
		seenForms[form.Name] = true
		if form.Label == "" {
			form.Label = humanizeIdentifier(form.Name)
		}
		form.Fields = normalizeIdentifierList(form.Fields)
		form.Layout = normalizeIdentifierList(form.Layout)
		if len(form.Layout) == 0 && len(form.Fields) > 0 {
			form.Layout = append([]string(nil), form.Fields...)
		}
		if len(form.Fields) == 0 && len(form.Layout) > 0 {
			form.Fields = append([]string(nil), form.Layout...)
		}
		form.Actions = normalizeAppDefinitionActions(form.Actions)
		form.Security = normalizeAppDefinitionSecurity(form.Security)
	}
	if err := migrateLegacyAppDefinitionFormsToTables(definition); err != nil {
		return err
	}

	seenServices := map[string]bool{}
	for i := range definition.Services {
		service := &definition.Services[i]
		service.Name = normalizeLooseIdentifier(service.Name)
		service.Label = strings.TrimSpace(service.Label)
		service.Description = strings.TrimSpace(service.Description)
		if service.Name == "" {
			return fmt.Errorf("service name is required")
		}
		if !IsSafeIdentifier(service.Name) {
			return fmt.Errorf("invalid service name %q in yaml definition", service.Name)
		}
		if seenServices[service.Name] {
			return fmt.Errorf("duplicate service name %q in yaml definition", service.Name)
		}
		seenServices[service.Name] = true
		if service.Label == "" {
			service.Label = humanizeIdentifier(service.Name)
		}

		seenMethods := map[string]bool{}
		for j := range service.Methods {
			method := &definition.Services[i].Methods[j]
			method.Name = normalizeLooseIdentifier(method.Name)
			method.Label = strings.TrimSpace(method.Label)
			method.Description = strings.TrimSpace(method.Description)
			method.Visibility = strings.TrimSpace(strings.ToLower(method.Visibility))
			method.Language = strings.TrimSpace(strings.ToLower(method.Language))
			method.Roles = normalizeIdentifierList(method.Roles)
			method.Script = strings.TrimSpace(method.Script)
			if method.Name == "" {
				return fmt.Errorf("method name is required on service %q", service.Name)
			}
			if !IsSafeIdentifier(method.Name) {
				return fmt.Errorf("invalid method name %q on service %q", method.Name, service.Name)
			}
			if seenMethods[method.Name] {
				return fmt.Errorf("duplicate method name %q on service %q", method.Name, service.Name)
			}
			seenMethods[method.Name] = true
			if method.Label == "" {
				method.Label = humanizeIdentifier(method.Name)
			}
			if method.Visibility == "" {
				method.Visibility = "private"
			}
			if method.Language == "" {
				method.Language = "javascript"
			}
		}
	}

	seenTriggers := map[string]bool{}
	for i := range definition.Triggers {
		trigger := &definition.Triggers[i]
		trigger.Name = normalizeLooseIdentifier(trigger.Name)
		trigger.Label = strings.TrimSpace(trigger.Label)
		trigger.Description = strings.TrimSpace(trigger.Description)
		trigger.Event = strings.TrimSpace(strings.ToLower(trigger.Event))
		trigger.Table = strings.TrimSpace(strings.ToLower(trigger.Table))
		trigger.Condition = strings.TrimSpace(trigger.Condition)
		trigger.Call = strings.TrimSpace(strings.ToLower(trigger.Call))
		trigger.Mode = strings.TrimSpace(strings.ToLower(trigger.Mode))
		if trigger.Name == "" {
			return fmt.Errorf("trigger name is required")
		}
		if !IsSafeIdentifier(trigger.Name) {
			return fmt.Errorf("invalid trigger name %q in yaml definition", trigger.Name)
		}
		if seenTriggers[trigger.Name] {
			return fmt.Errorf("duplicate trigger name %q in yaml definition", trigger.Name)
		}
		seenTriggers[trigger.Name] = true
		if trigger.Label == "" {
			trigger.Label = humanizeIdentifier(trigger.Name)
		}
		if trigger.Mode == "" {
			trigger.Mode = "async"
		}
	}

	seenSchedules := map[string]bool{}
	for i := range definition.Schedules {
		schedule := &definition.Schedules[i]
		schedule.Name = normalizeLooseIdentifier(schedule.Name)
		schedule.Label = strings.TrimSpace(schedule.Label)
		schedule.Description = strings.TrimSpace(schedule.Description)
		schedule.Cron = strings.TrimSpace(schedule.Cron)
		schedule.Call = strings.TrimSpace(strings.ToLower(schedule.Call))
		if schedule.Name == "" {
			return fmt.Errorf("schedule name is required")
		}
		if !IsSafeIdentifier(schedule.Name) {
			return fmt.Errorf("invalid schedule name %q in yaml definition", schedule.Name)
		}
		if seenSchedules[schedule.Name] {
			return fmt.Errorf("duplicate schedule name %q in yaml definition", schedule.Name)
		}
		seenSchedules[schedule.Name] = true
		if schedule.Label == "" {
			schedule.Label = humanizeIdentifier(schedule.Name)
		}
	}

	seenEndpoints := map[string]bool{}
	for i := range definition.Endpoints {
		endpoint := &definition.Endpoints[i]
		endpoint.Name = normalizeLooseIdentifier(endpoint.Name)
		endpoint.Label = strings.TrimSpace(endpoint.Label)
		endpoint.Description = strings.TrimSpace(endpoint.Description)
		endpoint.Method = normalizeHTTPMethod(endpoint.Method)
		endpoint.Path = normalizeEndpointPath(endpoint.Path)
		endpoint.Call = strings.TrimSpace(strings.ToLower(endpoint.Call))
		endpoint.Roles = normalizeIdentifierList(endpoint.Roles)
		if endpoint.Name == "" {
			return fmt.Errorf("endpoint name is required")
		}
		if !IsSafeIdentifier(endpoint.Name) {
			return fmt.Errorf("invalid endpoint name %q in yaml definition", endpoint.Name)
		}
		if seenEndpoints[endpoint.Name] {
			return fmt.Errorf("duplicate endpoint name %q in yaml definition", endpoint.Name)
		}
		seenEndpoints[endpoint.Name] = true
		if endpoint.Label == "" {
			endpoint.Label = humanizeIdentifier(endpoint.Name)
		}
	}

	seenClientScripts := map[string]bool{}
	for i := range definition.ClientScripts {
		script := &definition.ClientScripts[i]
		script.Name = normalizeLooseIdentifier(script.Name)
		script.Label = strings.TrimSpace(script.Label)
		script.Description = strings.TrimSpace(script.Description)
		script.Table = strings.TrimSpace(strings.ToLower(script.Table))
		script.Event = strings.TrimSpace(strings.ToLower(script.Event))
		script.Field = strings.TrimSpace(strings.ToLower(script.Field))
		script.Language = strings.TrimSpace(strings.ToLower(script.Language))
		script.Script = strings.TrimSpace(script.Script)
		if script.Name == "" {
			return fmt.Errorf("client script name is required")
		}
		if !IsSafeIdentifier(script.Name) {
			return fmt.Errorf("invalid client script name %q in yaml definition", script.Name)
		}
		if seenClientScripts[script.Name] {
			return fmt.Errorf("duplicate client script name %q in yaml definition", script.Name)
		}
		seenClientScripts[script.Name] = true
		if script.Label == "" {
			script.Label = humanizeIdentifier(script.Name)
		}
		if script.Language == "" {
			script.Language = "javascript"
		}
	}

	for i := range definition.Pages {
		page := &definition.Pages[i]
		page.Name = strings.TrimSpace(page.Name)
		page.Slug = strings.TrimSpace(strings.ToLower(page.Slug))
		page.Label = strings.TrimSpace(page.Label)
		page.Description = strings.TrimSpace(page.Description)
		page.SearchKeywords = strings.TrimSpace(page.SearchKeywords)
		page.EditorMode = strings.TrimSpace(strings.ToLower(page.EditorMode))
		page.Status = strings.TrimSpace(strings.ToLower(page.Status))
		page.Actions = normalizeAppDefinitionActions(page.Actions)
		page.Security = normalizeAppDefinitionSecurity(page.Security)
		if page.Name == "" {
			page.Name = page.Label
		}
		if page.Label == "" {
			page.Label = page.Name
		}
		if page.EditorMode == "" {
			page.EditorMode = "wysiwyg"
		}
		if page.Status == "" {
			page.Status = "draft"
		}
	}

	for i := range definition.Seeds {
		definition.Seeds[i].Table = strings.TrimSpace(strings.ToLower(definition.Seeds[i].Table))
	}

	seenDocs := map[string]bool{}
	for i := range definition.Documentation {
		article := &definition.Documentation[i]
		article.Name = normalizeLooseIdentifier(article.Name)
		article.Label = strings.TrimSpace(article.Label)
		article.Description = strings.TrimSpace(article.Description)
		article.Category = strings.TrimSpace(article.Category)
		article.Visibility = strings.TrimSpace(strings.ToLower(article.Visibility))
		article.Content = strings.TrimSpace(article.Content)
		article.Related = normalizeIdentifierList(article.Related)
		if article.Name == "" {
			return fmt.Errorf("documentation article name is required")
		}
		if !IsSafeIdentifier(article.Name) {
			return fmt.Errorf("invalid documentation article name %q in yaml definition", article.Name)
		}
		if seenDocs[article.Name] {
			return fmt.Errorf("duplicate documentation article name %q in yaml definition", article.Name)
		}
		seenDocs[article.Name] = true
		if article.Label == "" {
			article.Label = humanizeIdentifier(article.Name)
		}
		if article.Visibility == "" {
			article.Visibility = "internal"
		}
	}

	return nil
}

func validateAppDefinitionForApp(ctx context.Context, app RegisteredApp, definition *AppDefinition) error {
	if definition == nil {
		return fmt.Errorf("definition is required")
	}
	if definition.Name == "" {
		return fmt.Errorf("app name is required")
	}
	if definition.Namespace != "" && len(definition.Namespace) > 8 {
		return fmt.Errorf("app namespace must be 1-8 characters")
	}
	if !IsSafeIdentifier(definition.Name) {
		return fmt.Errorf("invalid app name %q", definition.Name)
	}
	if definition.Namespace != "" && !IsSafeIdentifier(definition.Namespace) {
		return fmt.Errorf("invalid app namespace %q", definition.Namespace)
	}

	apps, err := ListActiveApps(ctx)
	if err != nil {
		return err
	}

	dependencyApps := map[string]RegisteredApp{}
	for _, dependency := range definition.Dependencies {
		found := false
		for _, candidate := range apps {
			if candidate.Name == dependency || candidate.Namespace == dependency {
				dependencyApps[candidate.Name] = candidate
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("dependency %q not found", dependency)
		}
	}

	tableByName := map[string]AppDefinitionTable{}
	for _, table := range definition.Tables {
		if err := validateAppDefinitionTableName(definition.Name, definition.Namespace, table.Name); err != nil {
			return err
		}
		tableByName[table.Name] = table
	}
	roleSet := map[string]bool{}
	for _, role := range definition.Roles {
		roleSet[role.Name] = true
	}

	for _, table := range definition.Tables {
		allowedFields := map[string]bool{}
		if definitionUsesImplicitSystemColumns(definition.Name, definition.Namespace, table) {
			allowedFields = recordSystemColumnNameSet()
		}
		for _, column := range table.Columns {
			allowedFields[column.Name] = true
			if err := validateDefinitionColumn(app, definition, dependencyApps, table, column); err != nil {
				return err
			}
		}

		if table.Extends != "" {
			parentApp, parentTable, ok := resolveValidationTable(app, definition, dependencyApps, table.Extends)
			if !ok {
				return fmt.Errorf("table %q extends unknown table %q", table.Name, table.Extends)
			}
			if parentApp.Name != app.Name && !parentTable.Extensible {
				return fmt.Errorf("table %q cannot extend %q because it is not marked extensible", table.Name, table.Extends)
			}
			inheritedColumns, err := collectInheritedColumnNames(app, definition, dependencyApps, parentApp, parentTable, map[string]bool{})
			if err != nil {
				return err
			}
			for inheritedName := range inheritedColumns {
				if IsSystemColumnName(inheritedName) && definitionUsesImplicitSystemColumns(definition.Name, definition.Namespace, table) {
					continue
				}
				if allowedFields[inheritedName] {
					return fmt.Errorf("table %q cannot redefine inherited column %q", table.Name, inheritedName)
				}
				allowedFields[inheritedName] = true
			}
		}
		if table.DisplayField != "" && !allowedFields[table.DisplayField] {
			return fmt.Errorf("table %q display_field %q does not exist", table.Name, table.DisplayField)
		}

		for _, form := range table.Forms {
			for _, field := range form.Fields {
				if !allowedFields[field] {
					return fmt.Errorf("form %q on table %q references unknown field %q", form.Name, table.Name, field)
				}
			}
		}
		for _, list := range table.Lists {
			for _, column := range list.Columns {
				if !allowedFields[column] {
					return fmt.Errorf("list %q on table %q references unknown column %q", list.Name, table.Name, column)
				}
			}
		}
		for _, related := range table.RelatedLists {
			if _, ok := tableByName[related.Table]; !ok {
				if _, _, ok := resolveValidationTable(app, definition, dependencyApps, related.Table); !ok {
					return fmt.Errorf("related list %q on table %q references unknown table %q", related.Name, table.Name, related.Table)
				}
			}
			if related.ReferenceField == "" {
				return fmt.Errorf("related list %q on table %q must declare reference_field", related.Name, table.Name)
			}
		}
		for _, trigger := range table.Triggers {
			if trigger.Event == "" {
				return fmt.Errorf("trigger %q on table %q must declare an event", trigger.Name, table.Name)
			}
			if trigger.Mode != "sync" && trigger.Mode != "async" {
				return fmt.Errorf("trigger %q on table %q mode must be sync or async", trigger.Name, table.Name)
			}
			if strings.TrimSpace(trigger.Call) == "" {
				return fmt.Errorf("trigger %q on table %q must declare a script", trigger.Name, table.Name)
			}
		}
		if err := validateAppDefinitionSecurity(roleSet, table.Security, fmt.Sprintf("security for table %q", table.Name), table.Name, allowedFields); err != nil {
			return err
		}
	}

	for _, form := range definition.Forms {
		table, ok := tableByName[form.Table]
		if !ok {
			return fmt.Errorf("form %q references unknown table %q", form.Name, form.Table)
		}
		allowedFields := map[string]bool{}
		if definitionUsesImplicitSystemColumns(definition.Name, definition.Namespace, table) {
			allowedFields = recordSystemColumnNameSet()
		}
		for _, column := range table.Columns {
			allowedFields[column.Name] = true
		}
		for _, field := range form.Layout {
			if !allowedFields[field] {
				return fmt.Errorf("form %q references unknown field %q on table %q", form.Name, field, form.Table)
			}
		}
		if err := validateAppDefinitionActionRoles(roleSet, form.Actions, fmt.Sprintf("actions for form %q", form.Name)); err != nil {
			return err
		}
		if err := validateAppDefinitionSecurity(roleSet, form.Security, fmt.Sprintf("security for form %q", form.Name), form.Table, allowedFields); err != nil {
			return err
		}
	}

	serviceIndex := map[string]map[string]AppDefinitionMethod{}
	for _, service := range definition.Services {
		methods := map[string]AppDefinitionMethod{}
		for _, method := range service.Methods {
			if method.Visibility != "private" && method.Visibility != "public" {
				return fmt.Errorf("method %q on service %q must be public or private", method.Name, service.Name)
			}
			if err := validateIdentifierRoles(roleSet, method.Roles, fmt.Sprintf("roles for method %q on service %q", method.Name, service.Name)); err != nil {
				return err
			}
			methods[method.Name] = method
		}
		serviceIndex[service.Name] = methods
	}

	for _, trigger := range definition.Triggers {
		if trigger.Event == "" {
			return fmt.Errorf("trigger %q must declare an event", trigger.Name)
		}
		if trigger.Mode != "sync" && trigger.Mode != "async" {
			return fmt.Errorf("trigger %q mode must be sync or async", trigger.Name)
		}
		if _, ok := tableByName[trigger.Table]; !ok {
			return fmt.Errorf("trigger %q references unknown table %q", trigger.Name, trigger.Table)
		}
		if err := validateMethodCall(serviceIndex, dependencyApps, trigger.Call); err != nil {
			return fmt.Errorf("trigger %q: %w", trigger.Name, err)
		}
	}

	for _, schedule := range definition.Schedules {
		if strings.TrimSpace(schedule.Cron) == "" {
			return fmt.Errorf("schedule %q must declare a cron expression", schedule.Name)
		}
		if err := validateMethodCall(serviceIndex, dependencyApps, schedule.Call); err != nil {
			return fmt.Errorf("schedule %q: %w", schedule.Name, err)
		}
	}

	for _, endpoint := range definition.Endpoints {
		if !endpoint.Enabled {
			continue
		}
		if endpoint.Method == "" {
			return fmt.Errorf("endpoint %q must declare a method", endpoint.Name)
		}
		if endpoint.Path == "" {
			return fmt.Errorf("endpoint %q must declare a path", endpoint.Name)
		}
		if err := validateMethodCall(serviceIndex, dependencyApps, endpoint.Call); err != nil {
			return fmt.Errorf("endpoint %q: %w", endpoint.Name, err)
		}
		if err := validateIdentifierRoles(roleSet, endpoint.Roles, fmt.Sprintf("roles for endpoint %q", endpoint.Name)); err != nil {
			return err
		}
	}

	for _, script := range definition.ClientScripts {
		if !script.Enabled {
			continue
		}
		if script.Table == "" {
			return fmt.Errorf("client script %q must declare a table", script.Name)
		}
		if _, ok := tableByName[script.Table]; !ok {
			return fmt.Errorf("client script %q references unknown table %q", script.Name, script.Table)
		}
		if script.Event == "" {
			return fmt.Errorf("client script %q must declare an event", script.Name)
		}
		switch script.Event {
		case "form.load", "field.change", "field.input":
		default:
			return fmt.Errorf("client script %q event must be form.load, field.change, or field.input", script.Name)
		}
		if (script.Event == "field.change" || script.Event == "field.input") && script.Field == "" {
			return fmt.Errorf("client script %q must declare a field for %s", script.Name, script.Event)
		}
		if script.Field != "" {
			table := tableByName[script.Table]
			allowedFields := map[string]bool{}
			if definitionUsesImplicitSystemColumns(definition.Name, definition.Namespace, table) {
				allowedFields = recordSystemColumnNameSet()
			}
			for _, column := range table.Columns {
				allowedFields[column.Name] = true
			}
			if !allowedFields[script.Field] {
				return fmt.Errorf("client script %q references unknown field %q on table %q", script.Name, script.Field, script.Table)
			}
		}
		if script.Language != "javascript" {
			return fmt.Errorf("client script %q must use javascript", script.Name)
		}
		if strings.TrimSpace(script.Script) == "" {
			return fmt.Errorf("client script %q must declare script", script.Name)
		}
	}

	for _, page := range definition.Pages {
		if err := validateAppDefinitionActionRoles(roleSet, page.Actions, fmt.Sprintf("actions for page %q", page.Slug)); err != nil {
			return err
		}
		if err := validateAppDefinitionSecurity(roleSet, page.Security, fmt.Sprintf("security for page %q", page.Slug), "", nil); err != nil {
			return err
		}
	}

	for _, article := range definition.Documentation {
		switch article.Visibility {
		case "internal", "external", "role-gated":
		default:
			return fmt.Errorf("documentation article %q has invalid visibility %q", article.Name, article.Visibility)
		}
	}

	return nil
}

func migrateLegacyAppDefinitionFormsToTables(definition *AppDefinition) error {
	if definition == nil || len(definition.Forms) == 0 {
		return nil
	}

	tableIndexByName := make(map[string]int, len(definition.Tables))
	for i, table := range definition.Tables {
		tableIndexByName[table.Name] = i
	}

	for _, assetForm := range definition.Forms {
		tableName := strings.TrimSpace(strings.ToLower(assetForm.Table))
		if tableName == "" {
			return fmt.Errorf("form %q must declare table", assetForm.Name)
		}
		tableIndex, ok := tableIndexByName[tableName]
		if !ok {
			return fmt.Errorf("form %q references unknown table %q", assetForm.Name, assetForm.Table)
		}

		fields := normalizeIdentifierList(assetForm.Fields)
		if len(fields) == 0 {
			fields = normalizeIdentifierList(assetForm.Layout)
		}

		formName := strings.TrimSpace(strings.ToLower(assetForm.Name))
		if formName == "" {
			formName = "default"
		}
		for _, existing := range definition.Tables[tableIndex].Forms {
			if strings.EqualFold(strings.TrimSpace(existing.Name), formName) {
				return fmt.Errorf("duplicate form name %q on table %q", formName, tableName)
			}
		}

		definition.Tables[tableIndex].Forms = append(definition.Tables[tableIndex].Forms, AppDefinitionForm{
			Name:        formName,
			Label:       strings.TrimSpace(assetForm.Label),
			Description: strings.TrimSpace(assetForm.Description),
			Fields:      fields,
			Actions:     normalizeAppDefinitionActions(assetForm.Actions),
			Security:    normalizeAppDefinitionSecurity(assetForm.Security),
		})
	}

	definition.Forms = nil
	return nil
}

func normalizeIdentifierList(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(strings.ToLower(item))
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func normalizeLooseIdentifier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return strings.Trim(value, "_")
}

func normalizeAppDefinitionActions(actions []AppDefinitionAction) []AppDefinitionAction {
	normalized := make([]AppDefinitionAction, 0, len(actions))
	seen := map[string]bool{}
	for _, action := range actions {
		action.Name = normalizeLooseIdentifier(action.Name)
		action.Label = strings.TrimSpace(action.Label)
		action.Call = strings.TrimSpace(action.Call)
		action.Roles = normalizeIdentifierList(action.Roles)
		if action.Name == "" {
			continue
		}
		if seen[action.Name] {
			continue
		}
		seen[action.Name] = true
		if action.Label == "" {
			action.Label = humanizeIdentifier(action.Name)
		}
		normalized = append(normalized, action)
	}
	return normalized
}

func normalizeSecurityOperation(operation string) string {
	operation = strings.TrimSpace(strings.ToUpper(operation))
	switch operation {
	case "C", "CREATE", "INSERT":
		return "C"
	case "R", "READ", "SELECT", "VIEW":
		return "R"
	case "U", "UPDATE", "WRITE", "EDIT":
		return "U"
	case "D", "DELETE", "REMOVE":
		return "D"
	default:
		return operation
	}
}

func normalizeSecurityEffect(effect string) string {
	effect = strings.TrimSpace(strings.ToLower(effect))
	switch effect {
	case "", "allow", "allowed", "permit", "permitted", "grant", "granted":
		if effect == "" {
			return ""
		}
		return "allow"
	case "deny", "denied", "block", "blocked", "forbid", "forbidden":
		return "deny"
	default:
		return effect
	}
}

func normalizeAppDefinitionSecurityRules(rules []AppDefinitionSecurityRule) []AppDefinitionSecurityRule {
	normalized := make([]AppDefinitionSecurityRule, 0, len(rules))
	seen := map[string]bool{}
	for _, rule := range rules {
		rule.Name = normalizeLooseIdentifier(rule.Name)
		rule.Description = strings.TrimSpace(rule.Description)
		rule.Effect = normalizeSecurityEffect(rule.Effect)
		rule.Operation = normalizeSecurityOperation(rule.Operation)
		rule.Table = strings.TrimSpace(strings.ToLower(rule.Table))
		rule.Field = strings.TrimSpace(strings.ToLower(rule.Field))
		rule.Condition = strings.TrimSpace(rule.Condition)
		rule.Role = strings.TrimSpace(strings.ToLower(rule.Role))
		if rule.Name != "" && seen[rule.Name] {
			continue
		}
		if rule.Name != "" {
			seen[rule.Name] = true
		}
		normalized = append(normalized, rule)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].Order == normalized[j].Order {
			return normalized[i].Name < normalized[j].Name
		}
		return normalized[i].Order < normalized[j].Order
	})
	return normalized
}

func normalizeAppDefinitionSecurity(security AppDefinitionSecurity) AppDefinitionSecurity {
	security.Roles = normalizeIdentifierList(security.Roles)
	security.Notes = strings.TrimSpace(security.Notes)
	security.Rules = normalizeAppDefinitionSecurityRules(security.Rules)
	return security
}

func scriptTextValue(code, legacy string) string {
	code = strings.TrimSpace(code)
	legacy = strings.TrimSpace(legacy)
	if code != "" {
		return code
	}
	return legacy
}

func normalizeHTTPMethod(method string) string {
	method = strings.TrimSpace(strings.ToUpper(method))
	switch method {
	case "", "GET", "POST", "PUT", "DELETE", "PATCH":
		return method
	default:
		return method
	}
}

func normalizeEndpointPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}

func validateIdentifierRoles(roleSet map[string]bool, roles []string, context string) error {
	for _, role := range roles {
		if !roleSet[role] {
			return fmt.Errorf("%s references unknown role %q", context, role)
		}
	}
	return nil
}

func validateAppDefinitionSecurity(roleSet map[string]bool, security AppDefinitionSecurity, context string, expectedTable string, allowedFields map[string]bool) error {
	if err := validateIdentifierRoles(roleSet, security.Roles, context); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, rule := range security.Rules {
		if rule.Name == "" {
			return fmt.Errorf("%s contains a security rule without a name", context)
		}
		if seen[rule.Name] {
			return fmt.Errorf("%s contains duplicate security rule %q", context, rule.Name)
		}
		seen[rule.Name] = true
		if rule.Role == "" {
			return fmt.Errorf("%s security rule %q must declare a role", context, rule.Name)
		}
		if !roleSet[rule.Role] {
			return fmt.Errorf("%s security rule %q references unknown role %q", context, rule.Name, rule.Role)
		}
		switch rule.Operation {
		case "C", "R", "U", "D":
		default:
			return fmt.Errorf("%s security rule %q must use operation C, R, U, or D", context, rule.Name)
		}
		switch rule.Effect {
		case "", "allow", "deny":
		default:
			return fmt.Errorf("%s security rule %q must use effect allow or deny", context, rule.Name)
		}
		if expectedTable != "" {
			if rule.Table != "" && rule.Table != expectedTable {
				return fmt.Errorf("%s security rule %q references table %q; expected %q", context, rule.Name, rule.Table, expectedTable)
			}
		}
		if rule.Operation == "D" && rule.Field != "" {
			return fmt.Errorf("%s security rule %q cannot target a field for delete operations", context, rule.Name)
		}
		if allowedFields != nil && rule.Field != "" && !allowedFields[rule.Field] {
			return fmt.Errorf("%s security rule %q references unknown field %q", context, rule.Name, rule.Field)
		}
		if rule.Condition != "" {
			if err := validateBooleanExpressionSyntax(rule.Condition); err != nil {
				return fmt.Errorf("%s security rule %q has invalid condition: %w", context, rule.Name, err)
			}
		}
	}
	return nil
}

func validateAppDefinitionActionRoles(roleSet map[string]bool, actions []AppDefinitionAction, context string) error {
	for _, action := range actions {
		if err := validateIdentifierRoles(roleSet, action.Roles, fmt.Sprintf("%s action %q", context, action.Name)); err != nil {
			return err
		}
	}
	return nil
}

func validateDefinitionColumn(currentApp RegisteredApp, currentDefinition *AppDefinition, dependencyApps map[string]RegisteredApp, table AppDefinitionTable, column AppDefinitionColumn) error {
	switch normalizeDataType(column.DataType) {
	case "reference":
		if column.ReferenceTable == "" {
			return fmt.Errorf("column %q on table %q must declare reference_table", column.Name, table.Name)
		}
		if _, _, ok := resolveValidationTable(currentApp, currentDefinition, dependencyApps, column.ReferenceTable); !ok {
			return fmt.Errorf("column %q on table %q references unknown table %q", column.Name, table.Name, column.ReferenceTable)
		}
		if len(column.Choices) > 0 {
			return fmt.Errorf("column %q on table %q cannot declare choices for a reference field", column.Name, table.Name)
		}
	case "choice":
		if len(column.Choices) == 0 {
			return fmt.Errorf("column %q on table %q must declare choices", column.Name, table.Name)
		}
		if column.ReferenceTable != "" {
			return fmt.Errorf("column %q on table %q cannot declare reference_table for a choice field", column.Name, table.Name)
		}
	case "autnumber":
		if err := validateAutoNumberPrefix(column.Prefix); err != nil {
			return fmt.Errorf("column %q on table %q: %w", column.Name, table.Name, err)
		}
		if column.ReferenceTable != "" {
			return fmt.Errorf("column %q on table %q cannot declare reference_table for an autnumber field", column.Name, table.Name)
		}
		if len(column.Choices) > 0 {
			return fmt.Errorf("column %q on table %q cannot declare choices for an autnumber field", column.Name, table.Name)
		}
		if strings.TrimSpace(column.DefaultValue) != "" {
			return fmt.Errorf("column %q on table %q cannot declare default_value for an autnumber field", column.Name, table.Name)
		}
	default:
		if column.ReferenceTable != "" {
			return fmt.Errorf("column %q on table %q can only use reference_table with data_type reference", column.Name, table.Name)
		}
		if len(column.Choices) > 0 && !strings.HasPrefix(normalizeDataType(column.DataType), "enum:") {
			return fmt.Errorf("column %q on table %q can only declare choices with data_type choice", column.Name, table.Name)
		}
		if column.Prefix != "" {
			return fmt.Errorf("column %q on table %q can only declare prefix with data_type autnumber", column.Name, table.Name)
		}
	}
	if strings.HasPrefix(normalizeDataType(column.DataType), "enum:") && len(column.Choices) > 0 {
		return fmt.Errorf("column %q on table %q must use either enum values or choices, not both", column.Name, table.Name)
	}
	if strings.TrimSpace(column.DefaultValue) != "" {
		testColumn := Column{
			NAME:      column.Name,
			DATA_TYPE: column.DataType,
			CHOICES:   append([]ChoiceOption(nil), column.Choices...),
		}
		if err := validateColumnValue(testColumn, column.DefaultValue); err != nil {
			return fmt.Errorf("column %q on table %q has invalid default value: %w", column.Name, table.Name, err)
		}
	}
	return nil
}

func normalizeChoiceOptions(options []ChoiceOption) []ChoiceOption {
	normalized := make([]ChoiceOption, 0, len(options))
	seen := map[string]bool{}
	for _, option := range options {
		value := strings.TrimSpace(option.Value)
		label := strings.TrimSpace(option.Label)
		if value == "" || seen[value] {
			continue
		}
		if label == "" {
			label = humanizeIdentifier(strings.ToLower(value))
		}
		seen[value] = true
		normalized = append(normalized, ChoiceOption{
			Value: value,
			Label: label,
		})
	}
	return normalized
}

func resolveValidationTable(currentApp RegisteredApp, currentDefinition *AppDefinition, dependencyApps map[string]RegisteredApp, tableName string) (RegisteredApp, AppDefinitionTable, bool) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if currentDefinition == nil {
		currentDefinition = effectiveRegisteredAppDefinition(currentApp)
	}
	if currentDefinition == nil {
		return RegisteredApp{}, AppDefinitionTable{}, false
	}
	for _, table := range currentDefinition.Tables {
		if table.Name == tableName {
			return currentApp, table, true
		}
	}
	for _, dependency := range dependencyApps {
		definition := effectiveRegisteredAppDefinition(dependency)
		if definition == nil {
			continue
		}
		for _, table := range definition.Tables {
			if table.Name == tableName {
				return dependency, table, true
			}
		}
	}
	return RegisteredApp{}, AppDefinitionTable{}, false
}

func collectInheritedColumnNames(currentApp RegisteredApp, currentDefinition *AppDefinition, dependencyApps map[string]RegisteredApp, ownerApp RegisteredApp, table AppDefinitionTable, visited map[string]bool) (map[string]bool, error) {
	if visited[table.Name] {
		return nil, fmt.Errorf("cyclic table inheritance detected at %q", table.Name)
	}
	visited[table.Name] = true
	names := map[string]bool{}
	if definitionUsesImplicitSystemColumns(ownerApp.Name, ownerApp.Namespace, table) {
		names = recordSystemColumnNameSet()
	}
	for _, column := range table.Columns {
		names[column.Name] = true
	}
	if table.Extends == "" {
		return names, nil
	}
	parentApp, parent, ok := resolveValidationTable(currentApp, currentDefinition, dependencyApps, table.Extends)
	if !ok {
		return nil, fmt.Errorf("extended table %q not found", table.Extends)
	}
	parentColumns, err := collectInheritedColumnNames(currentApp, currentDefinition, dependencyApps, parentApp, parent, visited)
	if err != nil {
		return nil, err
	}
	for name := range parentColumns {
		names[name] = true
	}
	return names, nil
}

func validateMethodCall(localServices map[string]map[string]AppDefinitionMethod, dependencyApps map[string]RegisteredApp, call string) error {
	call = strings.TrimSpace(strings.ToLower(call))
	if call == "" {
		return fmt.Errorf("method call is required")
	}
	serviceName, methodName, ok := strings.Cut(call, ".")
	if !ok || serviceName == "" || methodName == "" {
		return fmt.Errorf("method call must use service.method syntax")
	}
	if methods, ok := localServices[serviceName]; ok {
		if _, ok := methods[methodName]; ok {
			return nil
		}
	}
	for _, dependency := range dependencyApps {
		if dependency.Definition == nil {
			continue
		}
		for _, service := range dependency.Definition.Services {
			if service.Name != serviceName {
				continue
			}
			for _, method := range service.Methods {
				if method.Name == methodName && method.Visibility == "public" {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("method %q is not available in this app or its dependencies", call)
}

func ensureDefinitionTable(ctx context.Context, definition *AppDefinition, tableName string) (*AppDefinitionTable, int, error) {
	for i := range definition.Tables {
		if definition.Tables[i].Name == tableName {
			return &definition.Tables[i], i, nil
		}
	}

	summary, err := GetBuilderTable(ctx, tableName)
	if err != nil {
		return nil, -1, err
	}
	columns, err := ListBuilderColumns(ctx, tableName)
	if err != nil {
		return nil, -1, err
	}

	table := AppDefinitionTable{
		Name:          summary.Name,
		LabelSingular: summary.LabelSingular,
		LabelPlural:   summary.LabelPlural,
		Description:   summary.Description,
		Columns:       make([]AppDefinitionColumn, 0, len(columns)),
	}
	for _, column := range columns {
		if IsSystemColumnName(column.Name) {
			continue
		}
		table.Columns = append(table.Columns, AppDefinitionColumn{
			Name:            column.Name,
			Label:           column.Label,
			DataType:        column.DataType,
			IsNullable:      column.IsNullable,
			DefaultValue:    column.DefaultValue,
			ValidationRegex: column.ValidationRule,
		})
	}

	definition.Tables = append(definition.Tables, table)
	index := len(definition.Tables) - 1
	return &definition.Tables[index], index, nil
}

func validateAppDefinitionTableName(appName, namespace, tableName string) error {
	appName = strings.TrimSpace(strings.ToLower(appName))
	namespace = strings.TrimSpace(strings.ToLower(namespace))
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		return nil
	}
	if namespace == "" {
		if isOOTBBaseAppName(appName, namespace) {
			if strings.HasPrefix(tableName, "_") {
				if strings.TrimSpace(strings.TrimPrefix(tableName, "_")) == "" {
					return fmt.Errorf("table %q must include a name after the '_' prefix", tableName)
				}
				return nil
			}
			if strings.HasPrefix(tableName, "base_") {
				if strings.TrimSpace(strings.TrimPrefix(tableName, "base_")) == "" {
					return fmt.Errorf("table %q must include a name after the %q_ prefix", tableName, "base")
				}
				return nil
			}
			return fmt.Errorf("table %q must begin with '_' or %q_ for the OOTB base app", tableName, "base")
		}
		if !strings.HasPrefix(tableName, "_") {
			return fmt.Errorf("table %q must begin with '_' for the system app", tableName)
		}
		if strings.TrimSpace(strings.TrimPrefix(tableName, "_")) == "" {
			return fmt.Errorf("table %q must include a name after the '_' prefix", tableName)
		}
		return nil
	}
	expectedPrefix := namespace + "_"
	if !strings.HasPrefix(tableName, expectedPrefix) {
		return fmt.Errorf("table %q must use the %q_ prefix", tableName, namespace)
	}
	if strings.TrimSpace(strings.TrimPrefix(tableName, expectedPrefix)) == "" {
		return fmt.Errorf("table %q must include a name after the %q_ prefix", tableName, namespace)
	}
	return nil
}

func inferDefinitionDisplayField(table AppDefinitionTable) string {
	names := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		names = append(names, column.Name)
	}
	return InferDisplayFieldName(names)
}

func resolveDefinitionDisplayField(table AppDefinitionTable) string {
	if field := NormalizeDisplayFieldName(table.DisplayField); field != "" {
		return field
	}
	return inferDefinitionDisplayField(table)
}
