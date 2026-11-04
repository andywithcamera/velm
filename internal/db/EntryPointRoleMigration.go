package db

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateEntryPointRoles backfills the reserved "authenticated" role onto every
// public service method and enabled endpoint whose stored definition declares no
// roles at all.
//
// Before v0.2.0 an empty role list meant "no restriction": appEntryPointAccessAllowed
// returned true when no roles resolved, so an author who forgot the field published an
// entry point that anyone could reach. That silent grant was the bug fixed in #138. The
// runtime now denies an empty list and ValidateAppDefinition refuses to save a new
// definition that omits it.
//
// Neither of those helps a definition already in the database. It fails validation the
// next time any sync pass validates it, and because those passes run during
// RunMigrations the failure is fatal at boot: the server cannot start, so there is no
// admin UI in which to repair the definition. Observed in production as
//
//	fatal: failed to initialize database: error running migrations: method
//	"ingest_event" on service "observability" is public and must declare roles
//
// backfilling "authenticated" preserves the closest thing to the old effective
// behaviour while being strictly no weaker than what the shipped definitions now
// declare. It runs before every validating sync so those passes see repaired data.
func MigrateEntryPointRoles(ctx context.Context) error {
	rows, err := Pool.Query(ctx, `
		SELECT _id::text, COALESCE(definition_yaml, ''), COALESCE(published_definition_yaml, '')
		FROM _app
		WHERE _deleted_at IS NULL
	`)
	if err != nil {
		return fmt.Errorf("list app definitions for entry point role backfill: %w", err)
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
			return fmt.Errorf("scan app definition for entry point role backfill: %w", err)
		}
		items = append(items, current)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate app definitions for entry point role backfill: %w", err)
	}

	for _, current := range items {
		nextDraft, draftChanged, err := backfillEntryPointRolesYAML(current.draft)
		if err != nil {
			return fmt.Errorf("backfill draft entry point roles %s: %w", current.id, err)
		}
		nextPublished, publishedChanged, err := backfillEntryPointRolesYAML(current.published)
		if err != nil {
			return fmt.Errorf("backfill published entry point roles %s: %w", current.id, err)
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
			return fmt.Errorf("update backfilled entry point roles %s: %w", current.id, err)
		}
	}

	return nil
}

func backfillEntryPointRolesYAML(content string) (string, bool, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false, nil
	}

	var definition AppDefinition
	if err := yaml.Unmarshal([]byte(content), &definition); err != nil {
		return "", false, fmt.Errorf("parse app definition yaml: %w", err)
	}

	if !backfillEntryPointRoles(&definition) {
		return content, false, nil
	}

	out, err := yaml.Marshal(&definition)
	if err != nil {
		return "", false, fmt.Errorf("marshal app definition yaml: %w", err)
	}
	return string(out), true, nil
}

// backfillEntryPointRoles reports whether it changed anything.
func backfillEntryPointRoles(definition *AppDefinition) bool {
	if definition == nil {
		return false
	}

	changed := false
	for serviceIndex := range definition.Services {
		methods := definition.Services[serviceIndex].Methods
		for methodIndex := range methods {
			if methods[methodIndex].Visibility != "public" {
				continue
			}
			if len(declaredRoles(methods[methodIndex].Roles)) > 0 {
				continue
			}
			methods[methodIndex].Roles = []string{AuthenticatedRole}
			changed = true
		}
	}
	for endpointIndex := range definition.Endpoints {
		if !definition.Endpoints[endpointIndex].Enabled {
			continue
		}
		if len(declaredRoles(definition.Endpoints[endpointIndex].Roles)) > 0 {
			continue
		}
		definition.Endpoints[endpointIndex].Roles = []string{AuthenticatedRole}
		changed = true
	}
	return changed
}
