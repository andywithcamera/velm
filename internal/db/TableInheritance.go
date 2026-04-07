package db

import (
	"context"
	"sort"
	"strings"
)

func ListTableQuerySources(ctx context.Context, tableName string) []string {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" || !IsSafeIdentifier(tableName) {
		return nil
	}

	sources := []string{tableName}
	if Pool == nil {
		return sources
	}

	apps, err := ListActiveApps(ctx)
	if err != nil {
		return sources
	}

	for _, descendant := range descendantTableNamesFromApps(apps, tableName) {
		if GetTable(descendant).ID == "" {
			continue
		}
		sources = append(sources, descendant)
	}

	return sources
}

func descendantTableNamesFromApps(apps []RegisteredApp, target string) []string {
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return nil
	}

	descendants := make([]string, 0, 8)
	seen := map[string]bool{target: true}
	for _, app := range apps {
		definition := effectiveRegisteredAppDefinition(app)
		if definition == nil {
			continue
		}

		ownerApp := app
		ownerApp.Definition = definition
		for _, table := range definition.Tables {
			name := strings.TrimSpace(strings.ToLower(table.Name))
			if name == "" || seen[name] {
				continue
			}
			if !definitionTableExtendsTargetWithApps(apps, ownerApp, table, target, map[string]bool{}) {
				continue
			}
			seen[name] = true
			descendants = append(descendants, name)
		}
	}

	sort.Strings(descendants)
	return descendants
}
