package db

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const ootbBaseAppName = "base"
const ootbBaseAppLabel = "Base"
const ootbBaseAppDescription = "Out-of-the-box platform foundation, including system administration and reusable task/entity models."

func SyncOOTBBaseAppDefinition(ctx context.Context) error {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return err
	}

	baseApp, ok := findExactRegisteredAppByNameOrNamespace(apps, ootbBaseAppName)
	if !ok {
		return nil
	}

	systemApp, systemFound := findExactRegisteredAppByNameOrNamespace(apps, "system")
	if !systemFound && !IsOOTBBaseApp(baseApp) {
		return nil
	}

	draft := composeOOTBBaseDefinition(draftDefinitionForSync(systemApp), draftDefinitionForSync(baseApp))
	published := composeOOTBBaseDefinition(publishedDefinitionForSync(systemApp), publishedDefinitionForSync(baseApp))
	if draft == nil && published == nil {
		return nil
	}

	targetApp := baseApp
	targetApp.Name = ootbBaseAppName
	targetApp.Namespace = ""
	targetApp.Label = ootbBaseAppLabel
	targetApp.Description = ootbBaseAppDescription

	if draft != nil {
		if err := prepareDefinitionForApp(targetApp, draft); err != nil {
			return err
		}
		if err := validateAppDefinitionForApp(ctx, targetApp, draft); err != nil {
			return err
		}
	}
	if published != nil {
		if err := prepareDefinitionForApp(targetApp, published); err != nil {
			return err
		}
		if err := validateAppDefinitionForApp(ctx, targetApp, published); err != nil {
			return err
		}
	}

	draftContent := strings.TrimSpace(baseApp.DefinitionYAML)
	if draft != nil {
		content, err := yaml.Marshal(draft)
		if err != nil {
			return fmt.Errorf("marshal OOTB base draft definition: %w", err)
		}
		draftContent = string(content)
	}

	publishedContent := strings.TrimSpace(baseApp.PublishedDefinitionYAML)
	if published != nil {
		content, err := yaml.Marshal(published)
		if err != nil {
			return fmt.Errorf("marshal OOTB base published definition: %w", err)
		}
		publishedContent = string(content)
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin OOTB base app sync: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if systemFound && !strings.EqualFold(systemApp.ID, baseApp.ID) {
		if _, err := tx.Exec(ctx, `DELETE FROM _app WHERE name = $1 OR namespace = $1`, systemApp.Name); err != nil {
			return fmt.Errorf("delete legacy system app row: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE _app
		SET namespace = '',
			label = $2,
			description = $3,
			status = 'active',
			definition_yaml = $4,
			published_definition_yaml = $5,
			definition_version = CASE
				WHEN COALESCE(definition_yaml, '') = $4 THEN definition_version
				ELSE GREATEST(definition_version, published_version) + 1
			END,
			published_version = CASE
				WHEN COALESCE(published_definition_yaml, '') = $5 THEN published_version
				ELSE GREATEST(definition_version, published_version) + 1
			END,
			_updated_at = NOW()
		WHERE _id = NULLIF($1, '')::uuid
	`, baseApp.ID, targetApp.Label, targetApp.Description, draftContent, publishedContent)
	if err != nil {
		return fmt.Errorf("update OOTB base app definition: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit OOTB base app sync: %w", err)
	}

	activeDefinition := published
	if activeDefinition == nil {
		activeDefinition = draft
	}
	if err := syncSystemLandingPageAndRoute(ctx, activeDefinition); err != nil {
		return err
	}

	InvalidateAuthzCache()
	return nil
}

func draftDefinitionForSync(app RegisteredApp) *AppDefinition {
	if app.DraftDefinition != nil {
		return cloneAppDefinition(app.DraftDefinition)
	}
	return cloneAppDefinition(app.Definition)
}

func publishedDefinitionForSync(app RegisteredApp) *AppDefinition {
	if app.Definition != nil {
		return cloneAppDefinition(app.Definition)
	}
	return cloneAppDefinition(app.DraftDefinition)
}

func composeOOTBBaseDefinition(systemDefinition, baseDefinition *AppDefinition) *AppDefinition {
	if systemDefinition == nil && baseDefinition == nil {
		return nil
	}

	composed := &AppDefinition{
		Name:        ootbBaseAppName,
		Namespace:   "",
		Label:       ootbBaseAppLabel,
		Description: ootbBaseAppDescription,
	}

	if baseDefinition != nil {
		if label := strings.TrimSpace(baseDefinition.Label); label != "" {
			composed.Label = label
		}
	}

	composed.Dependencies = mergeOOTBDependencies(systemDefinition, baseDefinition)
	composed.Tables = mergeOOTBTables(systemDefinition, baseDefinition)
	composed.Roles = mergeOOTBRoles(systemDefinition, baseDefinition)
	composed.Forms = mergeOOTBForms(systemDefinition, baseDefinition)
	composed.Services = mergeOOTBServices(systemDefinition, baseDefinition)
	composed.Endpoints = mergeOOTBEndpoints(systemDefinition, baseDefinition)
	composed.Triggers = mergeOOTBTriggers(systemDefinition, baseDefinition)
	composed.Schedules = mergeOOTBSchedules(systemDefinition, baseDefinition)
	composed.ClientScripts = mergeOOTBClientScripts(systemDefinition, baseDefinition)
	composed.Pages = mergeOOTBPages(systemDefinition, baseDefinition)
	composed.Seeds = mergeOOTBSeeds(systemDefinition, baseDefinition)
	composed.Documentation = mergeOOTBDocumentation(systemDefinition, baseDefinition)

	ensureSystemLandingPage(composed)
	return composed
}

func mergeOOTBDependencies(systemDefinition, baseDefinition *AppDefinition) []string {
	seen := map[string]bool{}
	dependencies := make([]string, 0, 4)
	appendDependency := func(items []string) {
		for _, dependency := range items {
			name := normalizeIdentifier(dependency)
			if name == "" || name == "system" || name == ootbBaseAppName || seen[name] {
				continue
			}
			seen[name] = true
			dependencies = append(dependencies, name)
		}
	}

	if systemDefinition != nil {
		appendDependency(systemDefinition.Dependencies)
	}
	if baseDefinition != nil {
		appendDependency(baseDefinition.Dependencies)
	}
	sort.Strings(dependencies)
	return dependencies
}

func mergeOOTBTables(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionTable {
	systemTables := definitionTables(systemDefinition)
	baseTables := definitionTables(baseDefinition)
	items := make([]AppDefinitionTable, 0, allocHintSum(len(systemTables), len(baseTables)))
	seen := map[string]bool{}
	for _, table := range systemTables {
		name := normalizeIdentifier(table.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, table)
	}
	for _, table := range baseTables {
		name := normalizeIdentifier(table.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, table)
	}
	return items
}

func mergeOOTBRoles(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionRole {
	systemRoles := definitionRoles(systemDefinition)
	baseRoles := definitionRoles(baseDefinition)
	items := make([]AppDefinitionRole, 0, allocHintSum(len(systemRoles), len(baseRoles)))
	seen := map[string]bool{}
	for _, role := range systemRoles {
		name := normalizeIdentifier(role.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, role)
	}
	for _, role := range baseRoles {
		name := normalizeIdentifier(role.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, role)
	}
	return items
}

func mergeOOTBForms(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionAssetForm {
	systemForms := definitionForms(systemDefinition)
	baseForms := definitionForms(baseDefinition)
	items := make([]AppDefinitionAssetForm, 0, allocHintSum(len(systemForms), len(baseForms)))
	seen := map[string]bool{}
	for _, form := range systemForms {
		name := normalizeIdentifier(form.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, form)
	}
	for _, form := range baseForms {
		name := normalizeIdentifier(form.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, form)
	}
	return items
}

func mergeOOTBServices(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionService {
	systemServices := definitionServices(systemDefinition)
	baseServices := definitionServices(baseDefinition)
	items := make([]AppDefinitionService, 0, allocHintSum(len(systemServices), len(baseServices)))
	seen := map[string]bool{}
	for _, service := range systemServices {
		name := normalizeIdentifier(service.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, service)
	}
	for _, service := range baseServices {
		name := normalizeIdentifier(service.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, service)
	}
	return items
}

func mergeOOTBTriggers(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionTrigger {
	systemTriggers := definitionTriggers(systemDefinition)
	baseTriggers := definitionTriggers(baseDefinition)
	items := make([]AppDefinitionTrigger, 0, allocHintSum(len(systemTriggers), len(baseTriggers)))
	seen := map[string]bool{}
	for _, trigger := range systemTriggers {
		name := normalizeIdentifier(trigger.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, trigger)
	}
	for _, trigger := range baseTriggers {
		name := normalizeIdentifier(trigger.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, trigger)
	}
	return items
}

func mergeOOTBSchedules(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionSchedule {
	systemSchedules := definitionSchedules(systemDefinition)
	baseSchedules := definitionSchedules(baseDefinition)
	items := make([]AppDefinitionSchedule, 0, allocHintSum(len(systemSchedules), len(baseSchedules)))
	seen := map[string]bool{}
	for _, schedule := range systemSchedules {
		name := normalizeIdentifier(schedule.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, schedule)
	}
	for _, schedule := range baseSchedules {
		name := normalizeIdentifier(schedule.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, schedule)
	}
	return items
}

func mergeOOTBEndpoints(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionEndpoint {
	systemEndpoints := definitionEndpoints(systemDefinition)
	baseEndpoints := definitionEndpoints(baseDefinition)
	items := make([]AppDefinitionEndpoint, 0, allocHintSum(len(systemEndpoints), len(baseEndpoints)))
	seen := map[string]bool{}
	for _, endpoint := range systemEndpoints {
		name := normalizeIdentifier(endpoint.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, endpoint)
	}
	for _, endpoint := range baseEndpoints {
		name := normalizeIdentifier(endpoint.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, endpoint)
	}
	return items
}

func mergeOOTBClientScripts(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionClientScript {
	systemScripts := definitionClientScripts(systemDefinition)
	baseScripts := definitionClientScripts(baseDefinition)
	items := make([]AppDefinitionClientScript, 0, allocHintSum(len(systemScripts), len(baseScripts)))
	seen := map[string]bool{}
	for _, script := range systemScripts {
		name := normalizeIdentifier(script.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, script)
	}
	for _, script := range baseScripts {
		name := normalizeIdentifier(script.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, script)
	}
	return items
}

func mergeOOTBPages(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionPage {
	systemPages := definitionPages(systemDefinition)
	basePages := definitionPages(baseDefinition)
	items := make([]AppDefinitionPage, 0, allocHintSum(len(systemPages), len(basePages)))
	seen := map[string]bool{}
	for _, page := range systemPages {
		key := normalizeIdentifier(page.Slug)
		if key == "" {
			key = normalizeIdentifier(page.Name)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, page)
	}
	for _, page := range basePages {
		key := normalizeIdentifier(page.Slug)
		if key == "" {
			key = normalizeIdentifier(page.Name)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, page)
	}
	return items
}

func mergeOOTBSeeds(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionSeed {
	systemSeeds := definitionSeeds(systemDefinition)
	baseSeeds := definitionSeeds(baseDefinition)
	items := make([]AppDefinitionSeed, 0, allocHintSum(len(systemSeeds), len(baseSeeds)))
	items = append(items, systemSeeds...)
	items = append(items, baseSeeds...)
	return items
}

func mergeOOTBDocumentation(systemDefinition, baseDefinition *AppDefinition) []AppDefinitionDocumentation {
	systemDocs := definitionDocumentation(systemDefinition)
	baseDocs := definitionDocumentation(baseDefinition)
	items := make([]AppDefinitionDocumentation, 0, allocHintSum(len(systemDocs), len(baseDocs)))
	seen := map[string]bool{}
	for _, article := range systemDocs {
		name := normalizeIdentifier(article.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, article)
	}
	for _, article := range baseDocs {
		name := normalizeIdentifier(article.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, article)
	}
	return items
}

func definitionTables(definition *AppDefinition) []AppDefinitionTable {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionTable(nil), definition.Tables...)
}

func definitionRoles(definition *AppDefinition) []AppDefinitionRole {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionRole(nil), definition.Roles...)
}

func definitionForms(definition *AppDefinition) []AppDefinitionAssetForm {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionAssetForm(nil), definition.Forms...)
}

func definitionServices(definition *AppDefinition) []AppDefinitionService {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionService(nil), definition.Services...)
}

func definitionTriggers(definition *AppDefinition) []AppDefinitionTrigger {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionTrigger(nil), definition.Triggers...)
}

func definitionSchedules(definition *AppDefinition) []AppDefinitionSchedule {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionSchedule(nil), definition.Schedules...)
}

func definitionEndpoints(definition *AppDefinition) []AppDefinitionEndpoint {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionEndpoint(nil), definition.Endpoints...)
}

func definitionClientScripts(definition *AppDefinition) []AppDefinitionClientScript {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionClientScript(nil), definition.ClientScripts...)
}

func definitionPages(definition *AppDefinition) []AppDefinitionPage {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionPage(nil), definition.Pages...)
}

func definitionSeeds(definition *AppDefinition) []AppDefinitionSeed {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionSeed(nil), definition.Seeds...)
}

func definitionDocumentation(definition *AppDefinition) []AppDefinitionDocumentation {
	if definition == nil {
		return nil
	}
	return append([]AppDefinitionDocumentation(nil), definition.Documentation...)
}
