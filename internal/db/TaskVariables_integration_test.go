package db

import (
	"context"
	"os"
	"testing"
)

// The reason the scope stopped being one document (#385): two writers that
// each loaded a copy and put the whole thing back would overwrite each
// other. Here both load the same task before either writes, then each writes
// a different name. Both names must survive — under the old column, the
// second write would have carried the first writer's view of the document
// and dropped the change.
func TestTaskVariablesDisjointWritesBothSurvive(t *testing.T) {
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

	taskID := seedBareTask(t, ctx)

	first, err := LoadTaskVars(ctx, Pool, taskID)
	if err != nil {
		t.Fatalf("load first: %v", err)
	}
	second, err := LoadTaskVars(ctx, Pool, taskID)
	if err != nil {
		t.Fatalf("load second: %v", err)
	}

	firstOutputs, _ := first.Values["outputs"].(map[string]any)
	firstOutputs["stage_a"] = "done"
	if err := first.Save(ctx, Pool); err != nil {
		t.Fatalf("save first: %v", err)
	}

	secondOutputs, _ := second.Values["outputs"].(map[string]any)
	secondOutputs["stage_b"] = "done"
	if err := second.Save(ctx, Pool); err != nil {
		t.Fatalf("save second: %v", err)
	}

	got, err := LoadTaskVars(ctx, Pool, taskID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	outputs, _ := got.Values["outputs"].(map[string]any)
	if outputs["stage_a"] != "done" {
		t.Fatalf("stage_a was lost by the second write: %#v", outputs)
	}
	if outputs["stage_b"] != "done" {
		t.Fatalf("stage_b is missing: %#v", outputs)
	}
}

// A value that is no longer in the scope must stop being stored: a deleted
// name that lingers is worse than one that never existed, because a filter
// would still find it.
func TestTaskVariablesRemovedNameIsDeleted(t *testing.T) {
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

	taskID := seedBareTask(t, ctx)
	if err := WriteTaskVariables(ctx, Pool, taskID, map[string]any{
		"inputs": map[string]any{"keep": "a", "drop": "b"},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	vars, err := LoadTaskVars(ctx, Pool, taskID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	inputs, _ := vars.Values["inputs"].(map[string]any)
	delete(inputs, "drop")
	if err := vars.Save(ctx, Pool); err != nil {
		t.Fatalf("save: %v", err)
	}

	var stored int
	if err := Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM _task_variables
		WHERE task_id = $1 AND scope = 'input' AND name = 'drop'`, taskID).Scan(&stored); err != nil {
		t.Fatalf("count: %v", err)
	}
	if stored != 0 {
		t.Fatalf("removed input is still stored (%d rows)", stored)
	}

	// And a typed projection drives a filter: the reason the rows exist.
	var matched int
	if err := Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM _task_variables
		WHERE scope = 'input' AND name = 'keep' AND value_text = 'a'`).Scan(&matched); err != nil {
		t.Fatalf("filter: %v", err)
	}
	if matched != 1 {
		t.Fatalf("filter on scope/name/value_text matched %d rows, want 1", matched)
	}
}

// seedBareTask inserts a task row with no variables, for tests about storage
// rather than about the engine.
func seedBareTask(t *testing.T, ctx context.Context) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task (number, title, work_type, state)
		VALUES ('', 'variables storage ' || gen_random_uuid(), 'TASK', 'pending')
		RETURNING _id::text`).Scan(&id); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM base_task WHERE _id = $1`, id)
	})
	return id
}
