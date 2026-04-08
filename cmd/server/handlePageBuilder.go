package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"velm/internal/auth"
	"velm/internal/db"

	"gopkg.in/yaml.v3"
)

type pageBuilderItem struct {
	Name       string
	Slug       string
	EditorMode string
	Content    string
	Status     string
}

type pageVersionItem struct {
	VersionNum int
	Status     string
	EditorMode string
	CreatedBy  string
	CreatedAt  string
}

type formHiddenField struct {
	Name  string
	Value string
}

type appEditorAppSummary struct {
	ID                  string
	Name                string
	Label               string
	Description         string
	ObjectCount         int
	DefinitionVersion   int64
	PublishedVersion    int64
	HasUnpublishedDraft bool
}

type appEditorFormProperty struct {
	Name          string
	Label         string
	Value         string
	DataType      string
	ReadOnly      bool
	ConditionExpr string
	Choices       []db.ChoiceOption
}

type appEditorObject struct {
	ID             string
	ParentID       string
	EditorTable    string
	EditorRecordID string
	Kind           string
	Name           string
	PhysicalName   string
	Label          string
	LabelSingular  string
	LabelPlural    string
	Description    string
	Href           string
	Meta           string
	ColumnCount    int
	Columns        []appEditorColumn
	SecurityRules  []appEditorSecurityRule
	ColumnDetail   appEditorColumn
	SecurityDetail appEditorSecurityRule
	FormProperties []appEditorFormProperty
	Editable       bool
	Published      bool
	Deletable      bool
	System         bool
}

type appEditorExplorerTable struct {
	Table         appEditorObject
	Forms         []appEditorFormGroup
	ClientScripts []appEditorClientScriptGroup
	Columns       []appEditorObject
	SystemColumns []appEditorObject
	DataPolicies  []appEditorObject
	TriggerItems  []appEditorObject
	RelatedLists  []appEditorObject
	SecurityItems []appEditorObject
}

type appEditorServiceGroup struct {
	Service appEditorObject
	Methods []appEditorObject
}

type appEditorFormGroup struct {
	Table    string
	Form     appEditorObject
	Layout   appEditorObject
	Actions  appEditorObject
	Security appEditorObject
}

type appEditorPageGroup struct {
	Page     appEditorObject
	Layout   appEditorObject
	Actions  appEditorObject
	Security appEditorObject
}

type appEditorClientScriptGroup struct {
	Script appEditorObject
	Code   appEditorObject
}

type appEditorDocumentationGroup struct {
	Article    appEditorObject
	Content    appEditorObject
	Category   appEditorObject
	Visibility appEditorObject
	Related    appEditorObject
}

type appEditorFormBinding struct {
	Source string
	Table  string
	Form   db.AppDefinitionAssetForm
}

type appEditorColumn struct {
	ID             string
	Name           string
	Label          string
	DataType       string
	IsNullable     bool
	DefaultValue   string
	Prefix         string
	ValidationRule string
}

type appEditorSecurityRule struct {
	Name        string
	Description string
	Status      string
}

type appRegistryItem struct {
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
	Definition              *db.AppDefinition
	DraftDefinition         *db.AppDefinition
}

type appEditorSelection struct {
	Apps              []appEditorAppSummary
	SelectedApp       appEditorAppSummary
	DefinitionObjects []appEditorObject
	ExplorerTables    []appEditorExplorerTable
	Dependencies      []appEditorObject
	Forms             []appEditorFormGroup
	Roles             []appEditorObject
	ClientScripts     []appEditorClientScriptGroup
	Services          []appEditorServiceGroup
	Endpoints         []appEditorObject
	Triggers          []appEditorObject
	Schedules         []appEditorObject
	Pages             []appEditorPageGroup
	Seeds             []appEditorObject
	Documentation     []appEditorDocumentationGroup
	Objects           []appEditorObject
}

var pageSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,63}$`)

func handlePageBuilder(w http.ResponseWriter, r *http.Request) {
	selection, err := listAppEditorData(r.Context(), strings.TrimSpace(strings.ToLower(r.URL.Query().Get("app"))))
	if err != nil {
		http.Error(w, "Failed to load app editor", http.StatusInternalServerError)
		return
	}
	initialOpenID := strings.TrimSpace(r.URL.Query().Get("active"))
	if initialOpenID == "" {
		initialOpenID = strings.TrimSpace(r.URL.Query().Get("open"))
	}
	if newObject, ok := buildNewAppEditorObject(r, selection.SelectedApp, selection.ExplorerTables, initialOpenID); ok {
		selection.Objects = append(selection.Objects, newObject)
	}
	if _, ok := findAppEditorObject(selection.Objects, initialOpenID); !ok {
		initialOpenID = ""
	}

	data := newViewData(w, r, r.URL.Path, "Application Editor", "Admin")
	data["View"] = "page-builder"
	data["AppEditorApps"] = selection.Apps
	data["AppEditorSelectedApp"] = selection.SelectedApp
	data["AppEditorDefinitionObjects"] = selection.DefinitionObjects
	data["AppEditorExplorerTables"] = selection.ExplorerTables
	data["AppEditorDependencies"] = selection.Dependencies
	data["AppEditorForms"] = selection.Forms
	data["AppEditorRoles"] = selection.Roles
	data["AppEditorClientScripts"] = selection.ClientScripts
	data["AppEditorServices"] = selection.Services
	data["AppEditorEndpoints"] = selection.Endpoints
	data["AppEditorTriggers"] = selection.Triggers
	data["AppEditorSchedules"] = selection.Schedules
	data["AppEditorPages"] = selection.Pages
	data["AppEditorSeeds"] = selection.Seeds
	data["AppEditorDocumentation"] = selection.Documentation
	data["AppEditorObjects"] = selection.Objects
	data["AppEditorInitialOpenID"] = initialOpenID
	if activeObject, ok := findAppEditorObject(selection.Objects, initialOpenID); ok {
		data["AppEditorActiveObject"] = activeObject
		if activeObject.EditorTable != "" && activeObject.EditorRecordID != "" {
			if formData, err := buildFormViewData(w, r, r.URL.Path, "Form: "+activeObject.EditorTable, "Builder", activeObject.EditorTable, activeObject.EditorRecordID, ""); err == nil {
				data["AppEditorActiveFormData"] = formData
			}
		} else if formData, ok := buildAppEditorObjectFormData(w, r, activeObject); ok {
			data["AppEditorActiveFormData"] = formData
		}
	}

	if err := templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, "Error rendering page builder", http.StatusInternalServerError)
	}
}

func listAppEditorData(ctx context.Context, selectedApp string) (appEditorSelection, error) {
	registry, err := listRegisteredApps(ctx)
	if err != nil {
		return appEditorSelection{}, err
	}
	tables, err := db.ListBuilderTables(ctx)
	if err != nil {
		return appEditorSelection{}, err
	}
	if len(registry) == 0 {
		registry = deriveRegisteredAppsFromTables(tables)
	}

	type appBucket struct {
		summary           appEditorAppSummary
		definitionObjects []appEditorObject
		explorerTables    []appEditorExplorerTable
		dependencies      []appEditorObject
		forms             []appEditorFormGroup
		roles             []appEditorObject
		clientScripts     []appEditorClientScriptGroup
		services          []appEditorServiceGroup
		endpoints         []appEditorObject
		triggers          []appEditorObject
		schedules         []appEditorObject
		pages             []appEditorPageGroup
		seeds             []appEditorObject
		documentation     []appEditorDocumentationGroup
		objects           []appEditorObject
	}

	buckets := map[string]*appBucket{}
	for _, app := range registry {
		buckets[app.Name] = &appBucket{
			summary: appEditorAppSummary{
				ID:                  app.ID,
				Name:                app.Name,
				Label:               app.Label,
				Description:         app.Description,
				DefinitionVersion:   app.DefinitionVersion,
				PublishedVersion:    app.PublishedVersion,
				HasUnpublishedDraft: app.DefinitionVersion > app.PublishedVersion,
			},
		}
	}

	for _, app := range registry {
		definition := app.DraftDefinition
		if definition == nil {
			definition = app.Definition
		}
		if definition == nil {
			continue
		}
		bucket := buckets[app.Name]
		if bucket == nil {
			continue
		}

		definitionObject := buildYAMLAppEditorDefinition(app)
		bucket.definitionObjects = append(bucket.definitionObjects, definitionObject)
		bucket.objects = append(bucket.objects, definitionObject)
		yamlObject := buildYAMLAppEditorRawYAML(app)
		bucket.definitionObjects = append(bucket.definitionObjects, yamlObject)
		bucket.objects = append(bucket.objects, yamlObject)
		for _, role := range definition.Roles {
			roleObject := buildYAMLAppEditorRole(app, role)
			bucket.roles = append(bucket.roles, roleObject)
			bucket.objects = append(bucket.objects, roleObject)
		}
		for _, table := range definition.Tables {
			explorerTable, objectList := buildYAMLAppEditorTable(ctx, app, table)
			bucket.explorerTables = append(bucket.explorerTables, explorerTable)
			bucket.objects = append(bucket.objects, objectList...)
		}
		for _, script := range definition.ClientScripts {
			scriptGroup, scriptObjects := buildYAMLAppEditorClientScript(app, script)
			if !attachAppEditorTableClientScript(bucket.explorerTables, script, scriptGroup) {
				bucket.clientScripts = append(bucket.clientScripts, scriptGroup)
			}
			bucket.objects = append(bucket.objects, scriptObjects...)
		}
		for _, service := range definition.Services {
			serviceGroup, serviceObjects := buildYAMLAppEditorService(app, service)
			bucket.services = append(bucket.services, serviceGroup)
			bucket.objects = append(bucket.objects, serviceObjects...)
		}
		for _, endpoint := range definition.Endpoints {
			endpointObject := buildYAMLAppEditorEndpoint(app, endpoint)
			bucket.endpoints = append(bucket.endpoints, endpointObject)
			bucket.objects = append(bucket.objects, endpointObject)
		}
		for _, trigger := range definition.Triggers {
			triggerObject := buildYAMLAppEditorTrigger(app, trigger)
			bucket.triggers = append(bucket.triggers, triggerObject)
			bucket.objects = append(bucket.objects, triggerObject)
		}
		for _, schedule := range definition.Schedules {
			scheduleObject := buildYAMLAppEditorSchedule(app, schedule)
			bucket.schedules = append(bucket.schedules, scheduleObject)
			bucket.objects = append(bucket.objects, scheduleObject)
		}
		for _, dependency := range definition.Dependencies {
			dependencyObject := buildYAMLAppEditorDependency(app, dependency)
			bucket.dependencies = append(bucket.dependencies, dependencyObject)
			bucket.objects = append(bucket.objects, dependencyObject)
		}
		for _, page := range definition.Pages {
			pageGroup, pageObjects := buildYAMLAppEditorPage(app, page)
			bucket.pages = append(bucket.pages, pageGroup)
			bucket.objects = append(bucket.objects, pageObjects...)
		}
		for index, seed := range definition.Seeds {
			seedObject := buildYAMLAppEditorSeed(app, seed, index)
			bucket.seeds = append(bucket.seeds, seedObject)
			bucket.objects = append(bucket.objects, seedObject)
		}
		for _, article := range definition.Documentation {
			docGroup, docObjects := buildYAMLAppEditorDocumentation(app, article)
			bucket.documentation = append(bucket.documentation, docGroup)
			bucket.objects = append(bucket.objects, docObjects...)
		}
	}

	for _, table := range tables {
		appName, objectName, include := deriveAppForTable(table.Name, registry)
		if !include {
			continue
		}
		if appHasYAMLDefinition(registry, appName) || appUsesYAMLDefinition(registry, table.Name) {
			continue
		}

		bucket := buckets[appName]
		if bucket == nil {
			continue
		}

		columns, err := db.ListBuilderColumns(ctx, table.Name)
		if err != nil {
			return appEditorSelection{}, err
		}
		explorerTable, objectList := buildDBAppEditorTable(table, objectName, columns)
		bucket.explorerTables = append(bucket.explorerTables, explorerTable)
		bucket.objects = append(bucket.objects, objectList...)
	}

	apps := make([]appEditorAppSummary, 0, len(buckets))
	for _, bucket := range buckets {
		sort.Slice(bucket.definitionObjects, func(i, j int) bool {
			return bucket.definitionObjects[i].Label < bucket.definitionObjects[j].Label
		})
		sort.Slice(bucket.explorerTables, func(i, j int) bool {
			return bucket.explorerTables[i].Table.Label < bucket.explorerTables[j].Table.Label
		})
		for i := range bucket.explorerTables {
			sort.Slice(bucket.explorerTables[i].DataPolicies, func(left, right int) bool {
				return bucket.explorerTables[i].DataPolicies[left].Label < bucket.explorerTables[i].DataPolicies[right].Label
			})
			sort.Slice(bucket.explorerTables[i].TriggerItems, func(left, right int) bool {
				return bucket.explorerTables[i].TriggerItems[left].Label < bucket.explorerTables[i].TriggerItems[right].Label
			})
			sort.Slice(bucket.explorerTables[i].RelatedLists, func(left, right int) bool {
				return bucket.explorerTables[i].RelatedLists[left].Label < bucket.explorerTables[i].RelatedLists[right].Label
			})
			sort.Slice(bucket.explorerTables[i].ClientScripts, func(left, right int) bool {
				return bucket.explorerTables[i].ClientScripts[left].Script.Label < bucket.explorerTables[i].ClientScripts[right].Script.Label
			})
		}
		sort.Slice(bucket.dependencies, func(i, j int) bool {
			return bucket.dependencies[i].Label < bucket.dependencies[j].Label
		})
		sort.Slice(bucket.forms, func(i, j int) bool {
			return bucket.forms[i].Form.Label < bucket.forms[j].Form.Label
		})
		sort.Slice(bucket.roles, func(i, j int) bool {
			return bucket.roles[i].Label < bucket.roles[j].Label
		})
		sort.Slice(bucket.clientScripts, func(i, j int) bool {
			return bucket.clientScripts[i].Script.Label < bucket.clientScripts[j].Script.Label
		})
		sort.Slice(bucket.services, func(i, j int) bool {
			return bucket.services[i].Service.Label < bucket.services[j].Service.Label
		})
		sort.Slice(bucket.endpoints, func(i, j int) bool {
			return bucket.endpoints[i].Label < bucket.endpoints[j].Label
		})
		sort.Slice(bucket.triggers, func(i, j int) bool {
			return bucket.triggers[i].Label < bucket.triggers[j].Label
		})
		sort.Slice(bucket.schedules, func(i, j int) bool {
			return bucket.schedules[i].Label < bucket.schedules[j].Label
		})
		sort.Slice(bucket.pages, func(i, j int) bool {
			return bucket.pages[i].Page.Label < bucket.pages[j].Page.Label
		})
		sort.Slice(bucket.seeds, func(i, j int) bool {
			return bucket.seeds[i].Label < bucket.seeds[j].Label
		})
		sort.Slice(bucket.documentation, func(i, j int) bool {
			return bucket.documentation[i].Article.Label < bucket.documentation[j].Article.Label
		})
		bucket.summary.ObjectCount = len(bucket.objects)
		apps = append(apps, bucket.summary)
	}
	sort.Slice(apps, func(i, j int) bool {
		return apps[i].Label < apps[j].Label
	})

	if selectedApp == "" && len(apps) > 0 {
		selectedApp = apps[0].Name
	}

	selected := appEditorAppSummary{}
	if bucket := buckets[selectedApp]; bucket != nil {
		selected = bucket.summary
		selected.ObjectCount = len(bucket.objects)
		return appEditorSelection{
			Apps:              apps,
			SelectedApp:       selected,
			DefinitionObjects: bucket.definitionObjects,
			ExplorerTables:    bucket.explorerTables,
			Dependencies:      bucket.dependencies,
			Forms:             bucket.forms,
			Roles:             bucket.roles,
			ClientScripts:     bucket.clientScripts,
			Services:          bucket.services,
			Endpoints:         bucket.endpoints,
			Triggers:          bucket.triggers,
			Schedules:         bucket.schedules,
			Pages:             bucket.pages,
			Seeds:             bucket.seeds,
			Documentation:     bucket.documentation,
			Objects:           bucket.objects,
		}, nil
	}

	return appEditorSelection{
		Apps:        apps,
		SelectedApp: selected,
	}, nil
}

func buildDBAppEditorTable(table db.BuilderTableSummary, objectName string, columns []db.BuilderColumnSummary) (appEditorExplorerTable, []appEditorObject) {
	label := strings.TrimSpace(table.LabelPlural)
	if label == "" {
		label = humanizeAppItemName(objectName)
	}
	meta := fmt.Sprintf("%d columns", table.ColumnCount)
	if table.Description != "" {
		meta = table.Description
	}

	objectColumns := make([]appEditorColumn, 0, len(columns))
	columnObjects := make([]appEditorObject, 0, len(columns))
	tableObjectID := "table:" + table.Name
	for _, column := range columns {
		objectColumns = append(objectColumns, appEditorColumn{
			ID:             column.ID,
			Name:           column.Name,
			Label:          column.Label,
			DataType:       column.DataType,
			IsNullable:     column.IsNullable,
			DefaultValue:   column.DefaultValue,
			Prefix:         column.Prefix,
			ValidationRule: column.ValidationRule,
		})
		columnLabel := strings.TrimSpace(column.Label)
		if columnLabel == "" {
			columnLabel = humanizeAppItemName(column.Name)
		}
		columnObject := appEditorObject{
			ID:           fmt.Sprintf("column:%s:%s", table.Name, column.Name),
			ParentID:     tableObjectID,
			Kind:         "column",
			Name:         column.Name,
			PhysicalName: fmt.Sprintf("%s.%s", table.Name, column.Name),
			Label:        columnLabel,
			Description:  fmt.Sprintf("%s column on %s", column.Name, table.Name),
			Meta:         column.DataType,
			ColumnDetail: appEditorColumn{
				Name:           column.Name,
				Label:          column.Label,
				DataType:       column.DataType,
				IsNullable:     column.IsNullable,
				DefaultValue:   column.DefaultValue,
				Prefix:         column.Prefix,
				ValidationRule: column.ValidationRule,
			},
			Published: true,
		}
		columnObjects = append(columnObjects, columnObject)
	}

	tableObject := appEditorObject{
		ID:            tableObjectID,
		Kind:          "table",
		Name:          objectName,
		PhysicalName:  table.Name,
		Label:         label,
		LabelSingular: strings.TrimSpace(table.LabelSingular),
		LabelPlural:   strings.TrimSpace(table.LabelPlural),
		Description:   strings.TrimSpace(table.Description),
		Href:          "/t/" + table.Name,
		Meta:          meta,
		ColumnCount:   table.ColumnCount,
		Columns:       objectColumns,
		SecurityRules: defaultAppEditorSecurityRules(table.Name),
		Published:     true,
	}
	securityObjects := defaultAppEditorSecurityItems(table.Name, tableObjectID)
	objects := []appEditorObject{tableObject}
	objects = append(objects, columnObjects...)
	objects = append(objects, securityObjects...)

	return appEditorExplorerTable{
		Table:         tableObject,
		Columns:       columnObjects,
		SecurityItems: securityObjects,
	}, objects
}

func publishedDefinitionTable(app appRegistryItem, tableName string) (db.AppDefinitionTable, bool) {
	if app.Definition == nil {
		return db.AppDefinitionTable{}, false
	}
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	for _, table := range app.Definition.Tables {
		if table.Name == tableName {
			return table, true
		}
	}
	return db.AppDefinitionTable{}, false
}

func publishedDefinitionHasColumn(app appRegistryItem, tableName, columnName string) bool {
	if db.IsSystemColumnName(columnName) {
		_, ok := publishedDefinitionTable(app, tableName)
		return ok
	}
	table, ok := publishedDefinitionTable(app, tableName)
	if !ok {
		return false
	}
	columnName = strings.TrimSpace(strings.ToLower(columnName))
	for _, column := range table.Columns {
		if column.Name == columnName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasRole(app appRegistryItem, roleName string) bool {
	if app.Definition == nil {
		return false
	}
	roleName = strings.TrimSpace(strings.ToLower(roleName))
	for _, role := range app.Definition.Roles {
		if role.Name == roleName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasDependency(app appRegistryItem, dependency string) bool {
	if app.Definition == nil {
		return false
	}
	dependency = strings.TrimSpace(strings.ToLower(dependency))
	for _, item := range app.Definition.Dependencies {
		if item == dependency {
			return true
		}
	}
	return false
}

func publishedDefinitionService(app appRegistryItem, serviceName string) (db.AppDefinitionService, bool) {
	if app.Definition == nil {
		return db.AppDefinitionService{}, false
	}
	serviceName = strings.TrimSpace(strings.ToLower(serviceName))
	for _, service := range app.Definition.Services {
		if service.Name == serviceName {
			return service, true
		}
	}
	return db.AppDefinitionService{}, false
}

func publishedDefinitionHasMethod(app appRegistryItem, serviceName, methodName string) bool {
	service, ok := publishedDefinitionService(app, serviceName)
	if !ok {
		return false
	}
	methodName = strings.TrimSpace(strings.ToLower(methodName))
	for _, method := range service.Methods {
		if method.Name == methodName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasTrigger(app appRegistryItem, triggerName string) bool {
	if app.Definition == nil {
		return false
	}
	triggerName = strings.TrimSpace(strings.ToLower(triggerName))
	for _, trigger := range app.Definition.Triggers {
		if trigger.Name == triggerName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasSchedule(app appRegistryItem, scheduleName string) bool {
	if app.Definition == nil {
		return false
	}
	scheduleName = strings.TrimSpace(strings.ToLower(scheduleName))
	for _, schedule := range app.Definition.Schedules {
		if schedule.Name == scheduleName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasPage(app appRegistryItem, slug string) bool {
	if app.Definition == nil {
		return false
	}
	slug = strings.TrimSpace(strings.ToLower(slug))
	for _, page := range app.Definition.Pages {
		if page.Slug == slug {
			return true
		}
	}
	return false
}

func publishedDefinitionHasFormAsset(app appRegistryItem, formName string) bool {
	if app.Definition == nil {
		return false
	}
	formName = strings.TrimSpace(strings.ToLower(formName))
	for _, form := range app.Definition.Forms {
		if form.Name == formName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasTableForm(app appRegistryItem, tableName, formName string) bool {
	if app.Definition == nil {
		return false
	}
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	formName = strings.TrimSpace(strings.ToLower(formName))
	for _, table := range app.Definition.Tables {
		if table.Name != tableName {
			continue
		}
		for _, form := range table.Forms {
			name := strings.TrimSpace(strings.ToLower(form.Name))
			if name == "" {
				name = "default"
			}
			if name == formName {
				return true
			}
		}
	}
	return false
}

func publishedDefinitionHasTableDataPolicy(app appRegistryItem, tableName, policyName string) bool {
	table, ok := publishedDefinitionTable(app, tableName)
	if !ok {
		return false
	}
	policyName = strings.TrimSpace(strings.ToLower(policyName))
	for _, policy := range table.DataPolicies {
		if policy.Name == policyName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasTableTrigger(app appRegistryItem, tableName, triggerName string) bool {
	table, ok := publishedDefinitionTable(app, tableName)
	if !ok {
		return false
	}
	triggerName = strings.TrimSpace(strings.ToLower(triggerName))
	for _, trigger := range table.Triggers {
		if trigger.Name == triggerName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasTableRelatedList(app appRegistryItem, tableName, relatedListName string) bool {
	table, ok := publishedDefinitionTable(app, tableName)
	if !ok {
		return false
	}
	relatedListName = strings.TrimSpace(strings.ToLower(relatedListName))
	for _, related := range table.RelatedLists {
		if related.Name == relatedListName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasTableSecurityRule(app appRegistryItem, tableName, ruleName string) bool {
	table, ok := publishedDefinitionTable(app, tableName)
	if !ok {
		return false
	}
	ruleName = strings.TrimSpace(strings.ToLower(ruleName))
	for _, rule := range table.Security.Rules {
		if rule.Name == ruleName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasClientScript(app appRegistryItem, scriptName string) bool {
	if app.Definition == nil {
		return false
	}
	scriptName = strings.TrimSpace(strings.ToLower(scriptName))
	for _, script := range app.Definition.ClientScripts {
		if script.Name == scriptName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasEndpoint(app appRegistryItem, endpointName string) bool {
	if app.Definition == nil {
		return false
	}
	endpointName = strings.TrimSpace(strings.ToLower(endpointName))
	for _, endpoint := range app.Definition.Endpoints {
		if endpoint.Name == endpointName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasDocumentation(app appRegistryItem, articleName string) bool {
	if app.Definition == nil {
		return false
	}
	articleName = strings.TrimSpace(strings.ToLower(articleName))
	for _, article := range app.Definition.Documentation {
		if article.Name == articleName {
			return true
		}
	}
	return false
}

func publishedDefinitionHasSeed(app appRegistryItem, tableName string, index int) bool {
	if app.Definition == nil || index < 0 || index >= len(app.Definition.Seeds) {
		return false
	}
	return app.Definition.Seeds[index].Table == strings.TrimSpace(strings.ToLower(tableName))
}

func effectiveAppEditorForms(definition *db.AppDefinition) []appEditorFormBinding {
	if definition == nil {
		return nil
	}

	items := make([]appEditorFormBinding, 0, allocHintSum(len(definition.Forms), len(definition.Tables)))
	seen := map[string]bool{}
	for _, form := range definition.Forms {
		name := strings.TrimSpace(strings.ToLower(form.Name))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, appEditorFormBinding{
			Source: "asset",
			Table:  form.Table,
			Form:   form,
		})
	}

	for _, table := range definition.Tables {
		for _, form := range table.Forms {
			name := strings.TrimSpace(strings.ToLower(form.Name))
			if name == "" {
				name = "default"
			}
			legacyID := table.Name + ":" + name
			if seen[legacyID] {
				continue
			}
			seen[legacyID] = true
			items = append(items, appEditorFormBinding{
				Source: "legacy",
				Table:  table.Name,
				Form: db.AppDefinitionAssetForm{
					Name:        legacyID,
					Table:       table.Name,
					Label:       form.Label,
					Description: fmt.Sprintf("%s form on %s", name, table.Name),
					Fields:      append([]string(nil), form.Fields...),
					Layout:      append([]string(nil), form.Fields...),
				},
			})
		}
	}

	return items
}

func attachAppEditorTableClientScript(explorerTables []appEditorExplorerTable, script db.AppDefinitionClientScript, group appEditorClientScriptGroup) bool {
	tableName := strings.TrimSpace(strings.ToLower(script.Table))
	if tableName == "" {
		return false
	}
	for i := range explorerTables {
		if strings.TrimSpace(strings.ToLower(explorerTables[i].Table.PhysicalName)) != tableName {
			continue
		}
		explorerTables[i].ClientScripts = append(explorerTables[i].ClientScripts, group)
		return true
	}
	return false
}

func buildYAMLAppEditorTable(ctx context.Context, app appRegistryItem, table db.AppDefinitionTable) (appEditorExplorerTable, []appEditorObject) {
	label := strings.TrimSpace(table.LabelPlural)
	if label == "" {
		label = humanizeAppItemName(table.Name)
	}
	_, tablePublished := publishedDefinitionTable(app, table.Name)

	allColumns := db.BuildYAMLColumnsWithContext(ctx, appEditorRegisteredApp(app), table)
	objectColumns := make([]appEditorColumn, 0, len(allColumns))
	columnObjects := make([]appEditorObject, 0, len(allColumns))
	systemColumnObjects := make([]appEditorObject, 0, len(allColumns))
	tableObjectID := "table:" + table.Name
	for _, column := range allColumns {
		columnLabel := strings.TrimSpace(column.LABEL)
		if columnLabel == "" {
			columnLabel = humanizeAppItemName(column.NAME)
		}
		objectColumns = append(objectColumns, appEditorColumn{
			ID:             column.ID,
			Name:           column.NAME,
			Label:          column.LABEL,
			DataType:       column.DATA_TYPE,
			IsNullable:     column.IS_NULLABLE,
			DefaultValue:   column.DEFAULT_VALUE.String,
			Prefix:         column.PREFIX.String,
			ValidationRule: column.VALIDATION_REGEX.String,
		})
		description := fmt.Sprintf("%s column on %s (YAML-defined)", column.NAME, table.Name)
		editable := !db.IsSystemColumnName(column.NAME)
		if !editable {
			description = fmt.Sprintf("%s system column on %s. Added automatically by the platform.", column.NAME, table.Name)
		}
		formProperties := buildAppEditorColumnFormProperties(column, editable)
		columnObject := appEditorObject{
			ID:           fmt.Sprintf("column:%s:%s", table.Name, column.NAME),
			ParentID:     tableObjectID,
			Kind:         "column",
			Name:         column.NAME,
			PhysicalName: fmt.Sprintf("%s.%s", table.Name, column.NAME),
			Label:        columnLabel,
			Description:  description,
			Meta:         column.DATA_TYPE,
			ColumnDetail: appEditorColumn{
				Name:           column.NAME,
				Label:          column.LABEL,
				DataType:       column.DATA_TYPE,
				IsNullable:     column.IS_NULLABLE,
				DefaultValue:   column.DEFAULT_VALUE.String,
				Prefix:         column.PREFIX.String,
				ValidationRule: column.VALIDATION_REGEX.String,
			},
			FormProperties: formProperties,
			Editable:       editable,
			Published:      publishedDefinitionHasColumn(app, table.Name, column.NAME),
			Deletable:      editable,
			System:         db.IsSystemColumnName(column.NAME),
		}
		if columnObject.System {
			systemColumnObjects = append(systemColumnObjects, columnObject)
			continue
		}
		columnObjects = append(columnObjects, columnObject)
	}

	meta := fmt.Sprintf("%d columns", len(allColumns))
	if table.Description != "" {
		meta = table.Description
	}
	tableObject := appEditorObject{
		ID:            tableObjectID,
		Kind:          "table",
		Name:          table.Name,
		PhysicalName:  table.Name,
		Label:         label,
		LabelSingular: table.LabelSingular,
		LabelPlural:   table.LabelPlural,
		Description:   table.Description,
		Href:          "/t/" + table.Name,
		Meta:          meta,
		ColumnCount:   len(allColumns),
		Columns:       objectColumns,
		SecurityRules: defaultAppEditorSecurityRules(table.Name),
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("name", "Name", table.Name, "text"),
			newReadOnlyAppEditorFormProperty("extends", "Extends", table.Extends, "text"),
			newAppEditorFormProperty("extensible", "Extensible", boolAppEditorValue(table.Extensible), "bool"),
			newAppEditorFormProperty("label_singular", "Label Singular", table.LabelSingular, "text"),
			newAppEditorFormProperty("label_plural", "Label Plural", table.LabelPlural, "text"),
			newChoiceAppEditorFormProperty("display_field", "Display Field", table.DisplayField, appEditorDisplayFieldChoices(allColumns, table.DisplayField)),
			newAppEditorFormProperty("description", "Description", table.Description, "textarea"),
		},
		Editable:  true,
		Published: tablePublished,
		Deletable: true,
	}
	dataPolicyObjects := make([]appEditorObject, 0, len(table.DataPolicies))
	for _, policy := range table.DataPolicies {
		dataPolicyObjects = append(dataPolicyObjects, buildYAMLAppEditorTableDataPolicy(app, table.Name, tableObjectID, policy))
	}
	tableTriggerObjects := make([]appEditorObject, 0, len(table.Triggers))
	for _, trigger := range table.Triggers {
		tableTriggerObjects = append(tableTriggerObjects, buildYAMLAppEditorTableTrigger(app, table.Name, tableObjectID, trigger))
	}
	relatedListObjects := make([]appEditorObject, 0, len(table.RelatedLists))
	for _, relatedList := range table.RelatedLists {
		relatedListObjects = append(relatedListObjects, buildYAMLAppEditorRelatedList(app, table.Name, tableObjectID, relatedList))
	}
	securityObjects := make([]appEditorObject, 0, len(table.Security.Rules))
	for _, rule := range table.Security.Rules {
		securityObjects = append(securityObjects, buildYAMLAppEditorTableSecurityRule(app, table.Name, tableObjectID, rule))
	}
	formGroups := make([]appEditorFormGroup, 0, len(table.Forms))
	formObjects := make([]appEditorObject, 0, allocHintMul(len(table.Forms), 4))
	for _, form := range table.Forms {
		formGroup, items := buildYAMLAppEditorForm(app, table.Name, form)
		formGroups = append(formGroups, formGroup)
		formObjects = append(formObjects, items...)
	}

	objects := []appEditorObject{tableObject}
	objects = append(objects, columnObjects...)
	objects = append(objects, systemColumnObjects...)
	objects = append(objects, dataPolicyObjects...)
	objects = append(objects, tableTriggerObjects...)
	objects = append(objects, relatedListObjects...)
	objects = append(objects, securityObjects...)
	objects = append(objects, formObjects...)

	return appEditorExplorerTable{
		Table:         tableObject,
		Forms:         formGroups,
		Columns:       columnObjects,
		SystemColumns: systemColumnObjects,
		DataPolicies:  dataPolicyObjects,
		TriggerItems:  tableTriggerObjects,
		RelatedLists:  relatedListObjects,
		SecurityItems: securityObjects,
	}, objects
}

func buildYAMLAppEditorTableDataPolicy(app appRegistryItem, tableName, parentID string, policy db.AppDefinitionDataPolicy) appEditorObject {
	label := strings.TrimSpace(policy.Label)
	if label == "" {
		label = humanizeAppItemName(policy.Name)
	}
	meta := strings.TrimSpace(policy.Action)
	if meta == "" {
		if policy.Enabled {
			meta = "Enabled"
		} else {
			meta = "Disabled"
		}
	}
	return appEditorObject{
		ID:          fmt.Sprintf("table-data-policy:%s:%s", tableName, policy.Name),
		ParentID:    parentID,
		Kind:        "data_policy",
		Name:        policy.Name,
		Label:       label,
		Description: firstNonEmpty(policy.Description, fmt.Sprintf("Data policy on %s.", tableName)),
		Meta:        meta,
		Editable:    true,
		Published:   publishedDefinitionHasTableDataPolicy(app, tableName, policy.Name),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("table_name", "Table", tableName, "text"),
			newReadOnlyAppEditorFormProperty("name", "Name", policy.Name, "text"),
			newAppEditorFormProperty("label", "Label", policy.Label, "text"),
			newAppEditorFormProperty("description", "Description", policy.Description, "textarea"),
			newAppEditorFormProperty("condition", "Condition", policy.Condition, "textarea"),
			newAppEditorFormProperty("action", "Action", policy.Action, "text"),
			newAppEditorFormProperty("enabled", "Enabled", boolAppEditorValue(policy.Enabled), "bool"),
		},
	}
}

func buildYAMLAppEditorTableTrigger(app appRegistryItem, tableName, parentID string, trigger db.AppDefinitionTrigger) appEditorObject {
	label := strings.TrimSpace(trigger.Label)
	if label == "" {
		label = humanizeAppItemName(trigger.Name)
	}
	return appEditorObject{
		ID:          fmt.Sprintf("table-trigger:%s:%s", tableName, trigger.Name),
		ParentID:    parentID,
		Kind:        "table_trigger",
		Name:        trigger.Name,
		Label:       label,
		Description: firstNonEmpty(trigger.Description, fmt.Sprintf("Record trigger on %s.", tableName)),
		Meta:        firstNonEmpty(trigger.Event, "trigger"),
		Editable:    true,
		Published:   publishedDefinitionHasTableTrigger(app, tableName, trigger.Name),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("table_name", "Table", tableName, "text"),
			newReadOnlyAppEditorFormProperty("name", "Name", trigger.Name, "text"),
			newAppEditorFormProperty("label", "Label", trigger.Label, "text"),
			newAppEditorFormProperty("description", "Description", trigger.Description, "textarea"),
			newAppEditorFormProperty("event", "Event", trigger.Event, "text"),
			newAppEditorFormProperty("condition", "Condition", trigger.Condition, "textarea"),
			newAppEditorFormProperty("call", "Call", trigger.Call, "text"),
			newAppEditorFormProperty("mode", "Mode", trigger.Mode, "text"),
			newAppEditorFormProperty("order", "Order", fmt.Sprintf("%d", trigger.Order), "text"),
			newAppEditorFormProperty("enabled", "Enabled", boolAppEditorValue(trigger.Enabled), "bool"),
		},
	}
}

func buildYAMLAppEditorRelatedList(app appRegistryItem, tableName, parentID string, related db.AppDefinitionRelatedList) appEditorObject {
	label := strings.TrimSpace(related.Label)
	if label == "" {
		label = humanizeAppItemName(related.Name)
	}
	meta := strings.TrimSpace(related.Table)
	if related.ReferenceField != "" {
		meta = strings.TrimSpace(meta + " via " + related.ReferenceField)
	}
	return appEditorObject{
		ID:          fmt.Sprintf("table-related-list:%s:%s", tableName, related.Name),
		ParentID:    parentID,
		Kind:        "related_list",
		Name:        related.Name,
		Label:       label,
		Description: fmt.Sprintf("Related list on %s.", tableName),
		Meta:        meta,
		Editable:    true,
		Published:   publishedDefinitionHasTableRelatedList(app, tableName, related.Name),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("table_name", "Table", tableName, "text"),
			newReadOnlyAppEditorFormProperty("name", "Name", related.Name, "text"),
			newAppEditorFormProperty("label", "Label", related.Label, "text"),
			newAppEditorFormProperty("table", "Target Table", related.Table, "text"),
			newAppEditorFormProperty("reference_field", "Reference Field", related.ReferenceField, "text"),
			newAppEditorFormProperty("columns", "Columns", formatAppEditorJSON(related.Columns), "json"),
		},
	}
}

func buildAppEditorColumnFormProperties(column db.Column, editable bool) []appEditorFormProperty {
	dataTypeProperty := newChoiceAppEditorFormProperty("data_type", "Data Type", column.DATA_TYPE, appEditorColumnTypeChoices(column.DATA_TYPE))
	if !editable {
		dataTypeProperty.ReadOnly = true
	}

	properties := []appEditorFormProperty{
		newReadOnlyAppEditorFormProperty("name", "Name", column.NAME, "text"),
		newAppEditorFormProperty("label", "Label", column.LABEL, "text"),
		dataTypeProperty,
		newAppEditorFormProperty("is_nullable", "Is Nullable", boolAppEditorValue(column.IS_NULLABLE), "bool"),
		newConditionalAppEditorFormProperty("prefix", "Prefix", column.PREFIX.String, "text", "data_type=autnumber"),
		newConditionalAppEditorFormProperty("reference_table", "Reference Table", column.REFERENCE_TABLE.String, "text", "data_type=reference"),
		newConditionalAppEditorFormProperty("choices", "Choices", formatAppEditorJSON(column.CHOICES), "json", "data_type=choice"),
		newAppEditorFormProperty("default_value", "Default Value", column.DEFAULT_VALUE.String, "text"),
		newAppEditorFormProperty("validation_regex", "Validation Regex", column.VALIDATION_REGEX.String, "text"),
		newAppEditorFormProperty("condition_expr", "Condition Expression", column.CONDITION_EXPR.String, "textarea"),
		newAppEditorFormProperty("validation_message", "Validation Message", column.VALIDATION_MSG.String, "textarea"),
	}
	if editable {
		return properties
	}
	for i := range properties {
		properties[i].ReadOnly = true
	}
	return properties
}

func buildYAMLAppEditorForm(app appRegistryItem, tableName string, form db.AppDefinitionForm) (appEditorFormGroup, []appEditorObject) {
	name := strings.TrimSpace(strings.ToLower(form.Name))
	if name == "" {
		name = "default"
	}
	label := strings.TrimSpace(form.Label)
	if label == "" {
		label = humanizeAppItemName(name)
	}
	parentID := fmt.Sprintf("tableform:%s:%s", tableName, name)
	published := publishedDefinitionHasTableForm(app, tableName, name)

	formObject := appEditorObject{
		ID:           parentID,
		Kind:         "form",
		Name:         name,
		Label:        label,
		PhysicalName: tableName,
		Description:  firstNonEmpty(form.Description, fmt.Sprintf("Form for %s.", tableName)),
		Meta:         tableName,
		Editable:     true,
		Published:    published,
		Deletable:    true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("name", "Name", name, "text"),
			newReadOnlyAppEditorFormProperty("table", "Table", tableName, "text"),
			newAppEditorFormProperty("label", "Label", form.Label, "text"),
			newAppEditorFormProperty("description", "Description", form.Description, "textarea"),
		},
	}

	layoutID := strings.Replace(parentID, "tableform:", "tableform-layout:", 1)
	layoutObject := appEditorObject{
		ID:          layoutID,
		ParentID:    parentID,
		Kind:        "form_layout",
		Name:        "layout",
		Label:       "Form Elements",
		Description: fmt.Sprintf("Fields and layout for the %s form.", label),
		Meta:        fmt.Sprintf("%d fields", len(form.Fields)),
		Editable:    true,
		Published:   published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("form_name", "Form", name, "text"),
			newAppEditorFormProperty("layout", "Fields / Layout", formatAppEditorJSON(form.Fields), "json"),
		},
	}

	actionsID := strings.Replace(parentID, "tableform:", "tableform-actions:", 1)
	actionsObject := appEditorObject{
		ID:          actionsID,
		ParentID:    parentID,
		Kind:        "form_actions",
		Name:        "actions",
		Label:       "Actions",
		Description: fmt.Sprintf("Actions available from the %s form.", label),
		Meta:        fmt.Sprintf("%d actions", len(form.Actions)),
		Editable:    true,
		Published:   published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("form_name", "Form", name, "text"),
			newAppEditorFormProperty("actions", "Actions", formatAppEditorJSON(form.Actions), "json"),
		},
	}

	securityID := strings.Replace(parentID, "tableform:", "tableform-security:", 1)
	securityObject := appEditorObject{
		ID:          securityID,
		ParentID:    parentID,
		Kind:        "form_security",
		Name:        "security",
		Label:       "Security",
		Description: fmt.Sprintf("Security rules for the %s form.", label),
		Meta:        securitySummary(form.Security),
		Editable:    true,
		Published:   published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("form_name", "Form", name, "text"),
			newAppEditorFormProperty("security", "Security", formatAppEditorJSON(form.Security), "json"),
		},
	}

	objects := []appEditorObject{formObject, layoutObject, actionsObject, securityObject}
	return appEditorFormGroup{
		Table:    tableName,
		Form:     formObject,
		Layout:   layoutObject,
		Actions:  actionsObject,
		Security: securityObject,
	}, objects
}

func buildYAMLAppEditorPage(app appRegistryItem, page db.AppDefinitionPage) (appEditorPageGroup, []appEditorObject) {
	title := firstNonEmpty(page.Label, page.Name, humanizeAppItemName(page.Slug))
	parentID := fmt.Sprintf("page:%s:%s", app.Name, page.Slug)
	runtimeSlug := db.QualifiedPageSlug(app.Namespace, page.Slug)
	if runtimeSlug == "" {
		runtimeSlug = page.Slug
	}
	description := fmt.Sprintf("Page /p/%s defined in the %s app.", runtimeSlug, app.Name)
	if strings.TrimSpace(page.SearchKeywords) != "" {
		description += " Search keywords: " + strings.TrimSpace(page.SearchKeywords) + "."
	}
	pageObject := appEditorObject{
		ID:          parentID,
		Kind:        "page",
		Name:        runtimeSlug,
		Label:       title,
		Description: description,
		Meta:        page.Status,
		Editable:    true,
		Published:   publishedDefinitionHasPage(app, page.Slug),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newAppEditorFormProperty("title", "Title", title, "text"),
			newAppEditorFormProperty("search_keywords", "Search Keywords", page.SearchKeywords, "text"),
			newAppEditorFormProperty("content", "Contents", page.Content, "page_wysiwyg"),
		},
	}

	layoutObject := appEditorObject{
		ID:          fmt.Sprintf("page-layout:%s:%s", app.Name, page.Slug),
		ParentID:    parentID,
		Kind:        "page_layout",
		Name:        "layout",
		Label:       "Content",
		Description: fmt.Sprintf("Page content and structure for %s.", title),
		Meta:        page.EditorMode,
		Editable:    true,
		Published:   pageObject.Published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("page_slug", "Page", runtimeSlug, "text"),
			newAppEditorFormProperty("content", "Page Content", page.Content, "page_wysiwyg"),
		},
	}

	actionsObject := appEditorObject{
		ID:          fmt.Sprintf("page-actions:%s:%s", app.Name, page.Slug),
		ParentID:    parentID,
		Kind:        "page_actions",
		Name:        "actions",
		Label:       "Actions",
		Description: fmt.Sprintf("Page actions for %s.", title),
		Meta:        fmt.Sprintf("%d actions", len(page.Actions)),
		Editable:    true,
		Published:   pageObject.Published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("page_slug", "Page", runtimeSlug, "text"),
			newAppEditorFormProperty("actions", "Actions", formatAppEditorJSON(page.Actions), "json"),
		},
	}

	securityObject := appEditorObject{
		ID:          fmt.Sprintf("page-security:%s:%s", app.Name, page.Slug),
		ParentID:    parentID,
		Kind:        "page_security",
		Name:        "security",
		Label:       "Security",
		Description: fmt.Sprintf("Page security for %s.", title),
		Meta:        securitySummary(page.Security),
		Editable:    true,
		Published:   pageObject.Published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("page_slug", "Page", runtimeSlug, "text"),
			newAppEditorFormProperty("security", "Security", formatAppEditorJSON(page.Security), "json"),
		},
	}

	objects := []appEditorObject{pageObject, layoutObject, actionsObject, securityObject}
	return appEditorPageGroup{
		Page:     pageObject,
		Layout:   layoutObject,
		Actions:  actionsObject,
		Security: securityObject,
	}, objects
}

func buildYAMLAppEditorDocumentation(app appRegistryItem, article db.AppDefinitionDocumentation) (appEditorDocumentationGroup, []appEditorObject) {
	label := firstNonEmpty(article.Label, humanizeAppItemName(article.Name))
	parentID := fmt.Sprintf("documentation:%s:%s", app.Name, article.Name)
	parentObject := appEditorObject{
		ID:          parentID,
		Kind:        "documentation",
		Name:        article.Name,
		Label:       label,
		Description: firstNonEmpty(article.Description, "Documentation article"),
		Meta:        article.Visibility,
		Editable:    true,
		Published:   publishedDefinitionHasDocumentation(app, article.Name),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("name", "Name", article.Name, "text"),
			newAppEditorFormProperty("label", "Label", article.Label, "text"),
			newAppEditorFormProperty("description", "Description", article.Description, "textarea"),
		},
	}

	contentObject := appEditorObject{
		ID:          fmt.Sprintf("documentation-content:%s:%s", app.Name, article.Name),
		ParentID:    parentID,
		Kind:        "documentation_content",
		Name:        "content",
		Label:       "Content",
		Description: fmt.Sprintf("Article content for %s.", label),
		Meta:        article.Category,
		Editable:    true,
		Published:   parentObject.Published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("article_name", "Article", article.Name, "text"),
			newAppEditorFormProperty("content", "Content", article.Content, "markdown"),
		},
	}
	categoryObject := appEditorObject{
		ID:          fmt.Sprintf("documentation-category:%s:%s", app.Name, article.Name),
		ParentID:    parentID,
		Kind:        "documentation_category",
		Name:        "category",
		Label:       "Category",
		Description: fmt.Sprintf("Category metadata for %s.", label),
		Meta:        firstNonEmpty(article.Category, "Uncategorized"),
		Editable:    true,
		Published:   parentObject.Published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("article_name", "Article", article.Name, "text"),
			newAppEditorFormProperty("category", "Category", article.Category, "text"),
		},
	}
	visibilityObject := appEditorObject{
		ID:          fmt.Sprintf("documentation-visibility:%s:%s", app.Name, article.Name),
		ParentID:    parentID,
		Kind:        "documentation_visibility",
		Name:        "visibility",
		Label:       "Visibility",
		Description: fmt.Sprintf("Audience rules for %s.", label),
		Meta:        article.Visibility,
		Editable:    true,
		Published:   parentObject.Published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("article_name", "Article", article.Name, "text"),
			newAppEditorFormProperty("visibility", "Visibility", article.Visibility, "text"),
		},
	}
	relatedObject := appEditorObject{
		ID:          fmt.Sprintf("documentation-related:%s:%s", app.Name, article.Name),
		ParentID:    parentID,
		Kind:        "documentation_related",
		Name:        "related",
		Label:       "Related",
		Description: fmt.Sprintf("Related links for %s.", label),
		Meta:        fmt.Sprintf("%d links", len(article.Related)),
		Editable:    true,
		Published:   parentObject.Published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("article_name", "Article", article.Name, "text"),
			newAppEditorFormProperty("related", "Related", formatAppEditorJSON(article.Related), "json"),
		},
	}

	objects := []appEditorObject{parentObject, contentObject, categoryObject, visibilityObject, relatedObject}
	return appEditorDocumentationGroup{
		Article:    parentObject,
		Content:    contentObject,
		Category:   categoryObject,
		Visibility: visibilityObject,
		Related:    relatedObject,
	}, objects
}

func buildYAMLAppEditorClientScript(app appRegistryItem, script db.AppDefinitionClientScript) (appEditorClientScriptGroup, []appEditorObject) {
	label := strings.TrimSpace(script.Label)
	if label == "" {
		label = humanizeAppItemName(script.Name)
	}
	parentID := fmt.Sprintf("client-script:%s:%s", app.Name, script.Name)
	parentObject := appEditorObject{
		ID:          parentID,
		Kind:        "client_script",
		Name:        script.Name,
		Label:       label,
		Description: firstNonEmpty(script.Description, fmt.Sprintf("Client script on %s.", script.Table)),
		Meta:        firstNonEmpty(script.Event, "client"),
		Editable:    true,
		Published:   publishedDefinitionHasClientScript(app, script.Name),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("name", "Name", script.Name, "text"),
			newAppEditorFormProperty("label", "Label", script.Label, "text"),
			newAppEditorFormProperty("description", "Description", script.Description, "textarea"),
			newAppEditorFormProperty("table", "Table", script.Table, "text"),
			newAppEditorFormProperty("event", "Event", script.Event, "text"),
			newAppEditorFormProperty("field", "Field", script.Field, "text"),
			newAppEditorFormProperty("language", "Language", firstNonEmpty(script.Language, "javascript"), "text"),
			newAppEditorFormProperty("enabled", "Enabled", boolAppEditorValue(script.Enabled), "bool"),
		},
	}
	codeObject := appEditorObject{
		ID:          fmt.Sprintf("client-script-code:%s:%s", app.Name, script.Name),
		ParentID:    parentID,
		Kind:        "client_script_code",
		Name:        "code",
		Label:       "Code",
		Description: fmt.Sprintf("Browser implementation for %s.", label),
		Meta:        firstNonEmpty(script.Language, "javascript"),
		Editable:    true,
		Published:   parentObject.Published,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("script_name", "Client Script", script.Name, "text"),
			newAppEditorFormProperty("script", "Code", script.Script, "textarea"),
		},
	}
	return appEditorClientScriptGroup{
		Script: parentObject,
		Code:   codeObject,
	}, []appEditorObject{parentObject, codeObject}
}

func buildYAMLAppEditorEndpoint(app appRegistryItem, endpoint db.AppDefinitionEndpoint) appEditorObject {
	label := strings.TrimSpace(endpoint.Label)
	if label == "" {
		label = humanizeAppItemName(endpoint.Name)
	}
	return appEditorObject{
		ID:          fmt.Sprintf("endpoint:%s:%s", app.Name, endpoint.Name),
		Kind:        "endpoint",
		Name:        endpoint.Name,
		Label:       label,
		Description: firstNonEmpty(endpoint.Description, fmt.Sprintf("%s %s", endpoint.Method, endpoint.Path)),
		Meta:        strings.TrimSpace(endpoint.Method + " " + endpoint.Path),
		Editable:    true,
		Published:   publishedDefinitionHasEndpoint(app, endpoint.Name),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("name", "Name", endpoint.Name, "text"),
			newAppEditorFormProperty("label", "Label", endpoint.Label, "text"),
			newAppEditorFormProperty("description", "Description", endpoint.Description, "textarea"),
			newAppEditorFormProperty("method", "Method", endpoint.Method, "text"),
			newAppEditorFormProperty("path", "Path", endpoint.Path, "text"),
			newAppEditorFormProperty("call", "Call", endpoint.Call, "text"),
			newAppEditorFormProperty("roles", "Roles", formatAppEditorJSON(endpoint.Roles), "json"),
			newAppEditorFormProperty("enabled", "Enabled", boolAppEditorValue(endpoint.Enabled), "bool"),
		},
	}
}

func buildYAMLAppEditorDefinition(app appRegistryItem) appEditorObject {
	dependencies := []string{}
	tableCount := 0
	formCount := 0
	roleCount := 0
	clientScriptCount := 0
	serviceCount := 0
	methodCount := 0
	endpointCount := 0
	triggerCount := 0
	scheduleCount := 0
	pageCount := 0
	seedCount := 0
	documentationCount := 0
	definition := app.DraftDefinition
	if definition == nil {
		definition = app.Definition
	}
	if definition != nil {
		dependencies = definition.Dependencies
		tableCount = len(definition.Tables)
		formCount = len(effectiveAppEditorForms(definition))
		roleCount = len(definition.Roles)
		clientScriptCount = len(definition.ClientScripts)
		serviceCount = len(definition.Services)
		endpointCount = len(definition.Endpoints)
		triggerCount = len(definition.Triggers)
		scheduleCount = len(definition.Schedules)
		for _, service := range definition.Services {
			methodCount += len(service.Methods)
		}
		pageCount = len(definition.Pages)
		seedCount = len(definition.Seeds)
		documentationCount = len(definition.Documentation)
	}

	return appEditorObject{
		ID:          "application:" + app.Name,
		Kind:        "application",
		Name:        app.Name,
		Label:       "Definition",
		Description: "Top-level app definition metadata and YAML summary.",
		Meta:        app.Status,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("name", "Name", app.Name, "text"),
			newReadOnlyAppEditorFormProperty("namespace", "Namespace", app.Namespace, "text"),
			newAppEditorFormProperty("label", "Label", app.Label, "text"),
			newAppEditorFormProperty("description", "Description", app.Description, "textarea"),
			newReadOnlyAppEditorFormProperty("status", "Status", app.Status, "text"),
			newAppEditorFormProperty("dependencies", "Dependencies", formatAppEditorJSON(dependencies), "json"),
			newReadOnlyAppEditorFormProperty("tables_count", "Tables", fmt.Sprintf("%d", tableCount), "text"),
			newReadOnlyAppEditorFormProperty("forms_count", "Forms", fmt.Sprintf("%d", formCount), "text"),
			newReadOnlyAppEditorFormProperty("roles_count", "Roles", fmt.Sprintf("%d", roleCount), "text"),
			newReadOnlyAppEditorFormProperty("client_scripts_count", "Client Scripts", fmt.Sprintf("%d", clientScriptCount), "text"),
			newReadOnlyAppEditorFormProperty("services_count", "Services", fmt.Sprintf("%d", serviceCount), "text"),
			newReadOnlyAppEditorFormProperty("methods_count", "Methods", fmt.Sprintf("%d", methodCount), "text"),
			newReadOnlyAppEditorFormProperty("endpoints_count", "Endpoints", fmt.Sprintf("%d", endpointCount), "text"),
			newReadOnlyAppEditorFormProperty("triggers_count", "Triggers", fmt.Sprintf("%d", triggerCount), "text"),
			newReadOnlyAppEditorFormProperty("schedules_count", "Schedules", fmt.Sprintf("%d", scheduleCount), "text"),
			newReadOnlyAppEditorFormProperty("pages_count", "Pages", fmt.Sprintf("%d", pageCount), "text"),
			newReadOnlyAppEditorFormProperty("seeds_count", "Seeds", fmt.Sprintf("%d", seedCount), "text"),
			newReadOnlyAppEditorFormProperty("documentation_count", "Documentation", fmt.Sprintf("%d", documentationCount), "text"),
			newReadOnlyAppEditorFormProperty("definition_version", "Draft Version", fmt.Sprintf("%d", app.DefinitionVersion), "text"),
			newReadOnlyAppEditorFormProperty("published_version", "Published Version", fmt.Sprintf("%d", app.PublishedVersion), "text"),
		},
		Editable:  true,
		Published: app.PublishedVersion > 0,
	}
}

func buildYAMLAppEditorRawYAML(app appRegistryItem) appEditorObject {
	content := strings.TrimSpace(app.DefinitionYAML)
	if content == "" && app.DraftDefinition != nil {
		content = formatAppEditorYAML(app.DraftDefinition)
	}
	if content == "" && app.Definition != nil {
		content = formatAppEditorYAML(app.Definition)
	}
	return appEditorObject{
		ID:          "yaml:" + app.Name,
		Kind:        "yaml",
		Name:        "raw_yaml",
		Label:       "Raw YAML",
		Description: "Edit the full app definition as YAML.",
		Meta:        "yaml",
		Editable:    true,
		Published:   app.PublishedVersion > 0,
		FormProperties: []appEditorFormProperty{
			newAppEditorFormProperty("definition_yaml", "Definition YAML", content, "textarea"),
		},
	}
}

func buildYAMLAppEditorDependency(app appRegistryItem, dependency string) appEditorObject {
	label := humanizeAppItemName(dependency)
	return appEditorObject{
		ID:          fmt.Sprintf("dependency:%s:%s", app.Name, dependency),
		Kind:        "dependency",
		Name:        dependency,
		Label:       label,
		Description: fmt.Sprintf("%s depends on the %s application.", app.Label, dependency),
		Meta:        "dependency",
		FormProperties: []appEditorFormProperty{
			newAppEditorFormProperty("name", "Name", dependency, "text"),
			newReadOnlyAppEditorFormProperty("source_app", "Source App", app.Name, "text"),
		},
		Editable:  true,
		Published: publishedDefinitionHasDependency(app, dependency),
		Deletable: true,
	}
}

func buildYAMLAppEditorTableSecurityRule(app appRegistryItem, tableName, parentID string, rule db.AppDefinitionSecurityRule) appEditorObject {
	label := humanizeAppItemName(rule.Name)
	description := strings.TrimSpace(rule.Description)
	if description == "" {
		description = fmt.Sprintf("%s %s security rule for %s.", securityEffectLabel(rule.Effect), securityOperationLabel(rule.Operation), tableName)
	}
	effect := securityRuleEffectValue(rule.Effect)
	return appEditorObject{
		ID:          fmt.Sprintf("table-security-rule:%s:%s", tableName, rule.Name),
		ParentID:    parentID,
		Kind:        "security_rule",
		Name:        rule.Name,
		Label:       label,
		Description: description,
		Meta:        securityRuleMeta(rule),
		Editable:    true,
		Published:   publishedDefinitionHasTableSecurityRule(app, tableName, rule.Name),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("table_name", "Table", tableName, "text"),
			newAppEditorFormProperty("name", "Name", rule.Name, "text"),
			newAppEditorFormProperty("description", "Description", rule.Description, "textarea"),
			newChoiceAppEditorFormProperty("effect", "Effect", effect, appEditorSecurityEffectChoices(effect)),
			newChoiceAppEditorFormProperty("operation", "Operation", rule.Operation, appEditorSecurityOperationChoices(rule.Operation)),
			newAppEditorFormProperty("field", "Field", rule.Field, "text"),
			newAppEditorFormProperty("condition", "Condition", rule.Condition, "textarea"),
			newAppEditorFormProperty("role", "Role", rule.Role, "text"),
			newAppEditorFormProperty("order", "Order", fmt.Sprintf("%d", rule.Order), "text"),
		},
	}
}

func buildYAMLAppEditorRole(app appRegistryItem, role db.AppDefinitionRole) appEditorObject {
	label := strings.TrimSpace(role.Label)
	if label == "" {
		label = humanizeAppItemName(role.Name)
	}
	return appEditorObject{
		ID:          fmt.Sprintf("role:%s:%s", app.Name, role.Name),
		Kind:        "role",
		Name:        role.Name,
		Label:       label,
		Description: strings.TrimSpace(role.Description),
		Meta:        "role",
		Editable:    true,
		Published:   publishedDefinitionHasRole(app, role.Name),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("name", "Name", role.Name, "text"),
			newAppEditorFormProperty("label", "Label", role.Label, "text"),
			newAppEditorFormProperty("description", "Description", role.Description, "textarea"),
		},
	}
}

func buildYAMLAppEditorService(app appRegistryItem, service db.AppDefinitionService) (appEditorServiceGroup, []appEditorObject) {
	label := strings.TrimSpace(service.Label)
	if label == "" {
		label = humanizeAppItemName(service.Name)
	}
	serviceObject := appEditorObject{
		ID:          fmt.Sprintf("service:%s:%s", app.Name, service.Name),
		Kind:        "service",
		Name:        service.Name,
		Label:       label,
		Description: service.Description,
		Meta:        fmt.Sprintf("%d methods", len(service.Methods)),
		Editable:    true,
		Published:   func() bool { _, ok := publishedDefinitionService(app, service.Name); return ok }(),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("name", "Name", service.Name, "text"),
			newAppEditorFormProperty("label", "Label", service.Label, "text"),
			newAppEditorFormProperty("description", "Description", service.Description, "textarea"),
		},
	}

	methodObjects := make([]appEditorObject, 0, len(service.Methods))
	objects := []appEditorObject{serviceObject}
	for _, method := range service.Methods {
		methodLabel := strings.TrimSpace(method.Label)
		if methodLabel == "" {
			methodLabel = humanizeAppItemName(method.Name)
		}
		methodObject := appEditorObject{
			ID:          fmt.Sprintf("method:%s:%s:%s", app.Name, service.Name, method.Name),
			ParentID:    serviceObject.ID,
			Kind:        "method",
			Name:        method.Name,
			Label:       methodLabel,
			Description: method.Description,
			Meta:        method.Visibility,
			Editable:    true,
			Published:   publishedDefinitionHasMethod(app, service.Name, method.Name),
			Deletable:   true,
			FormProperties: []appEditorFormProperty{
				newReadOnlyAppEditorFormProperty("service_name", "Service", service.Name, "text"),
				newReadOnlyAppEditorFormProperty("name", "Name", method.Name, "text"),
				newAppEditorFormProperty("label", "Label", method.Label, "text"),
				newAppEditorFormProperty("description", "Description", method.Description, "textarea"),
				newAppEditorFormProperty("visibility", "Visibility", method.Visibility, "text"),
				newAppEditorFormProperty("language", "Language", method.Language, "text"),
				newAppEditorFormProperty("roles", "Roles", formatAppEditorJSON(method.Roles), "json"),
				newAppEditorFormProperty("script", "Script", method.Script, "textarea"),
			},
		}
		methodObjects = append(methodObjects, methodObject)
		objects = append(objects, methodObject)
	}

	return appEditorServiceGroup{
		Service: serviceObject,
		Methods: methodObjects,
	}, objects
}

func buildYAMLAppEditorTrigger(app appRegistryItem, trigger db.AppDefinitionTrigger) appEditorObject {
	label := strings.TrimSpace(trigger.Label)
	if label == "" {
		label = humanizeAppItemName(trigger.Name)
	}
	return appEditorObject{
		ID:          fmt.Sprintf("trigger:%s:%s", app.Name, trigger.Name),
		Kind:        "trigger",
		Name:        trigger.Name,
		Label:       label,
		Description: trigger.Description,
		Meta:        trigger.Event,
		Editable:    true,
		Published:   publishedDefinitionHasTrigger(app, trigger.Name),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("name", "Name", trigger.Name, "text"),
			newAppEditorFormProperty("label", "Label", trigger.Label, "text"),
			newAppEditorFormProperty("description", "Description", trigger.Description, "textarea"),
			newAppEditorFormProperty("event", "Event", trigger.Event, "text"),
			newAppEditorFormProperty("table", "Table", trigger.Table, "text"),
			newAppEditorFormProperty("condition", "Condition", trigger.Condition, "textarea"),
			newAppEditorFormProperty("call", "Call", trigger.Call, "text"),
			newAppEditorFormProperty("mode", "Mode", trigger.Mode, "text"),
			newAppEditorFormProperty("order", "Order", fmt.Sprintf("%d", trigger.Order), "text"),
			newAppEditorFormProperty("enabled", "Enabled", boolAppEditorValue(trigger.Enabled), "bool"),
		},
	}
}

func buildYAMLAppEditorSchedule(app appRegistryItem, schedule db.AppDefinitionSchedule) appEditorObject {
	label := strings.TrimSpace(schedule.Label)
	if label == "" {
		label = humanizeAppItemName(schedule.Name)
	}
	return appEditorObject{
		ID:          fmt.Sprintf("schedule:%s:%s", app.Name, schedule.Name),
		Kind:        "schedule",
		Name:        schedule.Name,
		Label:       label,
		Description: schedule.Description,
		Meta:        schedule.Cron,
		Editable:    true,
		Published:   publishedDefinitionHasSchedule(app, schedule.Name),
		Deletable:   true,
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("name", "Name", schedule.Name, "text"),
			newAppEditorFormProperty("label", "Label", schedule.Label, "text"),
			newAppEditorFormProperty("description", "Description", schedule.Description, "textarea"),
			newAppEditorFormProperty("cron", "Cron", schedule.Cron, "text"),
			newAppEditorFormProperty("call", "Call", schedule.Call, "text"),
			newAppEditorFormProperty("enabled", "Enabled", boolAppEditorValue(schedule.Enabled), "bool"),
		},
	}
}

func buildYAMLAppEditorSeed(app appRegistryItem, seed db.AppDefinitionSeed, index int) appEditorObject {
	return appEditorObject{
		ID:          fmt.Sprintf("seed:%s:%s:%d", app.Name, seed.Table, index),
		Kind:        "seed",
		Name:        seed.Table,
		Label:       humanizeAppItemName(seed.Table) + " Seed",
		Description: fmt.Sprintf("Seed rows for %s in the %s app.", seed.Table, app.Name),
		Meta:        fmt.Sprintf("%d rows", len(seed.Rows)),
		FormProperties: []appEditorFormProperty{
			newReadOnlyAppEditorFormProperty("table", "Table", seed.Table, "text"),
			newReadOnlyAppEditorFormProperty("row_count", "Row Count", fmt.Sprintf("%d", len(seed.Rows)), "text"),
			newAppEditorFormProperty("rows", "Rows", formatAppEditorJSON(seed.Rows), "json"),
		},
		Editable:  true,
		Published: publishedDefinitionHasSeed(app, seed.Table, index),
		Deletable: true,
	}
}

func appUsesYAMLDefinition(registry []appRegistryItem, tableName string) bool {
	for _, app := range registry {
		definition := app.DraftDefinition
		if definition == nil {
			definition = app.Definition
		}
		if definition == nil {
			continue
		}
		for _, table := range definition.Tables {
			if table.Name == tableName {
				return true
			}
		}
	}
	return false
}

func appHasYAMLDefinitionForScope(registry []appRegistryItem, scope string) bool {
	scope = strings.TrimSpace(strings.ToLower(scope))
	for _, app := range registry {
		definition := app.DraftDefinition
		if definition == nil {
			definition = app.Definition
		}
		if definition == nil {
			continue
		}
		if app.Name == scope || app.Namespace == scope {
			return true
		}
	}
	return false
}

func appHasYAMLDefinition(registry []appRegistryItem, appName string) bool {
	for _, app := range registry {
		if app.Name == appName {
			return app.DraftDefinition != nil || app.Definition != nil
		}
	}
	return false
}

func deriveAppForTable(tableName string, registry []appRegistryItem) (appName string, objectName string, include bool) {
	name := strings.TrimSpace(strings.ToLower(tableName))
	if name == "" || strings.HasPrefix(name, "_") {
		return "", "", false
	}

	for _, app := range registry {
		prefix := strings.TrimSpace(strings.ToLower(app.Namespace)) + "_"
		if prefix == "_" {
			continue
		}
		if strings.HasPrefix(name, prefix) {
			return app.Name, name, true
		}
	}

	return "", "", false
}

func listRegisteredApps(ctx context.Context) ([]appRegistryItem, error) {
	items, err := db.ListActiveApps(ctx)
	if err != nil {
		return nil, err
	}

	apps := make([]appRegistryItem, 0, len(items))
	for _, item := range items {
		label := item.Label
		description := item.Description
		if item.DraftDefinition != nil {
			if candidate := strings.TrimSpace(item.DraftDefinition.Label); candidate != "" {
				label = candidate
			}
			if candidate := strings.TrimSpace(item.DraftDefinition.Description); candidate != "" {
				description = candidate
			}
		}
		apps = append(apps, appRegistryItem{
			ID:                      item.ID,
			Name:                    item.Name,
			Namespace:               item.Namespace,
			Label:                   label,
			Description:             description,
			Status:                  item.Status,
			DefinitionYAML:          item.DefinitionYAML,
			PublishedDefinitionYAML: item.PublishedDefinitionYAML,
			DefinitionVersion:       item.DefinitionVersion,
			PublishedVersion:        item.PublishedVersion,
			Definition:              item.Definition,
			DraftDefinition:         item.DraftDefinition,
		})
	}
	return apps, nil
}

func deriveRegisteredAppsFromTables(tables []db.BuilderTableSummary) []appRegistryItem {
	appsByNamespace := map[string]appRegistryItem{}
	for _, table := range tables {
		name := strings.TrimSpace(strings.ToLower(table.Name))
		if name == "" || strings.HasPrefix(name, "_") {
			continue
		}

		namespace, _, found := strings.Cut(name, "_")
		if !found || namespace == "" {
			continue
		}

		if _, exists := appsByNamespace[namespace]; exists {
			continue
		}

		label := strings.ToUpper(namespace)
		if namespace != strings.ToLower(namespace) {
			label = namespace
		}

		appsByNamespace[namespace] = appRegistryItem{
			Name:        namespace,
			Namespace:   namespace,
			Label:       label,
			Description: fmt.Sprintf("%s application derived from namespaced tables", label),
			Status:      "active",
		}
	}

	apps := make([]appRegistryItem, 0, len(appsByNamespace))
	for _, app := range appsByNamespace {
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool {
		return apps[i].Label < apps[j].Label
	})
	return apps
}

func humanizeAppItemName(input string) string {
	parts := strings.Split(strings.ReplaceAll(input, "_", " "), " ")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func defaultAppEditorSecurityRules(tableName string) []appEditorSecurityRule {
	return []appEditorSecurityRule{
		{
			Name:        "Table Access",
			Description: fmt.Sprintf("Permissions for viewing and editing records in %s are currently inherited from platform roles.", tableName),
			Status:      "Inherited",
		},
		{
			Name:        "Row Conditions",
			Description: "No app-specific row-level conditions are configured yet.",
			Status:      "Not Configured",
		},
	}
}

func defaultAppEditorSecurityItems(tableName, parentID string) []appEditorObject {
	return []appEditorObject{
		{
			ID:           fmt.Sprintf("security:%s:access", tableName),
			ParentID:     parentID,
			Kind:         "security",
			Name:         "access",
			PhysicalName: tableName,
			Label:        "Access Rules",
			Description:  fmt.Sprintf("Role-based access for %s", tableName),
			Meta:         "Inherited",
			SecurityDetail: appEditorSecurityRule{
				Name:        "Access Rules",
				Description: fmt.Sprintf("Role-based access for %s is inherited from platform roles for now.", tableName),
				Status:      "Inherited",
			},
			FormProperties: []appEditorFormProperty{
				newReadOnlyAppEditorFormProperty("name", "Name", "Access Rules", "text"),
				newReadOnlyAppEditorFormProperty("table", "Table", tableName, "text"),
				newReadOnlyAppEditorFormProperty("status", "Status", "Inherited", "text"),
				newReadOnlyAppEditorFormProperty("description", "Description", fmt.Sprintf("Role-based access for %s is inherited from platform roles for now.", tableName), "textarea"),
			},
		},
		{
			ID:           fmt.Sprintf("security:%s:rows", tableName),
			ParentID:     parentID,
			Kind:         "security",
			Name:         "row_rules",
			PhysicalName: tableName,
			Label:        "Row Rules",
			Description:  fmt.Sprintf("Row-level conditions for %s", tableName),
			Meta:         "Not Configured",
			SecurityDetail: appEditorSecurityRule{
				Name:        "Row Rules",
				Description: fmt.Sprintf("No row-level conditions are configured yet for %s.", tableName),
				Status:      "Not Configured",
			},
			FormProperties: []appEditorFormProperty{
				newReadOnlyAppEditorFormProperty("name", "Name", "Row Rules", "text"),
				newReadOnlyAppEditorFormProperty("table", "Table", tableName, "text"),
				newReadOnlyAppEditorFormProperty("status", "Status", "Not Configured", "text"),
				newReadOnlyAppEditorFormProperty("description", "Description", fmt.Sprintf("No row-level conditions are configured yet for %s.", tableName), "textarea"),
			},
		},
	}
}

func findAppEditorObject(objects []appEditorObject, id string) (appEditorObject, bool) {
	for _, object := range objects {
		if object.ID == id {
			return object, true
		}
	}
	return appEditorObject{}, false
}

func buildNewAppEditorObject(r *http.Request, selectedApp appEditorAppSummary, explorerTables []appEditorExplorerTable, activeID string) (appEditorObject, bool) {
	switch strings.TrimSpace(activeID) {
	case "new:app":
		return appEditorObject{
			ID:          "new:app",
			Kind:        "application",
			Name:        "New Application",
			Label:       "New Application",
			Description: "Create a new app shell. Tables and columns remain draft-only until publish.",
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("namespace", "Namespace", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
			},
		}, true
	case "new:table":
		return appEditorObject{
			ID:          "new:table",
			Kind:        "table",
			Name:        "New Table",
			Label:       "New Table",
			Description: fmt.Sprintf("Create a new draft table for %s. Use the %s_ prefix. System columns like _id and _created_at are added automatically, and SQL is created on publish.", selectedApp.Label, selectedApp.Name),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("extends", "Extends", "", "text"),
				newAppEditorFormProperty("extensible", "Extensible", "false", "bool"),
				newAppEditorFormProperty("label_singular", "Label Singular", "", "text"),
				newAppEditorFormProperty("label_plural", "Label Plural", "", "text"),
				newAppEditorFormProperty("display_field", "Display Field", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
			},
		}, true
	case "new:column":
		tableName := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table")))
		description := "Create a new draft column. SQL is created on publish."
		if tableName != "" {
			description = fmt.Sprintf("Create a new draft column for %s. SQL is created on publish.", tableName)
		}
		return appEditorObject{
			ID:          "new:column",
			Kind:        "column",
			Name:        "New Column",
			Label:       "New Column",
			Description: description,
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newReadOnlyAppEditorFormProperty("table_name", "Table", tableName, "text"),
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newChoiceAppEditorFormProperty("data_type", "Data Type", "text", appEditorColumnTypeChoices("text")),
				newAppEditorFormProperty("is_nullable", "Is Nullable", "true", "bool"),
				newConditionalAppEditorFormProperty("prefix", "Prefix", "", "text", "data_type=autnumber"),
				newConditionalAppEditorFormProperty("reference_table", "Reference Table", "", "text", "data_type=reference"),
				newConditionalAppEditorFormProperty("choices", "Choices", "", "json", "data_type=choice"),
				newAppEditorFormProperty("default_value", "Default Value", "", "text"),
				newAppEditorFormProperty("validation_regex", "Validation Regex", "", "text"),
				newAppEditorFormProperty("condition_expr", "Condition Expression", "", "textarea"),
				newAppEditorFormProperty("validation_message", "Validation Message", "", "textarea"),
			},
		}, true
	case "new:form":
		tableName := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table")))
		description := fmt.Sprintf("Create a form for %s.", selectedApp.Label)
		tableProperty := newAppEditorFormProperty("table", "Table", tableName, "text")
		if tableName != "" {
			description = fmt.Sprintf("Create a form definition for %s.", tableName)
			tableProperty = newReadOnlyAppEditorFormProperty("table", "Table", tableName, "text")
		}
		return appEditorObject{
			ID:          "new:form",
			Kind:        "form",
			Name:        "New Form",
			Label:       "New Form",
			Description: description,
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
				tableProperty,
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
			},
		}, true
	case "new:data-policy":
		tableName := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table")))
		description := fmt.Sprintf("Create a data policy for %s.", selectedApp.Label)
		tableProperty := newAppEditorFormProperty("table_name", "Table", tableName, "text")
		if tableName != "" {
			description = fmt.Sprintf("Create a data policy on %s.", tableName)
			tableProperty = newReadOnlyAppEditorFormProperty("table_name", "Table", tableName, "text")
		}
		return appEditorObject{
			ID:          "new:data-policy",
			Kind:        "data_policy",
			Name:        "New Data Policy",
			Label:       "New Data Policy",
			Description: description,
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				tableProperty,
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
				newAppEditorFormProperty("condition", "Condition", "", "textarea"),
				newAppEditorFormProperty("action", "Action", "", "text"),
				newAppEditorFormProperty("enabled", "Enabled", "true", "bool"),
			},
		}, true
	case "new:table-trigger":
		tableName := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table")))
		description := fmt.Sprintf("Create a table trigger for %s.", selectedApp.Label)
		tableProperty := newAppEditorFormProperty("table_name", "Table", tableName, "text")
		if tableName != "" {
			description = fmt.Sprintf("Create a trigger declared directly on %s.", tableName)
			tableProperty = newReadOnlyAppEditorFormProperty("table_name", "Table", tableName, "text")
		}
		return appEditorObject{
			ID:          "new:table-trigger",
			Kind:        "table_trigger",
			Name:        "New Table Trigger",
			Label:       "New Table Trigger",
			Description: description,
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				tableProperty,
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
				newAppEditorFormProperty("event", "Event", "record.update", "text"),
				newAppEditorFormProperty("condition", "Condition", "", "textarea"),
				newAppEditorFormProperty("call", "Call", "", "text"),
				newAppEditorFormProperty("mode", "Mode", "async", "text"),
				newAppEditorFormProperty("order", "Order", "100", "text"),
				newAppEditorFormProperty("enabled", "Enabled", "true", "bool"),
			},
		}, true
	case "new:related-list":
		tableName := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table")))
		description := fmt.Sprintf("Create a related list for %s.", selectedApp.Label)
		tableProperty := newAppEditorFormProperty("table_name", "Table", tableName, "text")
		if tableName != "" {
			description = fmt.Sprintf("Create a related list on %s.", tableName)
			tableProperty = newReadOnlyAppEditorFormProperty("table_name", "Table", tableName, "text")
		}
		return appEditorObject{
			ID:          "new:related-list",
			Kind:        "related_list",
			Name:        "New Related List",
			Label:       "New Related List",
			Description: description,
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				tableProperty,
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("table", "Target Table", "", "text"),
				newAppEditorFormProperty("reference_field", "Reference Field", "", "text"),
				newAppEditorFormProperty("columns", "Columns", "[]", "json"),
			},
		}, true
	case "new:security-rule":
		tableName := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table")))
		description := fmt.Sprintf("Create a security rule for %s.", selectedApp.Label)
		tableProperty := newAppEditorFormProperty("table_name", "Table", tableName, "text")
		if tableName != "" {
			description = fmt.Sprintf("Create a security rule on %s.", tableName)
			tableProperty = newReadOnlyAppEditorFormProperty("table_name", "Table", tableName, "text")
		}
		return appEditorObject{
			ID:          "new:security-rule",
			Kind:        "security_rule",
			Name:        "New Security Rule",
			Label:       "New Security Rule",
			Description: description,
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				tableProperty,
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
				newChoiceAppEditorFormProperty("effect", "Effect", "allow", appEditorSecurityEffectChoices("allow")),
				newChoiceAppEditorFormProperty("operation", "Operation", "R", appEditorSecurityOperationChoices("R")),
				newAppEditorFormProperty("field", "Field", "", "text"),
				newAppEditorFormProperty("condition", "Condition", "", "textarea"),
				newAppEditorFormProperty("role", "Role", "", "text"),
				newAppEditorFormProperty("order", "Order", "100", "text"),
			},
		}, true
	case "new:dependency":
		return appEditorObject{
			ID:          "new:dependency",
			Kind:        "dependency",
			Name:        "New Dependency",
			Label:       "New Dependency",
			Description: fmt.Sprintf("Add an app dependency to %s.", selectedApp.Label),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
			},
		}, true
	case "new:page":
		return appEditorObject{
			ID:          "new:page",
			Kind:        "page",
			Name:        "New Page",
			Label:       "New Page",
			Description: fmt.Sprintf("Create a page asset for %s.", selectedApp.Label),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("title", "Title", "", "text"),
				newAppEditorFormProperty("search_keywords", "Search Keywords", "", "text"),
				newAppEditorFormProperty("content", "Contents", "<section><h1>New Page</h1><p>Start writing here.</p></section>", "page_wysiwyg"),
			},
		}, true
	case "new:client-script":
		return appEditorObject{
			ID:          "new:client-script",
			Kind:        "client_script",
			Name:        "New Client Script",
			Label:       "New Client Script",
			Description: fmt.Sprintf("Create a browser-side client script for %s.", selectedApp.Label),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
				newAppEditorFormProperty("table", "Table", strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table"))), "text"),
				newAppEditorFormProperty("event", "Event", "form.load", "text"),
				newAppEditorFormProperty("field", "Field", "", "text"),
				newAppEditorFormProperty("language", "Language", "javascript", "text"),
				newAppEditorFormProperty("enabled", "Enabled", "true", "bool"),
			},
		}, true
	case "new:role":
		return appEditorObject{
			ID:          "new:role",
			Kind:        "role",
			Name:        "New Role",
			Label:       "New Role",
			Description: fmt.Sprintf("Create a new app role for %s.", selectedApp.Label),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
			},
		}, true
	case "new:documentation":
		return appEditorObject{
			ID:          "new:documentation",
			Kind:        "documentation",
			Name:        "New Article",
			Label:       "New Article",
			Description: fmt.Sprintf("Create a documentation article for %s.", selectedApp.Label),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
			},
		}, true
	case "new:service":
		return appEditorObject{
			ID:          "new:service",
			Kind:        "service",
			Name:        "New Service",
			Label:       "New Service",
			Description: fmt.Sprintf("Create a new service namespace for %s.", selectedApp.Label),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
			},
		}, true
	case "new:endpoint":
		return appEditorObject{
			ID:          "new:endpoint",
			Kind:        "endpoint",
			Name:        "New Endpoint",
			Label:       "New Endpoint",
			Description: fmt.Sprintf("Create an HTTP endpoint for %s.", selectedApp.Label),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
				newAppEditorFormProperty("method", "Method", "POST", "text"),
				newAppEditorFormProperty("path", "Path", "", "text"),
				newAppEditorFormProperty("call", "Call", "", "text"),
				newAppEditorFormProperty("roles", "Roles", "", "json"),
				newAppEditorFormProperty("enabled", "Enabled", "true", "bool"),
			},
		}, true
	case "new:trigger":
		return appEditorObject{
			ID:          "new:trigger",
			Kind:        "trigger",
			Name:        "New Trigger",
			Label:       "New Trigger",
			Description: fmt.Sprintf("Create a new trigger for %s.", selectedApp.Label),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
				newAppEditorFormProperty("event", "Event", "record.update", "text"),
				newAppEditorFormProperty("table", "Table", "", "text"),
				newAppEditorFormProperty("condition", "Condition", "", "textarea"),
				newAppEditorFormProperty("call", "Call", "", "text"),
				newAppEditorFormProperty("mode", "Mode", "async", "text"),
				newAppEditorFormProperty("order", "Order", "100", "text"),
				newAppEditorFormProperty("enabled", "Enabled", "true", "bool"),
			},
		}, true
	case "new:schedule":
		return appEditorObject{
			ID:          "new:schedule",
			Kind:        "schedule",
			Name:        "New Schedule",
			Label:       "New Schedule",
			Description: fmt.Sprintf("Create a new schedule for %s.", selectedApp.Label),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
				newAppEditorFormProperty("cron", "Cron", "0 * * * *", "text"),
				newAppEditorFormProperty("call", "Call", "", "text"),
				newAppEditorFormProperty("enabled", "Enabled", "true", "bool"),
			},
		}, true
	case "new:method":
		return appEditorObject{
			ID:          "new:method",
			Kind:        "method",
			Name:        "New Method",
			Label:       "New Method",
			Description: fmt.Sprintf("Create a new method for %s.", selectedApp.Label),
			Editable:    true,
			FormProperties: []appEditorFormProperty{
				newAppEditorFormProperty("name", "Name", "", "text"),
				newAppEditorFormProperty("service_name", "Service", strings.TrimSpace(r.URL.Query().Get("service")), "text"),
				newAppEditorFormProperty("label", "Label", "", "text"),
				newAppEditorFormProperty("description", "Description", "", "textarea"),
				newAppEditorFormProperty("visibility", "Visibility", "private", "text"),
				newAppEditorFormProperty("language", "Language", "javascript", "text"),
				newAppEditorFormProperty("roles", "Roles", "", "json"),
				newAppEditorFormProperty("script", "Script", defaultJavaScriptBusinessScript, "textarea"),
			},
		}, true
	default:
		return appEditorObject{}, false
	}
}

func buildAppEditorObjectFormData(w http.ResponseWriter, r *http.Request, object appEditorObject) (map[string]any, bool) {
	if len(object.FormProperties) == 0 {
		return nil, false
	}

	columns := make([]db.Column, 0, len(object.FormProperties))
	configs := make(map[string]formFieldConfig, len(object.FormProperties))
	values := make(map[string]string, len(object.FormProperties))

	for _, property := range object.FormProperties {
		name := strings.TrimSpace(strings.ToLower(property.Name))
		if name == "" {
			continue
		}
		label := strings.TrimSpace(property.Label)
		if label == "" {
			label = humanizeAppItemName(name)
		}

		dataType := strings.TrimSpace(strings.ToLower(property.DataType))
		columnDataType := "text"
		cfg := formFieldConfig{
			Kind:          "text",
			InputType:     "text",
			ReadOnly:      property.ReadOnly,
			Placeholder:   label,
			ConditionExpr: strings.TrimSpace(property.ConditionExpr),
		}

		switch dataType {
		case "page_wysiwyg":
			columnDataType = "long_text"
			cfg.Kind = "page_wysiwyg"
			cfg.InputType = "textarea"
			values[name] = property.Value
		case "page_html":
			columnDataType = "long_text"
			cfg.Kind = "page_html"
			cfg.InputType = "textarea"
			values[name] = property.Value
		case "choice":
			columnDataType = "choice"
			cfg.Kind = "choice"
			cfg.InputType = "select"
			cfg.ReferenceRows = make([]formReferenceOption, 0, len(property.Choices))
			for _, choice := range property.Choices {
				value := strings.TrimSpace(choice.Value)
				if value == "" {
					continue
				}
				choiceLabel := strings.TrimSpace(choice.Label)
				if choiceLabel == "" {
					choiceLabel = value
				}
				cfg.ReferenceRows = append(cfg.ReferenceRows, formReferenceOption{
					Value: value,
					Label: choiceLabel,
				})
			}
			values[name] = property.Value
		case "bool", "boolean":
			columnDataType = "bool"
			cfg.Kind = "bool"
			cfg.InputType = "checkbox"
			values[name] = normalizeAppEditorBoolValue(property.Value)
		case "json", "jsonb":
			columnDataType = "jsonb"
			cfg.Kind = "json"
			cfg.InputType = "textarea"
			values[name] = property.Value
		case "markdown":
			columnDataType = "markdown"
			cfg.Kind = "markdown"
			cfg.InputType = "textarea"
			values[name] = property.Value
		case "textarea":
			cfg.Kind = "textarea"
			cfg.InputType = "textarea"
			values[name] = property.Value
		case "date":
			columnDataType = "date"
			cfg.Kind = "date"
			cfg.InputType = "date"
			values[name] = property.Value
		case "datetime", "timestamp":
			columnDataType = "timestamptz"
			cfg.Kind = "date"
			cfg.InputType = "datetime-local"
			values[name] = property.Value
		default:
			values[name] = property.Value
		}

		columns = append(columns, db.Column{
			NAME:          name,
			LABEL:         label,
			DATA_TYPE:     columnDataType,
			IS_NULLABLE:   true,
			IS_READONLY:   property.ReadOnly,
			TABLE_ID:      "app_editor",
			IS_HIDDEN:     false,
			CREATED_AT:    time.Time{},
			UPDATED_AT:    time.Time{},
			DEFAULT_VALUE: sql.NullString{},
		})
		configs[name] = cfg
	}

	viewData := newViewData(w, r, r.URL.Path, "Application Editor", "Admin")
	description := strings.TrimSpace(object.Description)
	if !object.Published && !strings.HasPrefix(object.ID, "new:") {
		if description != "" {
			description += " "
		}
		description += "Draft only. Publish the app to make this live."
	}
	viewData["FormTable"] = "app_editor"
	viewData["FormID"] = "yaml:" + object.ID
	viewData["FormTableLabel"] = object.Label
	viewData["FormTableDescription"] = description
	viewData["FormColumns"] = columns
	viewData["FormFieldConfigs"] = configs
	viewData["FormFieldValues"] = values
	viewData["FormReadOnly"] = !object.Editable
	viewData["FormIsDeleted"] = false
	viewData["FormTimelineCanComment"] = false
	viewData["FormTimelineItems"] = []formTimelineItem{}
	viewData["FormRelatedSections"] = []formRelatedSection{}
	viewData["FormHidePrimaryActions"] = !object.Editable
	viewData["FormHideDeleteActions"] = !object.Deletable
	viewData["FormHideSecondaryActions"] = true
	viewData["FormHideTimeline"] = true
	viewData["FormHideRelatedSections"] = object.Kind != "form" || strings.HasPrefix(object.ID, "new:")
	viewData["FormDisableRealtime"] = true
	viewData["FormScriptEditor"] = object.Kind == "script" || object.Kind == "method" || object.Kind == "script_code" || object.Kind == "client_script_code"
	if !viewData["FormHideRelatedSections"].(bool) {
		viewData["FormRelatedTitle"] = "Form Elements"
		viewData["FormRelatedDescription"] = "Columns in the form layout and table related lists appear together here."
		viewData["FormRelatedSections"] = buildAppEditorFormElementSections(r.Context(), strings.TrimSpace(strings.ToLower(r.URL.Query().Get("app"))), object)
	}
	formAction := "/api/app-editor/object/save"
	primaryActionLabel := "Save Draft"
	hiddenFields := []formHiddenField{
		{Name: "app_name", Value: strings.TrimSpace(strings.ToLower(r.URL.Query().Get("app")))},
		{Name: "object_id", Value: object.ID},
		{Name: "return_to", Value: r.URL.RequestURI()},
	}
	if object.ID == "new:app" {
		formAction = "/api/app-editor/app/create"
		primaryActionLabel = "Create App"
		hiddenFields = nil
	}
	viewData["FormAction"] = formAction
	viewData["FormPrimaryActionLabel"] = primaryActionLabel
	if object.Deletable {
		viewData["FormDeleteAction"] = "/api/app-editor/object/delete"
		viewData["FormDeleteLabel"] = "Delete"
		viewData["FormDeleteConfirm"] = appEditorDeleteConfirmMessage(object)
	}
	viewData["FormHiddenFields"] = hiddenFields
	return viewData, true
}

func buildAppEditorFormElementSections(ctx context.Context, appName string, object appEditorObject) []formRelatedSection {
	appName = strings.TrimSpace(strings.ToLower(appName))
	if appName == "" || !strings.HasPrefix(object.ID, "tableform:") {
		return nil
	}

	tableName, formName, ok := parseLegacyTableFormID(strings.TrimPrefix(object.ID, "tableform:"))
	if !ok {
		return nil
	}

	app, err := db.GetActiveAppByName(ctx, appName)
	if err != nil {
		return nil
	}
	definition := db.CloneAppDefinition(app.DraftDefinition)
	if definition == nil {
		definition = db.CloneAppDefinition(app.Definition)
	}
	if definition == nil {
		return nil
	}

	table, _, err := requireDefinitionTable(definition, tableName)
	if err != nil {
		return nil
	}

	var form *db.AppDefinitionForm
	for i := range table.Forms {
		name := strings.TrimSpace(strings.ToLower(table.Forms[i].Name))
		if name == "" {
			name = "default"
		}
		if name == formName {
			form = &table.Forms[i]
			break
		}
	}
	if form == nil {
		return nil
	}

	return buildAppEditorFormElementRows(ctx, app, tableName, *table, *form)
}

func buildAppEditorFormElementRows(ctx context.Context, app db.RegisteredApp, tableName string, table db.AppDefinitionTable, form db.AppDefinitionForm) []formRelatedSection {
	appName := strings.TrimSpace(strings.ToLower(app.Name))
	tableName = strings.TrimSpace(strings.ToLower(tableName))

	allColumns := db.BuildYAMLColumnsWithContext(ctx, app, table)
	columnLabels := make(map[string]string, len(allColumns))
	for _, column := range allColumns {
		label := strings.TrimSpace(column.LABEL)
		if label == "" {
			label = humanizeAppItemName(column.NAME)
		}
		columnLabels[column.NAME] = label
	}

	rows := make([]formRelatedRow, 0, allocHintSum(len(form.Fields), len(table.RelatedLists)))
	for _, field := range form.Fields {
		label := strings.TrimSpace(columnLabels[field])
		if label == "" {
			label = humanizeAppItemName(field)
		}
		rows = append(rows, formRelatedRow{
			ID:   "column:" + tableName + ":" + field,
			Href: "/admin/app-editor?app=" + url.QueryEscape(appName) + "&active=" + url.QueryEscape("column:"+tableName+":"+field),
			Cells: []formRelatedCell{
				{Label: "Element", Value: label},
				{Label: "Kind", Value: "Column"},
				{Label: "Target", Value: tableName + "." + field},
			},
		})
	}
	for _, related := range table.RelatedLists {
		label := strings.TrimSpace(related.Label)
		if label == "" {
			label = humanizeAppItemName(related.Name)
		}
		rows = append(rows, formRelatedRow{
			ID:   "related_list:" + tableName + ":" + related.Name,
			Href: "/admin/app-editor?app=" + url.QueryEscape(appName) + "&active=" + url.QueryEscape("table-related-list:"+tableName+":"+related.Name),
			Cells: []formRelatedCell{
				{Label: "Element", Value: label},
				{Label: "Kind", Value: "Related List"},
				{Label: "Target", Value: related.Table + " via " + related.ReferenceField},
			},
		})
	}
	if len(rows) == 0 {
		return nil
	}

	summary := fmt.Sprintf("%d form elements", len(rows))
	if len(rows) == 1 {
		summary = "1 form element"
	}
	summary += " from columns and related lists"

	return []formRelatedSection{{
		TableName:  tableName,
		TableLabel: "Form Elements",
		Summary:    summary,
		Count:      len(rows),
		Rows:       rows,
	}}
}

func appEditorRegisteredApp(app appRegistryItem) db.RegisteredApp {
	return db.RegisteredApp{
		ID:                      app.ID,
		Name:                    app.Name,
		Namespace:               app.Namespace,
		Label:                   app.Label,
		Description:             app.Description,
		Status:                  app.Status,
		DefinitionYAML:          app.DefinitionYAML,
		PublishedDefinitionYAML: app.PublishedDefinitionYAML,
		DefinitionVersion:       app.DefinitionVersion,
		PublishedVersion:        app.PublishedVersion,
		Definition:              app.Definition,
		DraftDefinition:         app.DraftDefinition,
	}
}

func appEditorDeleteConfirmMessage(object appEditorObject) string {
	kind := strings.TrimSpace(strings.ToLower(object.Kind))
	if kind != "" {
		kind = strings.ToLower(humanizeAppItemName(kind))
	}
	label := strings.TrimSpace(object.Label)
	if label == "" {
		label = strings.TrimSpace(object.Name)
	}
	if label == "" {
		label = "this item"
	}
	return fmt.Sprintf("Delete this %s from the app draft: %s?", kind, label)
}

func newAppEditorFormProperty(name, label, value, dataType string) appEditorFormProperty {
	return appEditorFormProperty{
		Name:     strings.TrimSpace(name),
		Label:    strings.TrimSpace(label),
		Value:    value,
		DataType: strings.TrimSpace(strings.ToLower(dataType)),
	}
}

func newConditionalAppEditorFormProperty(name, label, value, dataType, conditionExpr string) appEditorFormProperty {
	item := newAppEditorFormProperty(name, label, value, dataType)
	item.ConditionExpr = strings.TrimSpace(conditionExpr)
	return item
}

func newChoiceAppEditorFormProperty(name, label, value string, choices []db.ChoiceOption) appEditorFormProperty {
	item := newAppEditorFormProperty(name, label, value, "choice")
	item.Choices = append([]db.ChoiceOption(nil), choices...)
	return item
}

func newReadOnlyAppEditorFormProperty(name, label, value, dataType string) appEditorFormProperty {
	item := newAppEditorFormProperty(name, label, value, dataType)
	item.ReadOnly = true
	return item
}

func appEditorColumnTypeChoices(current string) []db.ChoiceOption {
	options := []db.ChoiceOption{
		{Value: "text", Label: "Text"},
		{Value: "varchar(255)", Label: "Short Text"},
		{Value: "long_text", Label: "Long Text"},
		{Value: "markdown", Label: "Markdown"},
		{Value: "int", Label: "Integer"},
		{Value: "float", Label: "Float"},
		{Value: "decimal", Label: "Decimal"},
		{Value: "bool", Label: "True/False"},
		{Value: "date", Label: "Date"},
		{Value: "timestamp", Label: "Date/Time"},
		{Value: "uuid", Label: "UUID"},
		{Value: "reference", Label: "Reference"},
		{Value: "choice", Label: "Choice"},
		{Value: "email", Label: "Email"},
		{Value: "url", Label: "URL"},
		{Value: "phone", Label: "Phone"},
		{Value: "jsonb", Label: "JSON"},
		{Value: "autnumber", Label: "Autnumber"},
	}
	current = strings.TrimSpace(strings.ToLower(current))
	if current == "" {
		return options
	}
	for _, option := range options {
		if option.Value == current {
			return options
		}
	}
	return append(options, db.ChoiceOption{
		Value: current,
		Label: current + " (custom)",
	})
}

func appEditorDisplayFieldChoices(columns []db.Column, current string) []db.ChoiceOption {
	choices := []db.ChoiceOption{{
		Value: "",
		Label: "Auto",
	}}
	for _, column := range columns {
		if column.IS_HIDDEN && !strings.EqualFold(column.NAME, "_id") {
			continue
		}
		label := strings.TrimSpace(column.LABEL)
		if label == "" {
			label = humanizeAppItemName(column.NAME)
		}
		choices = append(choices, db.ChoiceOption{
			Value: column.NAME,
			Label: label,
		})
	}

	current = strings.TrimSpace(strings.ToLower(current))
	for _, choice := range choices {
		if choice.Value == current {
			return choices
		}
	}
	if current != "" {
		choices = append(choices, db.ChoiceOption{
			Value: current,
			Label: current + " (custom)",
		})
	}
	return choices
}

func appEditorSecurityOperationChoices(current string) []db.ChoiceOption {
	current = strings.TrimSpace(strings.ToUpper(current))
	choices := []db.ChoiceOption{
		{Value: "C", Label: "Create"},
		{Value: "R", Label: "Read"},
		{Value: "U", Label: "Update"},
		{Value: "D", Label: "Delete"},
	}
	for _, choice := range choices {
		if choice.Value == current {
			return choices
		}
	}
	if current != "" {
		choices = append(choices, db.ChoiceOption{
			Value: current,
			Label: current + " (custom)",
		})
	}
	return choices
}

func appEditorSecurityEffectChoices(current string) []db.ChoiceOption {
	current = securityRuleEffectValue(current)
	choices := []db.ChoiceOption{
		{Value: "allow", Label: "Allow"},
		{Value: "deny", Label: "Deny"},
	}
	for _, choice := range choices {
		if choice.Value == current {
			return choices
		}
	}
	if current != "" {
		choices = append(choices, db.ChoiceOption{
			Value: current,
			Label: current + " (custom)",
		})
	}
	return choices
}

func boolAppEditorValue(value bool) string {
	if value {
		return "true"
	}
	return "false"
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

func securityOperationLabel(operation string) string {
	switch strings.TrimSpace(strings.ToUpper(operation)) {
	case "C":
		return "Create"
	case "R":
		return "Read"
	case "U":
		return "Update"
	case "D":
		return "Delete"
	default:
		return "Access"
	}
}

func securityRuleEffectValue(effect string) string {
	effect = strings.TrimSpace(strings.ToLower(effect))
	if effect == "" {
		return "allow"
	}
	return effect
}

func securityEffectLabel(effect string) string {
	switch securityRuleEffectValue(effect) {
	case "deny":
		return "Deny"
	default:
		return "Allow"
	}
}

func securityRuleMeta(rule db.AppDefinitionSecurityRule) string {
	parts := []string{securityEffectLabel(rule.Effect), securityOperationLabel(rule.Operation), strings.TrimSpace(rule.Role)}
	if field := strings.TrimSpace(rule.Field); field != "" {
		parts = append(parts, field)
	}
	parts = append(parts, fmt.Sprintf("order %d", rule.Order))
	return strings.Join(parts, " · ")
}

func securitySummary(security db.AppDefinitionSecurity) string {
	if len(security.Rules) > 0 {
		if len(security.Rules) == 1 {
			return "1 rule"
		}
		return fmt.Sprintf("%d rules", len(security.Rules))
	}
	if len(security.Roles) == 0 {
		return "Open"
	}
	return fmt.Sprintf("%d roles", len(security.Roles))
}

func normalizeAppEditorBoolValue(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "1", "true", "yes", "on":
		return "true"
	default:
		return "false"
	}
}

func formatAppEditorJSON(value any) string {
	if value == nil {
		return ""
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(content)
}

func formatAppEditorYAML(value any) string {
	content, err := yaml.Marshal(value)
	if err != nil {
		return ""
	}
	return string(content)
}

func handleSaveRootRouteTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	target := normalizeRootRouteTarget(strings.TrimSpace(strings.ToLower(r.FormValue("root_route_target"))))
	if target == "" {
		http.Error(w, "Invalid root route target", http.StatusBadRequest)
		return
	}

	_, err := db.Pool.Exec(context.Background(), `
		INSERT INTO _property (key, value)
		VALUES ('root_route_target', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, target)
	if err != nil {
		http.Error(w, "Failed to save root route target", http.StatusInternalServerError)
		return
	}

	_ = loadPropertiesFromDB()
	http.Redirect(w, r, "/admin/app-editor?root_saved=1", http.StatusSeeOther)
}

func normalizeRootRouteTarget(input string) string {
	target := strings.TrimSpace(strings.ToLower(input))
	switch {
	case target == "login" || target == "/login" || target == "/login/":
		return "login"
	case strings.HasPrefix(target, "page:"):
		slug := normalizePageRouteSlug(strings.TrimSpace(strings.TrimPrefix(target, "page:")))
		if slug != "" {
			return "page:" + slug
		}
	case strings.HasPrefix(target, "/p/"):
		slug := normalizePageRouteSlug(strings.TrimSpace(strings.TrimPrefix(target, "/p/")))
		if slug != "" {
			return "page:" + slug
		}
	case strings.HasPrefix(target, "table:"):
		tableName := strings.TrimSpace(strings.TrimPrefix(target, "table:"))
		if db.IsSafeIdentifier(tableName) {
			return "table:" + tableName
		}
	case strings.HasPrefix(target, "/t/"):
		tableName := strings.TrimSpace(strings.TrimPrefix(target, "/t/"))
		if db.IsSafeIdentifier(tableName) {
			return "table:" + tableName
		}
	}
	return ""
}

func handleSavePageBuilder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(strings.ToLower(r.FormValue("slug")))
	content := strings.TrimSpace(r.FormValue("content"))
	editorMode := strings.TrimSpace(strings.ToLower(r.FormValue("editor_mode")))
	status := strings.TrimSpace(strings.ToLower(r.FormValue("status")))
	userID := auth.UserIDFromRequest(r)

	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if !pageSlugPattern.MatchString(slug) {
		http.Error(w, "invalid slug (use lowercase letters, numbers, hyphens)", http.StatusBadRequest)
		return
	}
	if editorMode != "wysiwyg" && editorMode != "html" {
		editorMode = "wysiwyg"
	}
	if editorMode == "html" {
		allowed, err := db.UserHasGlobalPermission(r.Context(), strings.TrimSpace(userID), "admin")
		if err != nil {
			http.Error(w, "Failed to validate permissions", http.StatusInternalServerError)
			return
		}
		if !allowed {
			http.Error(w, "Raw HTML mode requires admin permission", http.StatusForbidden)
			return
		}
	}
	if status != "published" {
		status = "draft"
	}

	if err := upsertPageForBuilder(name, slug, content, editorMode, status, userID); err != nil {
		http.Error(w, "Failed to save page", http.StatusInternalServerError)
		return
	}

	_ = loadPropertiesFromDB()
	http.Redirect(w, r, "/admin/app-editor?slug="+slug+"&saved=1", http.StatusSeeOther)
}

func listPagesForBuilder() ([]pageBuilderItem, error) {
	rows, err := db.Pool.Query(context.Background(), `
		SELECT name, slug, COALESCE(editor_mode, 'wysiwyg'), content, COALESCE(status, 'draft')
		FROM _page
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pages := make([]pageBuilderItem, 0, 32)
	for rows.Next() {
		var p pageBuilderItem
		if err := rows.Scan(&p.Name, &p.Slug, &p.EditorMode, &p.Content, &p.Status); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pages, nil
}

func upsertPageForBuilder(name, slug, content, editorMode, status, userID string) error {
	tx, err := db.Pool.Begin(context.Background())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var count int
	if err := tx.QueryRow(context.Background(), `SELECT COUNT(1) FROM _page WHERE slug = $1`, slug).Scan(&count); err != nil {
		return err
	}

	if count == 0 {
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO _page (name, slug, content, editor_mode, status, published_at)
			VALUES ($1, $2, $3, $4, $5, CASE WHEN $5 = 'published' THEN NOW() ELSE NULL END)
		`, name, slug, content, editorMode, status); err != nil {
			return fmt.Errorf("insert page: %w", err)
		}
	} else {
		if _, err := tx.Exec(context.Background(), `
			UPDATE _page
			SET name = $2, content = $3, editor_mode = $4, status = $5,
			    published_at = CASE WHEN $5 = 'published' THEN NOW() ELSE published_at END
			WHERE slug = $1
		`, slug, name, content, editorMode, status); err != nil {
			return fmt.Errorf("update page: %w", err)
		}
	}

	var nextVersion int
	if err := tx.QueryRow(context.Background(), `SELECT COALESCE(MAX(version_num), 0) + 1 FROM _page_version WHERE page_slug = $1`, slug).Scan(&nextVersion); err == nil {
		_, _ = tx.Exec(context.Background(), `
			INSERT INTO _page_version (page_slug, version_num, name, content, editor_mode, status, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		`, slug, nextVersion, name, content, editorMode, status, strings.TrimSpace(userID))
	}

	if err := tx.Commit(context.Background()); err != nil {
		return err
	}
	return nil
}

func listPageVersionsForBuilder(slug string) ([]pageVersionItem, error) {
	rows, err := db.Pool.Query(context.Background(), `
		SELECT version_num, status, editor_mode, COALESCE(created_by, ''), _created_at::text
		FROM _page_version
		WHERE page_slug = $1
		ORDER BY version_num DESC
		LIMIT 30
	`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := make([]pageVersionItem, 0, 30)
	for rows.Next() {
		var v pageVersionItem
		if err := rows.Scan(&v.VersionNum, &v.Status, &v.EditorMode, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return versions, nil
}
