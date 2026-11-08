package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestUserRoleTableStaysDropped pins #199 R4 and the regression that nearly
// shipped with it.
//
// 083_drop_user_role.sql drops _user_role, but the system app definition still
// declares it: 042 seeds the definition, and every instance that ran 042 holds
// its own copy. ensureNamedSystemPhysicalTables recreates any declared system
// table that is missing physically, so the drop was undone on the next boot and
// the table came back empty. removedSystemTables is what stops that, and this
// test is what keeps it stopped.
//
// It also proves the backfill: a direct grant that exists before 083 must still
// be an admin grant after it, through group membership.
func TestUserRoleTableStaysDropped(t *testing.T) {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}

	ctx := context.Background()
	dbName := fmt.Sprintf("velm_userrole_%d", time.Now().UnixNano())

	adminConn, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create scratch database: %v", err)
	}

	scratchURL := databaseURLFor(t, baseURL, dbName)
	t.Setenv("DATABASE_URL", scratchURL)
	os.Setenv("DATABASE_URL", scratchURL)
	if err := ConnectToDB(); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("ConnectToDB: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		CloseDB()
		if _, err := adminConn.Exec(bg, "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("cleanup: drop scratch database %s: %v", dbName, err)
		}
		_ = adminConn.Close(bg)
	})

	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// 1. The table is gone.
	var exists bool
	if err := Pool.QueryRow(ctx, `SELECT to_regclass('_user_role') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	if exists {
		t.Fatal("_user_role exists after migrations; 083 did not drop it")
	}

	// 2. The system app definition no longer declares it, so a later sync cannot
	//    recreate it. This is the check that would have caught the regression.
	app, err := GetActiveAppByName(ctx, "system")
	if err != nil {
		t.Fatalf("GetActiveAppByName: %v", err)
	}
	for _, definition := range []*AppDefinition{app.Definition, app.DraftDefinition} {
		if definition == nil {
			continue
		}
		for _, table := range definition.Tables {
			if normaliseTableName(table.Name) == "_user_role" {
				t.Fatalf("system app definition still declares _user_role; the sync will recreate it")
			}
		}
	}

	// 3. Running the sync chain again changes nothing: the table stays dropped.
	if err := SyncSystemAppDefinitionWithPhysicalTables(ctx); err != nil {
		t.Fatalf("SyncSystemAppDefinitionWithPhysicalTables: %v", err)
	}
	if err := Pool.QueryRow(ctx, `SELECT to_regclass('_user_role') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("to_regclass after sync: %v", err)
	}
	if exists {
		t.Fatal("_user_role was recreated by the system app sync")
	}
}
