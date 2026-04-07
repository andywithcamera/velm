package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func RunMigrations(ctx context.Context) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}

	if _, err := Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _schema_migration (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create _schema_migration: %w", err)
	}

	applied, err := loadAppliedMigrations(ctx)
	if err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migration files: %w", err)
	}

	var versions []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".sql") {
			versions = append(versions, name)
		}
	}
	sort.Strings(versions)

	const systemAppMigration = "042_system_app.sql"

	for _, version := range versions {
		if applied[version] {
			continue
		}
		if err := applyMigration(ctx, version); err != nil {
			return err
		}
		// System app YAML is inserted in 042.
		// Later migrations (043+) reference _user, so the minimal required system tables
		// must exist before those apply on a fresh DB.
		if version == systemAppMigration {
			if err := EnsureRequiredSystemTablesAfter042(ctx); err != nil {
				return fmt.Errorf("sync system tables after %s: %w", version, err)
			}
		}
	}

	if err := MigrateLegacyScriptDefinitions(ctx); err != nil {
		return err
	}
	if err := SyncSystemAppDefinitionWithPhysicalTables(ctx); err != nil {
		return err
	}
	if err := SyncBaseAppDefinitionTaskModel(ctx); err != nil {
		return err
	}
	if err := SyncOOTBBaseAppDefinition(ctx); err != nil {
		return err
	}
	if err := SyncBundledAppDefinitions(ctx); err != nil {
		return err
	}
	if err := SyncPublishedAppPages(ctx); err != nil {
		return err
	}

	return nil
}

func loadAppliedMigrations(ctx context.Context) (map[string]bool, error) {
	rows, err := Pool.Query(ctx, `SELECT version FROM _schema_migration`)
	if err != nil {
		return nil, fmt.Errorf("load applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan migration version: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration versions: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, version string) error {
	content, err := migrationsFS.ReadFile("migrations/" + version)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", version, err)
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", version, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, string(content)); err != nil {
		return fmt.Errorf("execute migration %s: %w", version, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO _schema_migration (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}

	return nil
}
