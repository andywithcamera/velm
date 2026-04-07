package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type legacyAppDefinition struct {
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
	Scripts       []AppDefinitionScript        `yaml:"scripts"`
}

func MigrateLegacyScriptDefinitions(ctx context.Context) error {
	rows, err := Pool.Query(ctx, `
		SELECT _id::text, COALESCE(definition_yaml, ''), COALESCE(published_definition_yaml, '')
		FROM _app
		WHERE _deleted_at IS NULL
	`)
	if err != nil {
		return fmt.Errorf("list app definitions for script migration: %w", err)
	}
	defer rows.Close()

	type item struct {
		id        string
		draft     string
		published string
	}
	items := make([]item, 0, 8)
	for rows.Next() {
		var current item
		if err := rows.Scan(&current.id, &current.draft, &current.published); err != nil {
			return fmt.Errorf("scan app definition for script migration: %w", err)
		}
		items = append(items, current)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate app definitions for script migration: %w", err)
	}

	for _, current := range items {
		nextDraft, draftChanged, err := migrateLegacyScriptDefinitionYAML(current.draft)
		if err != nil {
			return fmt.Errorf("migrate draft definition %s: %w", current.id, err)
		}
		nextPublished, publishedChanged, err := migrateLegacyScriptDefinitionYAML(current.published)
		if err != nil {
			return fmt.Errorf("migrate published definition %s: %w", current.id, err)
		}
		if !draftChanged && !publishedChanged {
			continue
		}
		if _, err := Pool.Exec(ctx, `
			UPDATE _app
			SET definition_yaml = $2,
				published_definition_yaml = $3,
				_updated_at = NOW()
			WHERE _id = $1::uuid
		`, current.id, nextDraft, nextPublished); err != nil {
			return fmt.Errorf("update migrated script definition %s: %w", current.id, err)
		}
	}

	return nil
}

func migrateLegacyScriptDefinitionYAML(content string) (string, bool, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false, nil
	}

	var legacy legacyAppDefinition
	if err := yaml.Unmarshal([]byte(content), &legacy); err != nil {
		return "", false, fmt.Errorf("parse legacy app definition yaml: %w", err)
	}
	if len(legacy.Scripts) == 0 {
		return content, false, nil
	}

	definition := &AppDefinition{
		Name:          legacy.Name,
		Namespace:     legacy.Namespace,
		Label:         legacy.Label,
		Description:   legacy.Description,
		Dependencies:  append([]string(nil), legacy.Dependencies...),
		Roles:         append([]AppDefinitionRole(nil), legacy.Roles...),
		Tables:        append([]AppDefinitionTable(nil), legacy.Tables...),
		Forms:         append([]AppDefinitionAssetForm(nil), legacy.Forms...),
		Pages:         append([]AppDefinitionPage(nil), legacy.Pages...),
		ClientScripts: append([]AppDefinitionClientScript(nil), legacy.ClientScripts...),
		Seeds:         append([]AppDefinitionSeed(nil), legacy.Seeds...),
		Documentation: append([]AppDefinitionDocumentation(nil), legacy.Documentation...),
		Services:      append([]AppDefinitionService(nil), legacy.Services...),
		Endpoints:     append([]AppDefinitionEndpoint(nil), legacy.Endpoints...),
		Triggers:      append([]AppDefinitionTrigger(nil), legacy.Triggers...),
		Schedules:     append([]AppDefinitionSchedule(nil), legacy.Schedules...),
	}

	migrateLegacyScriptsIntoDefinition(definition, legacy.Scripts)
	if err := normalizeAppDefinition(definition); err != nil {
		return "", false, err
	}

	out, err := yaml.Marshal(definition)
	if err != nil {
		return "", false, fmt.Errorf("marshal migrated app definition yaml: %w", err)
	}
	return string(out), true, nil
}

func migrateLegacyScriptsIntoDefinition(definition *AppDefinition, scripts []AppDefinitionScript) {
	if definition == nil || len(scripts) == 0 {
		return
	}

	serviceName := uniqueLegacyScriptServiceName(definition.Services)
	serviceIndex := ensureServiceIndex(definition, serviceName)

	for _, script := range scripts {
		methodName := uniqueLegacyMethodName(definition.Services[serviceIndex].Methods, script.Name)
		label := firstNonEmpty(strings.TrimSpace(script.Label), humanizeIdentifier(script.Name))
		code := scriptTextValue(script.Code, script.Script)
		enabled := script.Enabled || strings.TrimSpace(strings.ToLower(script.Status)) == "published"

		method := AppDefinitionMethod{
			Name:        methodName,
			Label:       label,
			Description: strings.TrimSpace(script.Description),
			Visibility:  legacyScriptVisibility(script, enabled),
			Language:    firstNonEmpty(strings.TrimSpace(strings.ToLower(script.Language)), "javascript"),
			Roles:       legacyScriptMethodRoles(script, enabled),
			Script:      code,
		}
		definition.Services[serviceIndex].Methods = append(definition.Services[serviceIndex].Methods, method)

		call := serviceName + "." + methodName
		if scriptHasEndpointBinding(script) {
			definition.Endpoints = append(definition.Endpoints, AppDefinitionEndpoint{
				Name:        uniqueLegacyEndpointName(definition.Endpoints, script.Name),
				Label:       label,
				Description: strings.TrimSpace(script.Description),
				Method:      script.Endpoint.Method,
				Path:        script.Endpoint.Path,
				Call:        call,
				Roles:       mergeIdentifierLists(script.Roles, script.Endpoint.Roles),
				Enabled:     script.Endpoint.Enabled && enabled,
			})
		}
		if scriptHasTriggerBinding(script) {
			definition.Triggers = append(definition.Triggers, AppDefinitionTrigger{
				Name:        uniqueLegacyTriggerName(definition.Triggers, script.Name),
				Label:       label,
				Description: strings.TrimSpace(script.Description),
				Event:       firstNonEmpty(strings.TrimSpace(strings.ToLower(script.EventName)), "record.update"),
				Table:       strings.TrimSpace(strings.ToLower(script.TableName)),
				Condition:   strings.TrimSpace(script.ConditionExpr),
				Call:        call,
				Mode:        legacyScriptTriggerMode(script),
				Order:       100,
				Enabled:     enabled,
			})
		}
	}
}

func scriptHasEndpointBinding(script AppDefinitionScript) bool {
	return script.Endpoint.Enabled && (strings.TrimSpace(script.Endpoint.Method) != "" || strings.TrimSpace(script.Endpoint.Path) != "")
}

func scriptHasTriggerBinding(script AppDefinitionScript) bool {
	return strings.TrimSpace(script.TableName) != "" && (strings.TrimSpace(script.EventName) != "" || strings.TrimSpace(script.TriggerType) != "")
}

func legacyScriptVisibility(script AppDefinitionScript, enabled bool) string {
	if scriptHasEndpointBinding(script) || scriptHasTriggerBinding(script) || !enabled {
		return "private"
	}
	return "public"
}

func legacyScriptMethodRoles(script AppDefinitionScript, enabled bool) []string {
	if scriptHasEndpointBinding(script) || scriptHasTriggerBinding(script) || !enabled {
		return nil
	}
	return append([]string(nil), script.Roles...)
}

func legacyScriptTriggerMode(script AppDefinitionScript) string {
	switch strings.TrimSpace(strings.ToLower(script.TriggerType)) {
	case "before_update", "before_insert", "sync":
		return "sync"
	default:
		return "async"
	}
}

func ensureServiceIndex(definition *AppDefinition, serviceName string) int {
	for i := range definition.Services {
		if strings.EqualFold(strings.TrimSpace(definition.Services[i].Name), serviceName) {
			return i
		}
	}
	definition.Services = append(definition.Services, AppDefinitionService{
		Name:        serviceName,
		Label:       "Migrated Scripts",
		Description: "Server methods migrated from the legacy top-level scripts section.",
		Methods:     []AppDefinitionMethod{},
	})
	return len(definition.Services) - 1
}

func uniqueLegacyScriptServiceName(services []AppDefinitionService) string {
	base := "migrated_scripts"
	name := base
	index := 2
	for legacyServiceNameExists(services, name) {
		name = base + "_" + strconv.Itoa(index)
		index++
	}
	return name
}

func legacyServiceNameExists(services []AppDefinitionService, target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	for _, service := range services {
		if strings.TrimSpace(strings.ToLower(service.Name)) == target {
			return true
		}
	}
	return false
}

func uniqueLegacyMethodName(methods []AppDefinitionMethod, raw string) string {
	base := normalizeLooseIdentifier(raw)
	if base == "" {
		base = "migrated_script"
	}
	name := base
	index := 2
	for legacyMethodNameExists(methods, name) {
		name = base + "_" + strconv.Itoa(index)
		index++
	}
	return name
}

func legacyMethodNameExists(methods []AppDefinitionMethod, target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	for _, method := range methods {
		if strings.TrimSpace(strings.ToLower(method.Name)) == target {
			return true
		}
	}
	return false
}

func uniqueLegacyEndpointName(items []AppDefinitionEndpoint, raw string) string {
	return uniqueLegacyName(raw, "migrated_endpoint", func(name string) bool {
		for _, item := range items {
			if strings.TrimSpace(strings.ToLower(item.Name)) == name {
				return true
			}
		}
		return false
	})
}

func uniqueLegacyTriggerName(items []AppDefinitionTrigger, raw string) string {
	return uniqueLegacyName(raw, "migrated_trigger", func(name string) bool {
		for _, item := range items {
			if strings.TrimSpace(strings.ToLower(item.Name)) == name {
				return true
			}
		}
		return false
	})
}

func uniqueLegacyName(raw, fallback string, exists func(string) bool) string {
	base := normalizeLooseIdentifier(raw)
	if base == "" {
		base = fallback
	}
	name := base
	index := 2
	for exists(name) {
		name = base + "_" + strconv.Itoa(index)
		index++
	}
	return name
}

func mergeIdentifierLists(items ...[]string) []string {
	seen := map[string]bool{}
	merged := make([]string, 0, 8)
	for _, list := range items {
		for _, item := range list {
			name := strings.TrimSpace(strings.ToLower(item))
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			merged = append(merged, name)
		}
	}
	return merged
}
