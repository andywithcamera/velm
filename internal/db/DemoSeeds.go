package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed demo-seeds/*.sql
var demoSeedsFS embed.FS

func RunDemoSeeds(ctx context.Context) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}

	if _, err := Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _demo_seed (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create _demo_seed: %w", err)
	}

	applied, err := loadAppliedDemoSeeds(ctx)
	if err != nil {
		return err
	}

	entries, err := fs.ReadDir(demoSeedsFS, "demo-seeds")
	if err != nil {
		return fmt.Errorf("read demo seed files: %w", err)
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

	for _, version := range versions {
		if applied[version] {
			continue
		}
		if err := applyDemoSeed(ctx, version); err != nil {
			return err
		}
	}

	return nil
}

func loadAppliedDemoSeeds(ctx context.Context) (map[string]bool, error) {
	rows, err := Pool.Query(ctx, `SELECT version FROM _demo_seed`)
	if err != nil {
		return nil, fmt.Errorf("load applied demo seeds: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan demo seed version: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate demo seed versions: %w", err)
	}
	return applied, nil
}

func applyDemoSeed(ctx context.Context, version string) error {
	content, err := demoSeedsFS.ReadFile("demo-seeds/" + version)
	if err != nil {
		return fmt.Errorf("read demo seed %s: %w", version, err)
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin demo seed %s: %w", version, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, string(content)); err != nil {
		return fmt.Errorf("execute demo seed %s: %w", version, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO _demo_seed (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record demo seed %s: %w", version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit demo seed %s: %w", version, err)
	}

	return nil
}
