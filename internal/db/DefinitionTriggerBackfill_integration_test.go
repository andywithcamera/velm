package db

// Slice 1 of the designer rework (Design Vision D6, Requirements AC-5 to AC-7):
// a definition always has a root trigger. A tree without one gains it when it is
// loaded, once, and never gains a second. Real Postgres, gated on
// TEST_DATABASE_URL; a skip is a broken run, not a pass.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func triggerTestSlug(t *testing.T, name string) string {
	t.Helper()
	return fmt.Sprintf("zz-trigger-%s-%d", name, time.Now().UnixNano())
}

func triggerTestDefinition(t *testing.T, ctx context.Context, slug string) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition (slug, display_name, is_container)
		VALUES ($1, $1, true)
		RETURNING _id::text`, slug).Scan(&id); err != nil {
		t.Fatalf("seed definition %q: %v", slug, err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(),
			`DELETE FROM base_task_definition_child WHERE container_definition_id = $1`, id)
		_, _ = Pool.Exec(context.Background(),
			`DELETE FROM base_task_definition WHERE _id = $1`, id)
	})
	return id
}

func countTriggers(t *testing.T, ctx context.Context, definitionID string) int {
	t.Helper()
	var count int
	if err := Pool.QueryRow(ctx, `
		SELECT count(*) FROM base_task_definition_child
		WHERE container_definition_id = $1 AND block_kind = 'trigger'`, definitionID).Scan(&count); err != nil {
		t.Fatalf("count triggers: %v", err)
	}
	return count
}

// AC-5 and AC-6: a zero-block definition loads with a trigger first, and the
// backfill is written rather than invented in the response.
func TestZeroBlockDefinitionGainsTrigger(t *testing.T) {
	ctx := schemaTestDB(t)
	id := triggerTestDefinition(t, ctx, triggerTestSlug(t, "zero-block"))

	blocks, err := LoadDefinitionDocument(ctx, id)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("loaded %d blocks, want the single trigger", len(blocks))
	}
	first := blocks[0]
	if first.Kind != "trigger" || first.ParentID != "" || first.Position != 0 {
		t.Fatalf("first block = %+v, want a root trigger at position 0", first)
	}
	if got := countTriggers(t, ctx, id); got != 1 {
		t.Fatalf("stored %d trigger rows after one open, want 1", got)
	}
}

// AC-7: opening twice does not add a second trigger.
func TestTriggerBackfillIsIdempotent(t *testing.T) {
	ctx := schemaTestDB(t)
	id := triggerTestDefinition(t, ctx, triggerTestSlug(t, "idempotent"))

	if _, err := LoadDefinitionDocument(ctx, id); err != nil {
		t.Fatalf("first load: %v", err)
	}
	blocks, err := LoadDefinitionDocument(ctx, id)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if got := countTriggers(t, ctx, id); got != 1 {
		t.Fatalf("stored %d trigger rows after two opens, want 1", got)
	}
	if len(blocks) != 1 || blocks[0].Kind != "trigger" {
		t.Fatalf("second load = %+v, want one trigger", blocks)
	}
}

// D6 also covers a legacy tree that has blocks but no trigger: the trigger is
// added ahead of them without reordering what is already stored.
func TestLegacyTreeWithoutTriggerGainsOne(t *testing.T) {
	ctx := schemaTestDB(t)
	slug := triggerTestSlug(t, "legacy")
	id := triggerTestDefinition(t, ctx, slug)
	childID := triggerTestDefinition(t, ctx, triggerTestSlug(t, "legacy-child"))
	if _, err := Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, sort_order, block_kind, position)
		VALUES ($1, $2, 10, 'task', 10)`, id, childID); err != nil {
		t.Fatalf("seed legacy task block: %v", err)
	}

	blocks, err := LoadDefinitionDocument(ctx, id)
	if err != nil {
		t.Fatalf("load legacy document: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("loaded %d blocks, want trigger plus the legacy task", len(blocks))
	}
	if blocks[0].Kind != "trigger" || blocks[0].Position != 0 {
		t.Fatalf("first block = %+v, want the trigger at position 0", blocks[0])
	}
	if blocks[1].Kind != "task" || blocks[1].Position <= blocks[0].Position {
		t.Fatalf("legacy block = %+v, want it after the trigger", blocks[1])
	}
}
