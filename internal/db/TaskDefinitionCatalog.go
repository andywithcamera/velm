package db

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// The catalog form is how routing is configured: which queue takes a task and
// which person in it. default_group_id was already there; the assignee column
// arrives with 090 and has to be described to the app before anyone can set
// it, or routing stays a migration's business.
func SyncTaskDefinitionCatalog(ctx context.Context) error {
	app, err := GetActiveAppByName(ctx, "task_definition")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "app not found") {
			return nil
		}
		return err
	}

	draft := cloneAppDefinition(app.DraftDefinition)
	if draft == nil {
		draft = cloneAppDefinition(app.Definition)
	}
	published := cloneAppDefinition(app.Definition)
	if published == nil {
		published = cloneAppDefinition(app.DraftDefinition)
	}
	if draft == nil && published == nil {
		return nil
	}

	draftChanged := ensureTaskDefinitionAssigneeColumn(draft)
	publishedChanged := ensureTaskDefinitionAssigneeColumn(published)
	if !draftChanged && !publishedChanged {
		return nil
	}

	if draft != nil {
		if err := prepareDefinitionForApp(app, draft); err != nil {
			return err
		}
		if err := validateAppDefinitionForApp(ctx, app, draft); err != nil {
			return err
		}
	}
	if published != nil {
		if err := prepareDefinitionForApp(app, published); err != nil {
			return err
		}
		if err := validateAppDefinitionForApp(ctx, app, published); err != nil {
			return err
		}
	}

	draftContent := strings.TrimSpace(app.DefinitionYAML)
	if draft != nil {
		content, err := yaml.Marshal(draft)
		if err != nil {
			return fmt.Errorf("marshal task definition draft: %w", err)
		}
		draftContent = string(content)
	}
	publishedContent := strings.TrimSpace(app.PublishedDefinitionYAML)
	if published != nil {
		content, err := yaml.Marshal(published)
		if err != nil {
			return fmt.Errorf("marshal task definition published: %w", err)
		}
		publishedContent = string(content)
	}

	_, err = Pool.Exec(ctx, `
		UPDATE _app
		SET definition_yaml = $2,
			published_definition_yaml = $3,
			definition_version = CASE
				WHEN COALESCE(definition_yaml, '') = $2 THEN definition_version
				ELSE GREATEST(definition_version, published_version) + 1
			END,
			published_version = CASE
				WHEN COALESCE(published_definition_yaml, '') = $3 THEN published_version
				ELSE GREATEST(definition_version, published_version) + 1
			END,
			_updated_at = NOW()
		WHERE name = $1 OR namespace = $1
	`, app.Name, draftContent, publishedContent)
	if err != nil {
		return fmt.Errorf("sync task definition app: %w", err)
	}
	return nil
}

// ensureTaskDefinitionAssigneeColumn puts default_assignee_id on the
// definition table and its default form, directly after the group it must
// agree with. Idempotent.
func ensureTaskDefinitionAssigneeColumn(definition *AppDefinition) bool {
	if definition == nil {
		return false
	}
	for i := range definition.Tables {
		table := &definition.Tables[i]
		if table.Name != "base_task_definition" {
			continue
		}
		before, _ := yaml.Marshal(table)
		hasColumn := false
		for _, column := range table.Columns {
			if strings.EqualFold(column.Name, "default_assignee_id") {
				hasColumn = true
				break
			}
		}
		if !hasColumn {
			groupIndex := -1
			for j, column := range table.Columns {
				if strings.EqualFold(column.Name, "default_group_id") {
					groupIndex = j
					break
				}
			}
			column := AppDefinitionColumn{
				Name:           "default_assignee_id",
				Label:          "Default Assignee",
				DataType:       "reference",
				IsNullable:     true,
				ReferenceTable: "_user",
			}
			if groupIndex >= 0 {
				// Keep the column next to the group it belongs with rather than
				// at the end, so the catalog form reads as routing config.
				table.Columns = slices.Insert(table.Columns, groupIndex+1, column)
			} else {
				table.Columns = append(table.Columns, column)
			}
		}
		table.Forms = upsertFieldAfterInForms(table.Forms, "default_assignee_id", "default_group_id")
		after, _ := yaml.Marshal(table)
		return string(before) != string(after)
	}
	return false
}
