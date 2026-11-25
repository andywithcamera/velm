package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// #373: 076 created the definition tables under app-local names, so their
// constraints are named after the *old* tables (uq_task_definition_child,
// _task_definition_slug_key). 078/080 renamed the tables to base_; 094/098 drop
// the constraints under the base_ spelling, which on a real instance drops
// nothing. A from-zero database takes the other path and is correct, so the
// suite was green while production still refused to save a container that
// names one definition twice.
//
// These tests read the live schema and then exercise the behaviour the
// constraint was blocking, not the migration's text.

// uniqueConstraintsOver returns the names of unique constraints on table whose
// column set is exactly columns.
func uniqueConstraintsOver(t *testing.T, ctx context.Context, table string, columns ...string) []string {
	t.Helper()
	rows, err := Pool.Query(ctx, `
		SELECT c.conname
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		WHERE t.relname = $1 AND c.contype = 'u'
			AND (
				SELECT array_agg(a.attname::text ORDER BY a.attname)
				FROM pg_attribute a
				WHERE a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
			) = $2`, table, columns)
	if err != nil {
		t.Fatalf("query unique constraints on %s: %v", table, err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan constraint name: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate constraints on %s: %v", table, err)
	}
	return names
}

// runMigrationSQL executes one embedded migration file against the test
// database. Every migration we run this way must be idempotent, which 106 is
// by construction: it drops by shape and names nothing it has not found.
func runMigrationSQL(t *testing.T, ctx context.Context, version string) {
	t.Helper()
	files, err := loadMigrationFiles()
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	content, ok := files[version]
	if !ok {
		t.Fatalf("migration %s is not embedded", version)
	}
	if _, err := Pool.Exec(ctx, string(content)); err != nil {
		t.Fatalf("execute migration %s: %v", version, err)
	}
}

// AC-1: after the full migration chain neither uniqueness rule survives, under
// any name.
func TestDefinitionUniqueConstraintsAreGoneByShape(t *testing.T) {
	ctx := taskEngineTestDB(t)

	if got := uniqueConstraintsOver(t, ctx, "base_task_definition_child",
		"child_definition_id", "container_definition_id"); len(got) != 0 {
		t.Fatalf("base_task_definition_child still unique over (container, child): %v", got)
	}
	if got := uniqueConstraintsOver(t, ctx, "base_task_definition", "slug"); len(got) != 0 {
		t.Fatalf("base_task_definition still plain-unique over slug: %v", got)
	}

	// The rule 094 put in place instead must still be there: slug unique among
	// live rows. A shape-based drop that took the partial index with it would
	// trade one bug for another.
	var liveIndex string
	if err := Pool.QueryRow(ctx, `
		SELECT indexname FROM pg_indexes
		WHERE tablename = 'base_task_definition' AND indexname = 'uq_base_task_definition_slug_live'`).Scan(&liveIndex); err != nil {
		t.Fatalf("uq_base_task_definition_slug_live is gone: %v", err)
	}
}

// AC-2: the behaviour 098 introduced. A container names one definition from two
// blocks — two approval gates, two "record the rejection" steps — and both
// rows are kept.
func TestContainerMayCallOneDefinitionTwice(t *testing.T) {
	ctx := taskEngineTestDB(t)

	suffix := strings.ReplaceAll(t.Name(), "/", "_")
	container := schemaTestDefinition(t, ctx, "106-container-"+suffix, true)
	child := schemaTestDefinition(t, ctx, "106-child-"+suffix, false)

	schemaTestBlock(t, ctx, container, child, 1)
	schemaTestBlock(t, ctx, container, child, 2)

	var count int
	if err := Pool.QueryRow(ctx, `
		SELECT count(*) FROM base_task_definition_child
		WHERE container_definition_id = $1 AND child_definition_id = $2`,
		container, child).Scan(&count); err != nil {
		t.Fatalf("count blocks: %v", err)
	}
	if count != 2 {
		t.Fatalf("container holds %d rows for one child definition, want 2; the save was "+
			"rejected or a row was lost", count)
	}
}

// AC-3: the 076-era path. Install the child constraint under the name a renamed
// instance actually carries, plus a name neither migration mentions, and prove
// 106 removes both while leaving slug reuse among live rows working.
func TestMigration106DropsConstraintsRenamedInstancesCarry(t *testing.T) {
	ctx := taskEngineTestDB(t)

	var duplicates int
	if err := Pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT 1 FROM base_task_definition_child
			GROUP BY container_definition_id, child_definition_id
			HAVING count(*) > 1
		) d`).Scan(&duplicates); err != nil {
		t.Fatalf("count duplicate blocks: %v", err)
	}
	if duplicates > 0 {
		t.Skip("test database holds duplicate block rows, so the legacy constraint cannot be reinstalled")
	}

	constraints := []struct{ table, name, columns string }{
		{"base_task_definition_child", "uq_task_definition_child", "(container_definition_id, child_definition_id)"},
		{"base_task_definition_child", "legacy_shape_only_uq", "(container_definition_id, child_definition_id)"},
	}
	for _, c := range constraints {
		table, name := c.table, c.name
		if _, err := Pool.Exec(ctx, `ALTER TABLE `+table+` ADD CONSTRAINT `+name+
			` UNIQUE `+c.columns); err != nil {
			t.Fatalf("install legacy constraint %s: %v", name, err)
		}
		t.Cleanup(func() {
			_, _ = Pool.Exec(context.Background(), `ALTER TABLE `+table+` DROP CONSTRAINT IF EXISTS `+name)
		})
	}

	// The slug rule is checked by behaviour rather than by installing the
	// constraint: the shared test database accumulates soft-deleted
	// definitions with a live twin, so a plain UNIQUE over slug cannot be
	// created there at all. The migration drops it by the same column-shape
	// match proven on the child table above.
	softSlug := fmt.Sprintf("106-soft-slug-%d", time.Now().UnixNano())
	if _, err := Pool.Exec(ctx, `
		INSERT INTO base_task_definition (slug, display_name, _deleted_at)
		VALUES ($1, 'old', NOW())`, softSlug); err != nil {
		t.Fatalf("seed soft-deleted definition: %v", err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(),
			`DELETE FROM base_task_definition WHERE slug = $1`, softSlug)
	})
	if _, err := Pool.Exec(ctx, `
		INSERT INTO base_task_definition (slug, display_name)
		VALUES ($1, 'new')`, softSlug); err != nil {
		t.Fatalf("a soft-deleted definition still reserves its slug: %v", err)
	}

	runMigrationSQL(t, ctx, "106_legacy_unique_constraint_names.sql")

	if got := uniqueConstraintsOver(t, ctx, "base_task_definition_child",
		"child_definition_id", "container_definition_id"); len(got) != 0 {
		t.Fatalf("106 left unique constraints on (container, child): %v", got)
	}
	if got := uniqueConstraintsOver(t, ctx, "base_task_definition", "slug"); len(got) != 0 {
		t.Fatalf("106 left plain-unique slug constraints: %v", got)
	}

	// 106 must be safe to run twice: it is the second-run no-op case for every
	// instance that already migrated.
	runMigrationSQL(t, ctx, "106_legacy_unique_constraint_names.sql")
}
