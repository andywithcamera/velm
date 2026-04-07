package db

import (
	"context"
	"strings"
)

func TaskTypeValueForTable(ctx context.Context, tableName string) string {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" || !IsSafeIdentifier(tableName) {
		return ""
	}
	if tableName == "base_task" {
		return "TASK"
	}

	app, table, ok, err := FindYAMLTableByName(ctx, tableName)
	if err != nil || !ok || app.Definition == nil {
		return ""
	}
	if !taskDefinitionExtendsBaseTask(ctx, app, table) {
		return ""
	}

	label := strings.TrimSpace(table.LabelSingular)
	if label == "" {
		label = scriptScopeAliasForTable(app, table.Name)
	}
	return normalizeTaskTypeValue(label)
}

func taskDefinitionExtendsBaseTask(ctx context.Context, app RegisteredApp, table AppDefinitionTable) bool {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return false
	}
	return taskDefinitionExtendsTarget(apps, app, table, "base_task", map[string]bool{})
}

func taskDefinitionExtendsTarget(apps []RegisteredApp, ownerApp RegisteredApp, table AppDefinitionTable, target string, visited map[string]bool) bool {
	name := strings.TrimSpace(strings.ToLower(table.Name))
	target = strings.TrimSpace(strings.ToLower(target))
	if name == target {
		return true
	}
	if name == "" || visited[name] {
		return false
	}
	visited[name] = true
	if strings.TrimSpace(table.Extends) == "" || ownerApp.Definition == nil {
		return false
	}

	dependencyApps := make(map[string]RegisteredApp, len(ownerApp.Definition.Dependencies))
	for _, dependency := range ownerApp.Definition.Dependencies {
		dependencyApp, ok := findRegisteredAppByNameOrNamespace(apps, dependency)
		if ok {
			dependencyApps[dependencyApp.Name] = dependencyApp
		}
	}

	parentApp, parent, ok := resolveValidationTable(ownerApp, ownerApp.Definition, dependencyApps, table.Extends)
	if !ok {
		return false
	}
	return taskDefinitionExtendsTarget(apps, parentApp, parent, target, visited)
}

func normalizeTaskTypeValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(value)), " ")
	return strings.ToUpper(value)
}
