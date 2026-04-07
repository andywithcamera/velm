package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type ScriptScope struct {
	CurrentApp     ScriptScopeApp      `json:"current_app"`
	DependencyApps []ScriptScopeApp    `json:"dependency_apps"`
	Objects        []ScriptScopeObject `json:"objects"`
}

type ScriptScopeApp struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Label     string `json:"label"`
}

type ScriptScopeObject struct {
	App       ScriptScopeApp      `json:"app"`
	TableName string              `json:"table_name"`
	Alias     string              `json:"alias"`
	Path      string              `json:"path"`
	Label     string              `json:"label"`
	Columns   []ScriptScopeColumn `json:"columns"`
}

type ScriptScopeColumn struct {
	Name           string `json:"name"`
	Label          string `json:"label"`
	Path           string `json:"path"`
	DataType       string `json:"data_type,omitempty"`
	IsNullable     bool   `json:"is_nullable"`
	ReferenceTable string `json:"reference_table,omitempty"`
	ReadOnly       bool   `json:"read_only,omitempty"`
	System         bool   `json:"system,omitempty"`
}

type ScriptResolvedReference struct {
	App        ScriptScopeApp `json:"app"`
	TableName  string         `json:"table_name"`
	TableAlias string         `json:"table_alias"`
	ColumnName string         `json:"column_name"`
	Path       string         `json:"path"`
}

func GetScriptScope(ctx context.Context, appScope string) (ScriptScope, error) {
	appScope = strings.TrimSpace(strings.ToLower(appScope))
	if appScope == "" || appScope == "global" {
		return ScriptScope{}, nil
	}

	apps, err := ListActiveApps(ctx)
	if err != nil {
		return ScriptScope{}, fmt.Errorf("list active apps: %w", err)
	}

	currentApp, dependencyApps, err := resolveScriptScopeApps(apps, appScope)
	if err != nil {
		return ScriptScope{}, err
	}

	allTables, err := ListBuilderTables(ctx)
	if err != nil {
		return ScriptScope{}, fmt.Errorf("list builder tables: %w", err)
	}

	objects := make([]ScriptScopeObject, 0, 16)
	localObjects, err := listScriptScopeObjectsForApp(ctx, currentApp, false, allTables)
	if err != nil {
		return ScriptScope{}, err
	}
	objects = append(objects, localObjects...)

	dependencyScopeApps := make([]ScriptScopeApp, 0, len(dependencyApps))
	for _, dependencyApp := range dependencyApps {
		dependencyScopeApps = append(dependencyScopeApps, scriptScopeAppMeta(dependencyApp))

		dependencyObjects, err := listScriptScopeObjectsForApp(ctx, dependencyApp, true, allTables)
		if err != nil {
			return ScriptScope{}, err
		}
		objects = append(objects, dependencyObjects...)
	}

	sort.Slice(objects, func(i, j int) bool {
		if objects[i].Path == objects[j].Path {
			return objects[i].TableName < objects[j].TableName
		}
		return objects[i].Path < objects[j].Path
	})

	return ScriptScope{
		CurrentApp:     scriptScopeAppMeta(currentApp),
		DependencyApps: dependencyScopeApps,
		Objects:        objects,
	}, nil
}

func ResolveScriptScopePath(scope ScriptScope, rawPath string) (ScriptResolvedReference, error) {
	path := strings.TrimSpace(strings.ToLower(rawPath))
	if path == "" {
		return ScriptResolvedReference{}, fmt.Errorf("script path is required")
	}

	parts := strings.Split(path, ".")
	switch len(parts) {
	case 1:
		object, ok := scriptScopeCurrentObject(scope, parts[0])
		if !ok {
			return ScriptResolvedReference{}, fmt.Errorf("table %q is not available in app %q", parts[0], scope.CurrentApp.Name)
		}
		return ScriptResolvedReference{
			App:        object.App,
			TableName:  object.TableName,
			TableAlias: object.Alias,
			Path:       object.Path,
		}, nil
	case 2:
		if dependencyApp, ok := scriptScopeDependencyApp(scope, parts[0]); ok {
			object, found := scriptScopeDependencyObject(scope, dependencyApp.Name, parts[1])
			if !found {
				return ScriptResolvedReference{}, fmt.Errorf("table %q is not available in dependency app %q", parts[1], dependencyApp.Name)
			}
			return ScriptResolvedReference{
				App:        object.App,
				TableName:  object.TableName,
				TableAlias: object.Alias,
				Path:       object.Path,
			}, nil
		}

		object, ok := scriptScopeCurrentObject(scope, parts[0])
		if !ok {
			return ScriptResolvedReference{}, fmt.Errorf("table %q is not available in app %q", parts[0], scope.CurrentApp.Name)
		}
		column, found := scriptScopeColumn(object, parts[1])
		if !found {
			return ScriptResolvedReference{}, fmt.Errorf("column %q is not available on %q", parts[1], object.Path)
		}
		return ScriptResolvedReference{
			App:        object.App,
			TableName:  object.TableName,
			TableAlias: object.Alias,
			ColumnName: column.Name,
			Path:       column.Path,
		}, nil
	case 3:
		dependencyApp, ok := scriptScopeDependencyApp(scope, parts[0])
		if !ok {
			return ScriptResolvedReference{}, fmt.Errorf("app %q is not available in script scope", parts[0])
		}

		object, found := scriptScopeDependencyObject(scope, dependencyApp.Name, parts[1])
		if !found {
			return ScriptResolvedReference{}, fmt.Errorf("table %q is not available in dependency app %q", parts[1], dependencyApp.Name)
		}
		column, found := scriptScopeColumn(object, parts[2])
		if !found {
			return ScriptResolvedReference{}, fmt.Errorf("column %q is not available on %q", parts[2], object.Path)
		}
		return ScriptResolvedReference{
			App:        object.App,
			TableName:  object.TableName,
			TableAlias: object.Alias,
			ColumnName: column.Name,
			Path:       column.Path,
		}, nil
	default:
		return ScriptResolvedReference{}, fmt.Errorf("unsupported script path %q", rawPath)
	}
}

func resolveScriptScopeApps(apps []RegisteredApp, appScope string) (RegisteredApp, []RegisteredApp, error) {
	currentApp, ok := findRegisteredAppByNameOrNamespace(apps, appScope)
	if !ok {
		return RegisteredApp{}, nil, fmt.Errorf("app %q not found", appScope)
	}

	definition := runtimeDefinitionForApp(currentApp)
	if definition == nil || len(definition.Dependencies) == 0 {
		return currentApp, nil, nil
	}

	dependencies := make([]RegisteredApp, 0, len(definition.Dependencies))
	for _, dependency := range definition.Dependencies {
		dependencyApp, ok := findRegisteredAppByNameOrNamespace(apps, dependency)
		if !ok {
			return RegisteredApp{}, nil, fmt.Errorf("dependency app %q not found for app %q", dependency, currentApp.Name)
		}
		dependencies = append(dependencies, dependencyApp)
	}

	return currentApp, dependencies, nil
}

func findRegisteredAppByNameOrNamespace(apps []RegisteredApp, rawName string) (RegisteredApp, bool) {
	if app, ok := findExactRegisteredAppByNameOrNamespace(apps, rawName); ok {
		return app, true
	}

	name := normalizeIdentifier(rawName)
	if name == "system" {
		for _, app := range apps {
			if IsOOTBBaseApp(app) {
				return app, true
			}
		}
	}
	return RegisteredApp{}, false
}

func listScriptScopeObjectsForApp(ctx context.Context, app RegisteredApp, dependency bool, allTables []BuilderTableSummary) ([]ScriptScopeObject, error) {
	if definition := runtimeDefinitionForApp(app); definition != nil {
		objects := make([]ScriptScopeObject, 0, len(definition.Tables))
		for _, table := range definition.Tables {
			alias := scriptScopeAliasForTable(app, table.Name)
			path := alias
			if dependency {
				path = scriptScopeAppPathName(scriptScopeAppMeta(app)) + "." + alias
			}

			definitionColumns := BuildYAMLColumnsWithContext(ctx, app, table)
			columns := make([]ScriptScopeColumn, 0, len(definitionColumns))
			for _, column := range definitionColumns {
				columns = append(columns, ScriptScopeColumn{
					Name:           column.NAME,
					Label:          column.LABEL,
					Path:           path + "." + column.NAME,
					DataType:       strings.TrimSpace(column.DATA_TYPE),
					IsNullable:     column.IS_NULLABLE,
					ReferenceTable: strings.TrimSpace(column.REFERENCE_TABLE.String),
					ReadOnly:       column.IS_READONLY || strings.HasPrefix(column.NAME, "_"),
					System:         strings.HasPrefix(column.NAME, "_"),
				})
			}
			sort.Slice(columns, func(i, j int) bool {
				return columns[i].Name < columns[j].Name
			})

			objects = append(objects, ScriptScopeObject{
				App:       scriptScopeAppMeta(app),
				TableName: table.Name,
				Alias:     alias,
				Path:      path,
				Label:     table.LabelSingular,
				Columns:   columns,
			})
		}
		return objects, nil
	}

	objects := make([]ScriptScopeObject, 0, 8)
	for _, table := range allTables {
		if !scriptScopeTableBelongsToApp(app, table.Name) {
			continue
		}

		columns, err := ListBuilderColumns(ctx, table.Name)
		if err != nil {
			return nil, fmt.Errorf("list builder columns for %s: %w", table.Name, err)
		}

		alias := scriptScopeAliasForTable(app, table.Name)
		path := alias
		if dependency {
			path = scriptScopeRegisteredAppPathName(app) + "." + alias
		}

		scopeColumns := make([]ScriptScopeColumn, 0, len(columns))
		for _, column := range columns {
			scopeColumns = append(scopeColumns, ScriptScopeColumn{
				Name:           column.Name,
				Label:          column.Label,
				Path:           path + "." + column.Name,
				DataType:       strings.TrimSpace(column.DataType),
				IsNullable:     column.IsNullable,
				ReferenceTable: strings.TrimSpace(column.ReferenceTable),
				ReadOnly:       strings.HasPrefix(column.Name, "_"),
				System:         strings.HasPrefix(column.Name, "_"),
			})
		}
		sort.Slice(scopeColumns, func(i, j int) bool {
			return scopeColumns[i].Name < scopeColumns[j].Name
		})

		objects = append(objects, ScriptScopeObject{
			App:       scriptScopeAppMeta(app),
			TableName: table.Name,
			Alias:     alias,
			Path:      path,
			Label:     table.LabelSingular,
			Columns:   scopeColumns,
		})
	}
	return objects, nil
}

func scriptScopeAppMeta(app RegisteredApp) ScriptScopeApp {
	return ScriptScopeApp{
		Name:      strings.TrimSpace(strings.ToLower(app.Name)),
		Namespace: strings.TrimSpace(strings.ToLower(app.Namespace)),
		Label:     strings.TrimSpace(app.Label),
	}
}

func scriptScopeAppPathName(app ScriptScopeApp) string {
	if namespace := strings.TrimSpace(strings.ToLower(app.Namespace)); namespace != "" {
		return namespace
	}
	return strings.TrimSpace(strings.ToLower(app.Name))
}

func scriptScopeRegisteredAppPathName(app RegisteredApp) string {
	if namespace := strings.TrimSpace(strings.ToLower(app.Namespace)); namespace != "" {
		return namespace
	}
	return strings.TrimSpace(strings.ToLower(app.Name))
}

func scriptScopeTableBelongsToApp(app RegisteredApp, tableName string) bool {
	name := strings.TrimSpace(strings.ToLower(tableName))
	if name == "" {
		return false
	}

	prefixes := make([]string, 0, 2)
	if namespace := strings.TrimSpace(strings.ToLower(app.Namespace)); namespace != "" {
		prefixes = append(prefixes, namespace+"_")
	}
	if appName := strings.TrimSpace(strings.ToLower(app.Name)); appName != "" && appName != strings.TrimSpace(strings.ToLower(app.Namespace)) {
		prefixes = append(prefixes, appName+"_")
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}

func scriptScopeAliasForTable(app RegisteredApp, tableName string) string {
	name := strings.TrimSpace(strings.ToLower(tableName))
	if name == "" {
		return ""
	}

	prefixes := make([]string, 0, 2)
	if namespace := strings.TrimSpace(strings.ToLower(app.Namespace)); namespace != "" {
		prefixes = append(prefixes, namespace+"_")
	}
	if appName := strings.TrimSpace(strings.ToLower(app.Name)); appName != "" && appName != strings.TrimSpace(strings.ToLower(app.Namespace)) {
		prefixes = append(prefixes, appName+"_")
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			if alias := strings.TrimPrefix(name, prefix); alias != "" {
				return alias
			}
		}
	}

	if strings.HasPrefix(name, "_") && len(name) > 1 {
		return strings.TrimPrefix(name, "_")
	}
	return name
}

func scriptScopeCurrentObject(scope ScriptScope, alias string) (ScriptScopeObject, bool) {
	for _, object := range scope.Objects {
		if object.App.Name != scope.CurrentApp.Name {
			continue
		}
		if object.Alias == alias {
			return object, true
		}
	}
	return ScriptScopeObject{}, false
}

func scriptScopeDependencyApp(scope ScriptScope, name string) (ScriptScopeApp, bool) {
	name = strings.TrimSpace(strings.ToLower(name))
	for _, app := range scope.DependencyApps {
		if app.Name == name || app.Namespace == name || scriptScopeAppPathName(app) == name {
			return app, true
		}
	}
	return ScriptScopeApp{}, false
}

func scriptScopeDependencyObject(scope ScriptScope, appName, alias string) (ScriptScopeObject, bool) {
	for _, object := range scope.Objects {
		if object.App.Name != appName {
			continue
		}
		if object.Alias == alias {
			return object, true
		}
	}
	return ScriptScopeObject{}, false
}

func scriptScopeColumn(object ScriptScopeObject, name string) (ScriptScopeColumn, bool) {
	for _, column := range object.Columns {
		if column.Name == name {
			return column, true
		}
	}
	return ScriptScopeColumn{}, false
}
