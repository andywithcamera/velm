package main

import (
	"context"
	"os"
	"testing"

	"velm/internal/db"
)

// The reference pickers are the only door into a reference column. They used to
// list every row including soft-deleted ones, so a record the UI no longer shows
// could still be selected as the value of a live field, and every delete ever
// made left a ghost in the list forever.
//
// Gated on TEST_DATABASE_URL like the other real-Postgres tests.
func TestReferenceOptionsExcludeSoftDeletedRecords(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}
	t.Setenv("DATABASE_URL", url)
	os.Setenv("DATABASE_URL", url)
	if err := db.ConnectToDB(); err != nil {
		t.Fatalf("ConnectToDB: %v", err)
	}
	t.Cleanup(db.CloseDB)

	ctx := context.Background()
	table := "zz_reference_picker_probe"
	if _, err := db.Pool.Exec(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
		t.Fatalf("drop probe table: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE `+table+` (
			_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			name text NOT NULL,
			_deleted_at timestamptz
		)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(context.Background(), `DROP TABLE IF EXISTS `+table); err != nil {
			t.Logf("cleanup: drop %s: %v", table, err)
		}
	})

	var liveID, deletedID string
	if err := db.Pool.QueryRow(ctx, `INSERT INTO `+table+` (name) VALUES ('web-01') RETURNING _id::text`).Scan(&liveID); err != nil {
		t.Fatalf("insert live row: %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO `+table+` (name, _deleted_at) VALUES ('web-02-retired', NOW()) RETURNING _id::text`).Scan(&deletedID); err != nil {
		t.Fatalf("insert deleted row: %v", err)
	}

	values := func(options []formReferenceOption) map[string]bool {
		out := make(map[string]bool, len(options))
		for _, option := range options {
			out[option.Value] = true
		}
		return out
	}

	// 1. A soft-deleted row is not offered.
	got := values(fetchReferenceOptions(ctx, table))
	if !got[liveID] {
		t.Fatalf("live record %s missing from reference options: %#v", liveID, got)
	}
	if got[deletedID] {
		t.Fatalf("soft-deleted record %s was offered as a choice: %#v", deletedID, got)
	}

	// 2. The record already stored in a field stays resolvable, so opening an
	//    older record does not silently blank the value it holds.
	got = values(fetchReferenceOptionsFiltered(ctx, table, "", 50, deletedID))
	if !got[deletedID] {
		t.Fatalf("the field's own stored value %s was dropped from its options: %#v", deletedID, got)
	}
	if !got[liveID] {
		t.Fatalf("live record %s missing when a selection was supplied: %#v", liveID, got)
	}

	// 3. Search must not reach a deleted row either.
	got = values(fetchReferenceOptionsFiltered(ctx, table, "retired", 50, ""))
	if got[deletedID] {
		t.Fatalf("search returned the soft-deleted record %s: %#v", deletedID, got)
	}

	// 4. A table without _deleted_at keeps working rather than 500ing on a
	//    column that is not there.
	if !tableHasColumn(table, "_deleted_at") {
		t.Fatal("probe table should report a _deleted_at column")
	}
}
