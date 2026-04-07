package db

import (
	"context"
	"sort"
	"strings"
)

func ResolveRuntimeAppByTable(ctx context.Context, tableName string) (RegisteredApp, bool, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return RegisteredApp{}, false, err
	}
	app, ok := resolveRuntimeAppByTableWithApps(apps, tableName)
	return app, ok, nil
}

func ListRuntimeClientScriptsForTable(ctx context.Context, tableName string) ([]AppDefinitionClientScript, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return nil, err
	}
	app, ok := resolveRuntimeAppByTableWithApps(apps, tableName)
	if !ok {
		return nil, nil
	}
	definition := runtimeDefinitionForApp(app)
	if definition == nil {
		return nil, nil
	}

	tableName = strings.TrimSpace(strings.ToLower(tableName))
	items := make([]AppDefinitionClientScript, 0, len(definition.ClientScripts))
	for _, script := range definition.ClientScripts {
		if !script.Enabled {
			continue
		}
		if strings.TrimSpace(strings.ToLower(script.Table)) != tableName {
			continue
		}
		items = append(items, script)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items, nil
}
