package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type ScriptRegistryEntry struct {
	ID            int64
	AppName       string
	AppLabel      string
	ServiceName   string
	MethodName    string
	Call          string
	Label         string
	Visibility    string
	Language      string
	LatestVersion int
	BindingCount  int
	PublishedAt   string
	OwnerUserID   string
	LatestCode    string
	EditorObject  string
}

type ScriptVersionEntry struct {
	ID          int64
	VersionNum  int
	Code        string
	Checksum    string
	CreatedBy   string
	PublishedAt string
	CreatedAt   string
}

func ListScriptRegistry(ctx context.Context) ([]ScriptRegistryEntry, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return nil, err
	}
	return buildScriptRegistryEntries(apps), nil
}

func GetScriptRegistryEntry(ctx context.Context, scriptID int64) (ScriptRegistryEntry, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return ScriptRegistryEntry{}, err
	}
	entry, ok := findScriptRegistryEntryByID(apps, scriptID)
	if !ok {
		return ScriptRegistryEntry{}, fmt.Errorf("script not found")
	}
	return entry, nil
}

func ListScriptVersions(ctx context.Context, scriptID int64) ([]ScriptVersionEntry, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return nil, err
	}
	app, service, method, ok := resolveYAMLMethodByID(apps, scriptID)
	if !ok {
		return nil, fmt.Errorf("script not found")
	}
	return buildYAMLMethodVersions(app, service, method), nil
}

func buildScriptRegistryEntries(apps []RegisteredApp) []ScriptRegistryEntry {
	items := make([]ScriptRegistryEntry, 0, 16)
	for _, app := range apps {
		definition := effectiveScriptRegistryDefinition(app)
		if definition == nil {
			continue
		}
		for _, service := range definition.Services {
			for _, method := range service.Methods {
				items = append(items, buildScriptRegistryEntry(app, definition, service, method))
			}
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].AppLabel == items[j].AppLabel {
			if items[i].ServiceName == items[j].ServiceName {
				return items[i].MethodName < items[j].MethodName
			}
			return items[i].ServiceName < items[j].ServiceName
		}
		return items[i].AppLabel < items[j].AppLabel
	})
	return items
}

func findScriptRegistryEntryByID(apps []RegisteredApp, scriptID int64) (ScriptRegistryEntry, bool) {
	for _, app := range apps {
		definition := effectiveScriptRegistryDefinition(app)
		if definition == nil {
			continue
		}
		for _, service := range definition.Services {
			for _, method := range service.Methods {
				entry := buildScriptRegistryEntry(app, definition, service, method)
				if entry.ID == scriptID {
					return entry, true
				}
			}
		}
	}
	return ScriptRegistryEntry{}, false
}

func resolveYAMLMethodByID(apps []RegisteredApp, scriptID int64) (RegisteredApp, AppDefinitionService, AppDefinitionMethod, bool) {
	for _, app := range apps {
		definition := effectiveScriptRegistryDefinition(app)
		if definition == nil {
			continue
		}
		for _, service := range definition.Services {
			for _, method := range service.Methods {
				if SyntheticYAMLMethodID(app.Name, service.Name, method.Name) == scriptID {
					return app, service, method, true
				}
			}
		}
	}
	return RegisteredApp{}, AppDefinitionService{}, AppDefinitionMethod{}, false
}

func effectiveScriptRegistryDefinition(app RegisteredApp) *AppDefinition {
	if app.DraftDefinition != nil {
		return app.DraftDefinition
	}
	return app.Definition
}

func buildScriptRegistryEntry(app RegisteredApp, definition *AppDefinition, service AppDefinitionService, method AppDefinitionMethod) ScriptRegistryEntry {
	code := strings.TrimSpace(method.Script)
	label := strings.TrimSpace(method.Label)
	if label == "" {
		label = humanizeIdentifier(method.Name)
	}
	appLabel := strings.TrimSpace(app.Label)
	if appLabel == "" {
		appLabel = strings.TrimSpace(app.Name)
	}
	latestVersion := 0
	if code != "" {
		latestVersion = 1
	}

	call := strings.TrimSpace(strings.ToLower(service.Name + "." + method.Name))
	return ScriptRegistryEntry{
		ID:            SyntheticYAMLMethodID(app.Name, service.Name, method.Name),
		AppName:       app.Name,
		AppLabel:      appLabel,
		ServiceName:   strings.TrimSpace(service.Name),
		MethodName:    strings.TrimSpace(method.Name),
		Call:          call,
		Label:         label,
		Visibility:    strings.TrimSpace(method.Visibility),
		Language:      strings.TrimSpace(method.Language),
		LatestVersion: latestVersion,
		BindingCount:  methodRegistryBindingCount(definition, call),
		PublishedAt:   "Published in app definition",
		OwnerUserID:   "yaml",
		LatestCode:    code,
		EditorObject:  fmt.Sprintf("method:%s:%s:%s", app.Name, service.Name, method.Name),
	}
}

func buildYAMLMethodVersions(app RegisteredApp, service AppDefinitionService, method AppDefinitionMethod) []ScriptVersionEntry {
	code := strings.TrimSpace(method.Script)
	if code == "" {
		return nil
	}
	checksumBytes := sha256.Sum256([]byte(code))
	return []ScriptVersionEntry{{
		ID:          SyntheticYAMLMethodID(app.Name, service.Name, method.Name),
		VersionNum:  1,
		Code:        code,
		Checksum:    hex.EncodeToString(checksumBytes[:]),
		CreatedBy:   "yaml",
		PublishedAt: "Published in app definition",
		CreatedAt:   "YAML definition",
	}}
}

func methodRegistryBindingCount(definition *AppDefinition, call string) int {
	if definition == nil {
		return 0
	}
	count := 0
	call = strings.TrimSpace(strings.ToLower(call))
	for _, endpoint := range definition.Endpoints {
		if strings.TrimSpace(strings.ToLower(endpoint.Call)) == call {
			count++
		}
	}
	for _, trigger := range definition.Triggers {
		if strings.TrimSpace(strings.ToLower(trigger.Call)) == call {
			count++
		}
	}
	for _, schedule := range definition.Schedules {
		if strings.TrimSpace(strings.ToLower(schedule.Call)) == call {
			count++
		}
	}
	for _, table := range definition.Tables {
		for _, trigger := range table.Triggers {
			if strings.TrimSpace(strings.ToLower(trigger.Call)) == call {
				count++
			}
		}
	}
	return count
}

func CountYAMLTableScriptDependencies(ctx context.Context, tableName string) (int, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return 0, err
	}
	return countYAMLTableScriptDependencies(apps, tableName), nil
}

func CountYAMLColumnConditionDependencies(ctx context.Context, tableName, columnName string) (int, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return 0, err
	}
	return countYAMLColumnConditionDependencies(apps, tableName, columnName), nil
}

func countYAMLTableScriptDependencies(apps []RegisteredApp, tableName string) int {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		return 0
	}

	count := 0
	for _, app := range apps {
		definition := effectiveScriptRegistryDefinition(app)
		if definition == nil {
			continue
		}
		for _, trigger := range definition.Triggers {
			if strings.TrimSpace(strings.ToLower(trigger.Table)) == tableName {
				count++
			}
		}
		for _, script := range definition.ClientScripts {
			if strings.TrimSpace(strings.ToLower(script.Table)) == tableName {
				count++
			}
		}
	}
	return count
}

func countYAMLColumnConditionDependencies(apps []RegisteredApp, tableName, columnName string) int {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	columnName = strings.TrimSpace(strings.ToLower(columnName))
	if tableName == "" || columnName == "" {
		return 0
	}

	count := 0
	for _, app := range apps {
		definition := effectiveScriptRegistryDefinition(app)
		if definition == nil {
			continue
		}
		for _, trigger := range definition.Triggers {
			if strings.TrimSpace(strings.ToLower(trigger.Table)) != tableName {
				continue
			}
			if strings.Contains(strings.ToLower(strings.TrimSpace(trigger.Condition)), columnName) {
				count++
			}
		}
		for _, script := range definition.ClientScripts {
			if strings.TrimSpace(strings.ToLower(script.Table)) != tableName {
				continue
			}
			if strings.TrimSpace(strings.ToLower(script.Field)) == columnName {
				count++
			}
		}
	}
	return count
}
