package db

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
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
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			checksum TEXT
		)
	`); err != nil {
		return fmt.Errorf("create _schema_migration: %w", err)
	}
	// Checksums arrived after the table did, so add the column on the way in
	// rather than in a migration file: the column has to exist before the
	// runner can record (or verify) a single checksum.
	if _, err := Pool.Exec(ctx, `ALTER TABLE _schema_migration ADD COLUMN IF NOT EXISTS checksum TEXT`); err != nil {
		return fmt.Errorf("add _schema_migration.checksum: %w", err)
	}

	applied, err := loadAppliedMigrations(ctx)
	if err != nil {
		return err
	}

	files, err := loadMigrationFiles()
	if err != nil {
		return err
	}

	versions := orderMigrationFiles(files)

	// Refuse to run against a schema whose applied history no longer matches
	// the code. A mismatch means someone edited a migration in place: this
	// database carries the old effect, a fresh one would carry the new, and
	// both claim the same version. Continuing would run code against a schema
	// it does not expect.
	backfill, err := verifyAppliedMigrationChecksums(applied, files)
	if err != nil {
		return err
	}
	if err := recordMigrationChecksums(ctx, backfill); err != nil {
		return err
	}

	const systemAppMigration = "042_system_app.sql"

	for _, version := range versions {
		if _, ok := applied[version]; ok {
			continue
		}
		if err := applyMigration(ctx, version, migrationChecksum(files[version])); err != nil {
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
	// Must run before the sync passes below: each of them validates whatever
	// definition is stored, so a stale empty role list would abort boot here.
	if err := MigrateEntryPointRoles(ctx); err != nil {
		return err
	}
	if err := SyncSystemAppDefinitionWithPhysicalTables(ctx); err != nil {
		return err
	}
	if err := SyncBaseAppDefinitionTaskModel(ctx); err != nil {
		return err
	}
	if err := SyncTaskDefinitionCatalog(ctx); err != nil {
		return err
	}
	if err := SyncBaseObservabilityDefinition(ctx); err != nil {
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

// appliedMigration is one row of _schema_migration. Checksum is nil for rows
// written before checksums existed.
type appliedMigration struct {
	checksum *string
}

// loadMigrationFiles reads every embedded migration, keyed by filename. The
// contents are returned so callers checksum exactly the bytes that would run.
// orderMigrationFiles returns the migration filenames in the order they apply:
// lexicographic by filename, which is not the same thing as numeric order.
//
// The leading number is a hint about when a migration was written, not the key
// that sequences it. Two files can carry the same number — 046 and 081 both do,
// and neither can be renumbered now that they are applied somewhere, because an
// applied file that changes name is a checksum failure at boot. So when numbers
// collide, the rest of the filename decides, and no code may assume otherwise.
// TestMigrationNumbersAreUnique keeps new collisions out.
func orderMigrationFiles(files map[string][]byte) []string {
	versions := make([]string, 0, len(files))
	for version := range files {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return versions
}

func loadMigrationFiles() (map[string][]byte, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migration files: %w", err)
	}

	files := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		files[name] = content
	}
	return files, nil
}

// migrationChecksum fingerprints a migration file's exact bytes.
func migrationChecksum(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// verifyAppliedMigrationChecksums compares what each applied row recorded with
// what the embedded file contains now. It returns the versions whose row has no
// recorded checksum; the caller backfills those on first boot after this
// change, since a row applied before checksums existed has no better truth than
// the file present at that moment.
//
// Every mismatch is fatal and names the file: the operator has to decide which
// side is wrong, and an automatic choice would silently damage one of them.
func verifyAppliedMigrationChecksums(applied map[string]appliedMigration, files map[string][]byte) ([]string, error) {
	versions := make([]string, 0, len(applied))
	for version := range applied {
		versions = append(versions, version)
	}
	sort.Strings(versions)

	var backfill []string
	for _, version := range versions {
		content, embedded := files[version]
		if !embedded {
			return nil, fmt.Errorf("migration %s is recorded as applied but no longer ships with this build: restore the file, or add a migration that undoes it", version)
		}

		recorded := applied[version].checksum
		if recorded == nil {
			backfill = append(backfill, version)
			continue
		}

		expected := migrationChecksum(content)
		if *recorded != expected {
			return nil, fmt.Errorf("migration %s has changed since it was applied: recorded sha256 %s, file is now %s. Migrations are append-only; revert the file and add a new migration instead", version, *recorded, expected)
		}
	}
	return backfill, nil
}

// recordMigrationChecksums persists the checksum for rows that predate the
// column. It only ever fills a NULL, never overwrites a recorded value.
func recordMigrationChecksums(ctx context.Context, versions []string) error {
	for _, version := range versions {
		content, err := migrationsFS.ReadFile("migrations/" + version)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		if _, err := Pool.Exec(ctx,
			`UPDATE _schema_migration SET checksum = $2 WHERE version = $1 AND checksum IS NULL`,
			version, migrationChecksum(content)); err != nil {
			return fmt.Errorf("backfill checksum for %s: %w", version, err)
		}
	}
	return nil
}

// AppliedSchemaVersion reports the highest migration this database has applied,
// so /version can put code identity and schema identity side by side.
func AppliedSchemaVersion(ctx context.Context) (string, error) {
	if Pool == nil {
		return "", fmt.Errorf("database pool is not initialized")
	}

	var version string
	err := Pool.QueryRow(ctx, `SELECT version FROM _schema_migration ORDER BY version DESC LIMIT 1`).Scan(&version)
	if err != nil {
		return "", fmt.Errorf("read applied schema version: %w", err)
	}
	return version, nil
}

func loadAppliedMigrations(ctx context.Context) (map[string]appliedMigration, error) {
	rows, err := Pool.Query(ctx, `SELECT version, checksum FROM _schema_migration`)
	if err != nil {
		return nil, fmt.Errorf("load applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]appliedMigration)
	for rows.Next() {
		var version string
		var checksum *string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("scan migration version: %w", err)
		}
		applied[version] = appliedMigration{checksum: checksum}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration versions: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, version string, checksum string) error {
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
	if _, err := tx.Exec(ctx, `INSERT INTO _schema_migration (version, checksum) VALUES ($1, $2)`, version, checksum); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}

	return nil
}
