package db

import (
	"strings"
	"testing"
)

func TestMigrationChecksumIsStableHexSHA256(t *testing.T) {
	// sha256("SELECT 1;\n"), computed independently of this package.
	const want = "b4e0497804e46e0a0b0b8c31975b062152d551bac49c3c2e80932567b4085dcd"

	got := migrationChecksum([]byte("SELECT 1;\n"))
	if got != want {
		t.Fatalf("checksum = %q, want %q", got, want)
	}
	if again := migrationChecksum([]byte("SELECT 1;\n")); again != got {
		t.Fatalf("checksum is not deterministic: %q then %q", got, again)
	}
	if other := migrationChecksum([]byte("SELECT 2;\n")); other == got {
		t.Fatalf("different content produced the same checksum")
	}
}

func TestLoadMigrationFilesReadsEmbeddedSet(t *testing.T) {
	files, err := loadMigrationFiles()
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations embedded")
	}
	if _, ok := files["042_system_app.sql"]; !ok {
		t.Fatal("042_system_app.sql missing from the embedded set")
	}
	for name, content := range files {
		if !strings.HasSuffix(name, ".sql") {
			t.Fatalf("non-SQL file %q loaded as a migration", name)
		}
		if len(content) == 0 {
			t.Fatalf("migration %q is empty", name)
		}
	}
}

func TestVerifyAppliedMigrationChecksumsAcceptsMatchingRows(t *testing.T) {
	files := map[string][]byte{"001_init.sql": []byte("CREATE TABLE t (id int);")}
	sum := migrationChecksum(files["001_init.sql"])
	applied := map[string]appliedMigration{"001_init.sql": {checksum: &sum}}

	backfill, err := verifyAppliedMigrationChecksums(applied, files)
	if err != nil {
		t.Fatalf("matching row rejected: %v", err)
	}
	if len(backfill) != 0 {
		t.Fatalf("backfill = %v, want none", backfill)
	}
}

func TestVerifyAppliedMigrationChecksumsRejectsEditedMigration(t *testing.T) {
	files := map[string][]byte{"001_init.sql": []byte("CREATE TABLE t (id int);")}
	stale := migrationChecksum([]byte("CREATE TABLE t (id serial);"))
	applied := map[string]appliedMigration{"001_init.sql": {checksum: &stale}}

	_, err := verifyAppliedMigrationChecksums(applied, files)
	if err == nil {
		t.Fatal("an edited applied migration was accepted")
	}
	message := err.Error()
	for _, want := range []string{"001_init.sql", stale, migrationChecksum(files["001_init.sql"]), "append-only"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not mention %q", message, want)
		}
	}
}

func TestVerifyAppliedMigrationChecksumsBackfillsRowsWithoutChecksum(t *testing.T) {
	files := map[string][]byte{
		"001_init.sql": []byte("CREATE TABLE t (id int);"),
		"002_more.sql": []byte("ALTER TABLE t ADD COLUMN name text;"),
	}
	sum := migrationChecksum(files["002_more.sql"])
	applied := map[string]appliedMigration{
		"001_init.sql": {checksum: nil},
		"002_more.sql": {checksum: &sum},
	}

	backfill, err := verifyAppliedMigrationChecksums(applied, files)
	if err != nil {
		t.Fatalf("pre-checksum row rejected: %v", err)
	}
	if len(backfill) != 1 || backfill[0] != "001_init.sql" {
		t.Fatalf("backfill = %v, want [001_init.sql]", backfill)
	}
}

func TestVerifyAppliedMigrationChecksumsRejectsMissingFile(t *testing.T) {
	files := map[string][]byte{"001_init.sql": []byte("CREATE TABLE t (id int);")}
	applied := map[string]appliedMigration{"999_vanished.sql": {checksum: nil}}

	_, err := verifyAppliedMigrationChecksums(applied, files)
	if err == nil {
		t.Fatal("an applied migration with no file was accepted")
	}
	if !strings.Contains(err.Error(), "999_vanished.sql") {
		t.Fatalf("error %q does not name the missing migration", err)
	}
}

func TestVerifyAppliedMigrationChecksumsIgnoresUnappliedFiles(t *testing.T) {
	files := map[string][]byte{
		"001_init.sql": []byte("CREATE TABLE t (id int);"),
		"002_new.sql":  []byte("ALTER TABLE t ADD COLUMN name text;"),
	}
	sum := migrationChecksum(files["001_init.sql"])
	applied := map[string]appliedMigration{"001_init.sql": {checksum: &sum}}

	backfill, err := verifyAppliedMigrationChecksums(applied, files)
	if err != nil {
		t.Fatalf("pending migration rejected: %v", err)
	}
	if len(backfill) != 0 {
		t.Fatalf("backfill = %v, want none", backfill)
	}
}
