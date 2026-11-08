package db

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// Integration tests for the task engine composition paths.
// Gated on TEST_DATABASE_URL like the other real-Postgres tests.
func taskEngineTestDB(t *testing.T) context.Context {
	t.Helper()
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
	return ctx
}

func seedDefinition(ctx context.Context, t *testing.T, slug, display string, isContainer bool, extra string) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition (slug, display_name, is_container, spawn_rules`+extra+`)
		VALUES ($1, $2, $3, '[]'::jsonb)
		RETURNING _id::text`, slug, display, isContainer).Scan(&id); err != nil {
		t.Fatalf("seed definition %s: %v", slug, err)
	}
	return id
}

func seedChildLink(ctx context.Context, t *testing.T, containerDef, childDef string, sortOrder int, mapping map[string]any) {
	t.Helper()
	mj, _ := json.Marshal(mapping)
	if _, err := Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, sort_order, input_mapping)
		VALUES ($1, $2, $3, $4::jsonb)`, containerDef, childDef, sortOrder, string(mj)); err != nil {
		t.Fatalf("seed child link: %v", err)
	}
}

func taskVars(ctx context.Context, t *testing.T, taskID string) map[string]any {
	t.Helper()
	var vj []byte
	if err := Pool.QueryRow(ctx, `SELECT variables FROM base_task WHERE _id = $1`, taskID).Scan(&vj); err != nil {
		t.Fatalf("read task variables: %v", err)
	}
	vars := map[string]any{}
	_ = json.Unmarshal(vj, &vars)
	return vars
}

// TestContainerInputMappingAtParentStart is the #208 acceptance test: a
// container started via StartTask binds its children's input_mapping from
// the parent scope, and mapped values overwrite spawn-time inputs.
func TestContainerInputMappingAtParentStart(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	childDef := seedDefinition(ctx, t, "tm-child-"+suffix, "Child", false, "")
	containerDef := seedDefinition(ctx, t, "tm-container-"+suffix, "Container", true, "")
	seedChildLink(ctx, t, containerDef, childDef, 0, map[string]any{
		"entity_id": "$inputs.entity_id",
		"label":     "literal-label",
		"missing":   "$outputs.nothere.x",
	})

	actorID, groupID := seedActorAndGroup(ctx, t)

	// Spawn the container with requester inputs; the child spawns with empty inputs.
	parentID, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{"entity_id": "E-42"})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	kids, err := childTaskIDs(ctx, parentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 {
		t.Fatalf("expected 1 child, got %d", len(kids))
	}
	childID := kids[0]
	if v := taskVars(ctx, t, childID)["inputs"].(map[string]any); len(v) != 0 {
		t.Fatalf("child should spawn with empty inputs, got %v", v)
	}

	// Pre-seed a spawn-time input that the mapping must overwrite.
	vars := taskVars(ctx, t, childID)
	vars["inputs"] = map[string]any{"entity_id": "stale", "keep": "me"}
	vj, _ := json.Marshal(vars)
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, childID, string(vj)); err != nil {
		t.Fatal(err)
	}

	// 077 requires a group before a task leaves new; engine spawns have none.
	// Pending fix tracked under #209's depends-on.
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET assignment_group_id = $2, assigned_user_id = $3 WHERE _id = $1`,
		parentID, groupID, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET assignment_group_id = $2, assigned_user_id = $3 WHERE _id = $1`,
		childID, groupID, actorID); err != nil {
		t.Fatal(err)
	}

	if err := StartTask(ctx, parentID, actorID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	got := taskVars(ctx, t, childID)["inputs"].(map[string]any)
	if got["entity_id"] != "E-42" {
		t.Fatalf("mapped entity_id = %v, want E-42 (mapped values overwrite spawn-time)", got["entity_id"])
	}
	if got["label"] != "literal-label" {
		t.Fatalf("literal input_mapping not passed through: %v", got["label"])
	}
	if _, present := got["missing"]; present {
		t.Fatalf("unresolvable ref must be omitted, got %v", got["missing"])
	}
	if got["keep"] != "me" {
		t.Fatalf("spawn-time input not present in other keys: %v", got["keep"])
	}
}

// TestResolveScopeRefMultiSegment covers the $outputs.<child>.<field> path.
func TestResolveScopeRefMultiSegment(t *testing.T) {
	scope := map[string]any{
		"inputs":  map[string]any{"a": "A"},
		"outputs": map[string]any{"draft": map[string]any{"doc_id": "D-1"}},
		"locals":  map[string]any{"n": float64(3)},
	}
	if v, ok := resolveScopeRef(scope, "$outputs.draft.doc_id"); !ok || v != "D-1" {
		t.Fatalf("outputs.<slug>.<field> = %v %v", v, ok)
	}
	if v, ok := resolveScopeRef(scope, "$inputs.a"); !ok || v != "A" {
		t.Fatalf("inputs.x = %v %v", v, ok)
	}
	if v, ok := resolveScopeRef(scope, "plain"); !ok || v != "plain" {
		t.Fatalf("literal = %v %v", v, ok)
	}
	if _, ok := resolveScopeRef(scope, "$outputs.missing.x"); ok {
		t.Fatal("unresolvable ref must return ok=false")
	}
}

// seedActorAndGroup creates a user, a group, and the membership linking
// them, satisfying the 055/077 assignment invariants for engine tests.
func seedActorAndGroup(ctx context.Context, t *testing.T) (string, string) {
	t.Helper()
	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	var userID, groupID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email, password_hash)
		VALUES ($1, $2, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
		RETURNING _id::text`, "task-engine-test", suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _group (name) VALUES ($1) RETURNING _id::text`, "TE "+suffix).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if _, err := Pool.Exec(ctx, `
		INSERT INTO _group_membership (group_id, user_id) VALUES ($1, $2)`, groupID, userID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = Pool.Exec(bg, `DELETE FROM _group_membership WHERE group_id = $1`, groupID)
		_, _ = Pool.Exec(bg, `DELETE FROM _group WHERE _id = $1`, groupID)
		_, _ = Pool.Exec(bg, `DELETE FROM _user WHERE _id = $1`, userID)
	})
	return userID, groupID
}

func childTaskIDs(ctx context.Context, parentID string) ([]string, error) {
	rows, err := Pool.Query(ctx, `SELECT _id::text FROM base_task WHERE parent_task_id = $1 AND _deleted_at IS NULL ORDER BY number`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
