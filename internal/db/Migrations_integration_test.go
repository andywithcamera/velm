package db

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestMigrationChecksumLifecycle exercises the checksum path against a REAL
// PostgreSQL database, which the pure unit tests cannot: the column actually
// existing, a fresh run recording checksums, a pre-checksum row being
// backfilled, and an edited migration aborting the next boot.
//
// Gated on TEST_DATABASE_URL; CI's `test` job supplies a Postgres service.
func TestMigrationChecksumLifecycle(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}

	t.Setenv("DATABASE_URL", url)
	os.Setenv("DATABASE_URL", url)
	if err := ConnectToDB(); err != nil {
		t.Fatalf("ConnectToDB: %v", err)
	}
	t.Cleanup(CloseDB)

	ctx := context.Background()
	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	files, err := loadMigrationFiles()
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}

	// 1. Every embedded migration is recorded once, with a checksum.
	rows, err := Pool.Query(ctx, `SELECT version, checksum FROM _schema_migration`)
	if err != nil {
		t.Fatalf("query _schema_migration: %v", err)
	}
	recorded := map[string]*string{}
	for rows.Next() {
		var version string
		var checksum *string
		if err := rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		if _, duplicate := recorded[version]; duplicate {
			rows.Close()
			t.Fatalf("migration %s recorded twice", version)
		}
		recorded[version] = checksum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate rows: %v", err)
	}

	for version, content := range files {
		stored, ok := recorded[version]
		if !ok {
			t.Fatalf("migration %s not recorded as applied", version)
		}
		if stored == nil {
			t.Fatalf("migration %s has no checksum after a clean run", version)
		}
		if want := migrationChecksum(content); *stored != want {
			t.Fatalf("migration %s checksum = %s, want %s", version, *stored, want)
		}
	}

	// 2. /version's schema value names the highest applied migration.
	highest, err := AppliedSchemaVersion(ctx)
	if err != nil {
		t.Fatalf("AppliedSchemaVersion: %v", err)
	}
	if highest == "" {
		t.Fatal("AppliedSchemaVersion returned nothing on a migrated database")
	}
	if _, ok := files[highest]; !ok {
		t.Fatalf("AppliedSchemaVersion = %q, which is not an embedded migration", highest)
	}

	// 3. A row that predates checksums gets one on the next boot, without
	//    rerunning the migration.
	const legacy = "001_security_core.sql"
	if _, err := Pool.Exec(ctx, `UPDATE _schema_migration SET checksum = NULL WHERE version = $1`, legacy); err != nil {
		t.Fatalf("null out checksum for %s: %v", legacy, err)
	}
	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations with a NULL checksum: %v", err)
	}
	var backfilled *string
	if err := Pool.QueryRow(ctx, `SELECT checksum FROM _schema_migration WHERE version = $1`, legacy).Scan(&backfilled); err != nil {
		t.Fatalf("read backfilled checksum: %v", err)
	}
	if backfilled == nil {
		t.Fatalf("checksum for %s was not backfilled", legacy)
	}
	if *backfilled != migrationChecksum(files[legacy]) {
		t.Fatalf("backfilled checksum = %s, want %s", *backfilled, migrationChecksum(files[legacy]))
	}

	// 4. A recorded checksum that no longer matches the file aborts boot and
	//    names the migration.
	if _, err := Pool.Exec(ctx, `UPDATE _schema_migration SET checksum = $2 WHERE version = $1`, legacy, "edited-in-place"); err != nil {
		t.Fatalf("corrupt checksum for %s: %v", legacy, err)
	}
	err = RunMigrations(ctx)
	if err == nil {
		t.Fatalf("boot continued with a divergent migration checksum")
	}
	if !strings.Contains(err.Error(), legacy) {
		t.Fatalf("error %q does not name %s", err, legacy)
	}

	// Restore the row so a shared database is left as it was found.
	if _, err := Pool.Exec(ctx, `UPDATE _schema_migration SET checksum = $2 WHERE version = $1`, legacy, migrationChecksum(files[legacy])); err != nil {
		t.Fatalf("restore checksum for %s: %v", legacy, err)
	}
}
