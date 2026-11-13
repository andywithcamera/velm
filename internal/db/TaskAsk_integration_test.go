package db

import (
	"fmt"
	"testing"
	"time"
)

// TestSpawnedTaskCarriesDefinitionAsk is the #225 acceptance test: a task
// spawned from a definition with ask='test' lands ask='test', and a task
// created outside the engine keeps the intake default.
func TestSpawnedTaskCarriesDefinitionAsk(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	defID := seedDefinition(ctx, t, "tm-ask-"+suffix, "Run Tests", false, "")
	if _, err := Pool.Exec(ctx, `UPDATE base_task_definition SET ask = 'test' WHERE _id = $1`, defID); err != nil {
		t.Fatalf("set definition ask: %v", err)
	}
	actorID, _ := seedActorAndGroup(ctx, t)

	taskID, err := SpawnTaskInstance(ctx, Pool, defID, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("SpawnTaskInstance: %v", err)
	}
	var ask string
	if err := Pool.QueryRow(ctx, `SELECT ask FROM base_task WHERE _id = $1`, taskID).Scan(&ask); err != nil {
		t.Fatalf("read ask: %v", err)
	}
	if ask != "test" {
		t.Fatalf("spawned task ask = %q, want %q", ask, "test")
	}

	// An explicit request ask overrides the definition.
	overrideID, err := SpawnTaskInstance(ctx, Pool, defID, "", actorID, map[string]any{"ask": "review"})
	if err != nil {
		t.Fatalf("SpawnTaskInstance with ask input: %v", err)
	}
	if err := Pool.QueryRow(ctx, `SELECT ask FROM base_task WHERE _id = $1`, overrideID).Scan(&ask); err != nil {
		t.Fatalf("read override ask: %v", err)
	}
	if ask != "review" {
		t.Fatalf("override ask = %q, want %q", ask, "review")
	}
}

// TestBaseTaskAskConstraintRejectsUnknownValues guards the CHECK: an
// out-of-enum ask is a programming error, not a free-text label.
func TestBaseTaskAskConstraintRejectsUnknownValues(t *testing.T) {
	ctx := taskEngineTestDB(t)

	actorID, _ := seedActorAndGroup(ctx, t)
	var id string
	err := Pool.QueryRow(ctx, `
		INSERT INTO base_task (number, title, ask, requested_by_user_id)
		VALUES ('', 'ask constraint', 'nonsense', $1) RETURNING _id::text`, actorID).Scan(&id)
	if err == nil {
		t.Fatalf("insert with ask='nonsense' succeeded, want CHECK violation")
	}
}
