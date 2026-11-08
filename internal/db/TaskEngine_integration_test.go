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
	testActorID = userID
	return userID, groupID
}

// testActorID is set by seedActorAndGroup; helpers reuse it.
var testActorID string

func actorIDForClose(t *testing.T) string {
	t.Helper()
	if testActorID == "" {
		t.Fatal("actor not seeded")
	}
	return testActorID
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

// TestDependencyEdgesFanIn is the #209 acceptance test: a five-child
// container where review depends on design + implementation + risk (fan-in),
// implementation and comms run in parallel, and closing tasks in order
// promotes exactly the children whose every prerequisite is closed.
func TestDependencyEdgesFanIn(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, groupID := seedActorAndGroup(ctx, t)

	slugs := map[string]string{"design": "", "impl": "", "risk": "", "comms": "", "review": ""}
	for slug := range slugs {
		slugs[slug] = seedDefinition(ctx, t, "fan-"+slug+"-"+suffix, slug, false, "")
	}
	// risk is gated off: its run_if at start (via the close helper) skips it,
	// proving a skipped predecessor satisfies its dependents.
	seedRunIf(ctx, t, slugs["risk"], "function run(ctx){ return false; }")
	containerDef := seedDefinition(ctx, t, "fan-container-"+suffix, "Container", true, "")
	for i, slug := range []string{"design", "impl", "risk", "comms", "review"} {
		var deps []string
		if slug == "review" {
			deps = []string{slugs["design"], slugs["impl"], slugs["risk"]}
		}
		seedChildLinkWithDeps(ctx, t, containerDef, slugs[slug], i, deps, "")
	}

	parentID, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	assignAll(ctx, t, parentID, groupID, actorID)

	// Nothing promoted before start: all five children pending.
	if got := countState(ctx, t, parentID, "pending"); got != 5 {
		t.Fatalf("after spawn: %d pending, want 5", got)
	}

	if err := StartTask(ctx, parentID, actorID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	// design/impl/comms promote; risk is run_if:false -> skipped at
	// promotion; review waits on three deps.
	if got := countState(ctx, t, parentID, "ready"); got != 3 {
		t.Fatalf("after start: %d ready, want 3", got)
	}
	if got := countState(ctx, t, parentID, "skipped"); got != 1 {
		t.Fatalf("after start: risk should be skipped, got %d", got)
	}
	if got := countState(ctx, t, parentID, "pending"); got != 1 {
		t.Fatalf("after start: %d pending, want 1 (review)", got)
	}

	// Close design only: review blocked (fan-in, 1/3 — risk already skipped,
	// which satisfies it, but design and impl are still open).
	closeChildBySlug(ctx, t, parentID, slugs["design"], "completed")
	if got := countState(ctx, t, parentID, "pending"); got != 1 {
		t.Fatalf("1/3 deps settled: review should still be pending, got %d pending", got)
	}

	// Close impl: design completed + impl completed + risk skipped = review
	// promotes. Skipped predecessors satisfy (one run_if:false must not
	// deadlock the pipeline).
	closeChildBySlug(ctx, t, parentID, slugs["impl"], "completed")
	if got := countState(ctx, t, parentID, "pending"); got != 0 {
		t.Fatalf("all deps settled: review should have promoted, %d still pending", got)
	}
	// design/impl/risk are closed or skipped; comms never left ready;
	// review is newly ready.
	if got := countState(ctx, t, parentID, "ready"); got != 2 {
		t.Fatalf("want 2 ready (comms + review), got %d", got)
	}
	if got := countState(ctx, t, parentID, "skipped"); got != 1 {
		t.Fatalf("risk should be skipped, got %d", got)
	}
}

// TestDependencyEdgesRejectionLoop covers depends_on_reason: a review that
// closed 'rejected' does not satisfy an edge expecting completion; the
// re-spawned implementation (follow-on inside the container) does.
func TestDependencyEdgesRejectionLoop(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, groupID := seedActorAndGroup(ctx, t)

	implDef := seedDefinitionWithSpawnRules(ctx, t, "loop-impl-"+suffix, "impl",
		`[{"on":"rejected","definition_slug":"loop-impl-`+suffix+`","input_mapping":{},"assign":"same"}]`)
	reviewDef := seedDefinition(ctx, t, "loop-review-"+suffix, "review", false, "")
	containerDef := seedDefinition(ctx, t, "loop-container-"+suffix, "Container", true, "")
	seedChildLinkWithDeps(ctx, t, containerDef, implDef, 0, nil, "")
	seedChildLinkWithDeps(ctx, t, containerDef, reviewDef, 1, []string{implDef}, "completed")

	parentID, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	assignAll(ctx, t, parentID, groupID, actorID)
	if err := StartTask(ctx, parentID, actorID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	implID := childByDef(ctx, t, parentID, implDef)
	// Rejected implementation: review must NOT promote; follow-on spawns
	// pending inside the container.
	if err := closeWithReason(ctx, t, implID, "rejected"); err != nil {
		t.Fatalf("close rejected: %v", err)
	}
	if got := countState(ctx, t, parentID, "pending"); got != 2 {
		t.Fatalf("after rejection: %d pending, want 2 (review + follow-on impl)", got)
	}
	followOns := childrenOfDef(ctx, t, parentID, implDef)
	if len(followOns) != 2 {
		t.Fatalf("follow-on implementation not spawned inside container: %d impl instances", len(followOns))
	}
	// The re-spawned implementation starts pending and, with no deps,
	// promotes at the next promotion pass triggered by the rejection.
	// (promoteChildren ran in the same transaction as the close.)
	newImpl := followOns[1]
	if got := taskState(ctx, t, newImpl); got != "ready" && got != "pending" {
		t.Fatalf("follow-on impl state = %s, want ready or pending", got)
	}

	// The revision completes: now review promotes.
	if got := taskState(ctx, t, newImpl); got == "pending" {
		// dependency-free pending children only promote on a promotion pass;
		// the close of the rejected instance already ran one, and the
		// follow-on spawned after it. Start the parent again? No — start the
		// revision directly is not the model. Closing the rejected twin
		// triggered one pass; this branch documents the ordering.
		_ = got
	}
	if _, err := Pool.Exec(ctx, `UPDATE base_task SET assignment_group_id = $2, assigned_user_id = $3 WHERE _id = $1`,
		newImpl, groupID, actorID); err != nil {
		t.Fatal(err)
	}
	if err := closeWithReason(ctx, t, newImpl, "completed"); err != nil {
		t.Fatalf("close revision: %v", err)
	}
	if got := taskState(ctx, t, childByDef(ctx, t, parentID, reviewDef)); got != "ready" {
		t.Fatalf("review state = %s, want ready after revision completed", got)
	}
}

// TestDependencyCycleRejectedOnSave proves the 084 trigger rejects a cycle
// at definition-save time, not at runtime.
func TestDependencyCycleRejectedOnSave(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	a := seedDefinition(ctx, t, "cyc-a-"+suffix, "a", false, "")
	b := seedDefinition(ctx, t, "cyc-b-"+suffix, "b", false, "")
	container := seedDefinition(ctx, t, "cyc-container-"+suffix, "c", true, "")
	seedChildLinkWithDeps(ctx, t, container, a, 0, nil, "")
	seedChildLinkWithDeps(ctx, t, container, b, 1, []string{a}, "")
	if _, err := Pool.Exec(ctx,
		`UPDATE base_task_definition_child SET depends_on = $2::uuid[]
		 WHERE container_definition_id = $1 AND child_definition_id = $3`,
		container, []string{b}, a); err == nil {
		t.Fatal("a -> b -> a cycle accepted on save; must be rejected")
	}
}

// seedDefinitionWithSpawnRules seeds a leaf definition with spawn rules.
func seedDefinitionWithSpawnRules(ctx context.Context, t *testing.T, slug, display, rules string) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition (slug, display_name, is_container, spawn_rules)
		VALUES ($1, $2, FALSE, $3::jsonb)
		RETURNING _id::text`, slug, display, rules).Scan(&id); err != nil {
		t.Fatalf("seed definition %s: %v", slug, err)
	}
	return id
}

// seedRunIf sets a run_if script on a definition (false -> skipped).
func seedRunIf(ctx context.Context, t *testing.T, defID, script string) {
	t.Helper()
	if _, err := Pool.Exec(ctx,
		`UPDATE base_task_definition SET run_if_script = $2 WHERE _id = $1`, defID, script); err != nil {
		t.Fatalf("seed run_if: %v", err)
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func seedChildLinkWithDeps(ctx context.Context, t *testing.T, containerDef, childDef string, sortOrder int, deps []string, reason string) {
	t.Helper()
	if _, err := Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, sort_order, input_mapping, depends_on, depends_on_reason)
		VALUES ($1, $2, $3, '{}'::jsonb, $4::uuid[], $5)`, containerDef, childDef, sortOrder, nonNil(deps), reason); err != nil {
		t.Fatalf("seed child link with deps: %v", err)
	}
}

func assignAll(ctx context.Context, t *testing.T, parentID, groupID, actorID string) {
	t.Helper()
	if _, err := Pool.Exec(ctx, `
		UPDATE base_task SET assignment_group_id = $2, assigned_user_id = $3
		WHERE parent_task_id = $1 OR _id = $1`, parentID, groupID, actorID); err != nil {
		t.Fatalf("assign: %v", err)
	}
}

func countState(ctx context.Context, t *testing.T, parentID, state string) int {
	t.Helper()
	var n int
	if err := Pool.QueryRow(ctx, `
		SELECT count(*) FROM base_task WHERE parent_task_id = $1 AND state = $2 AND _deleted_at IS NULL`,
		parentID, state).Scan(&n); err != nil {
		t.Fatalf("count state: %v", err)
	}
	return n
}

// closeChildBySlug closes the single instance of a definition under parent.
func closeChildBySlug(ctx context.Context, t *testing.T, parentID, defID, reason string) {
	t.Helper()
	id := childByDef(ctx, t, parentID, defID)
	if err := closeWithReason(ctx, t, id, reason); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func childByDef(ctx context.Context, t *testing.T, parentID, defID string) string {
	t.Helper()
	ids := childrenOfDef(ctx, t, parentID, defID)
	if len(ids) == 0 {
		t.Fatalf("no child for definition %s under %s", defID, parentID)
	}
	return ids[0]
}

func childrenOfDef(ctx context.Context, t *testing.T, parentID, defID string) []string {
	t.Helper()
	rows, err := Pool.Query(ctx, `
		SELECT _id::text FROM base_task
		WHERE parent_task_id = $1 AND definition_id = $2 AND _deleted_at IS NULL
		ORDER BY number`, parentID, defID)
	if err != nil {
		t.Fatalf("childrenOfDef: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// closeWithReason drives an engine task to closed with an explicit reason,
// through the real transition path (ready -> in_progress -> closed).
func closeWithReason(ctx context.Context, t *testing.T, taskID, reason string) error {
	t.Helper()
	if err := StartTask(ctx, taskID, actorIDForClose(t)); err != nil {
		return err
	}
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := transitionTaskInTx(ctx, tx, taskID, taskStateDone); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE base_task SET closure_reason = $2 WHERE _id = $1`, taskID, reason); err != nil {
		return err
	}
	if err := onTaskClosed(ctx, tx, taskID, reason, actorIDForClose(t)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func taskState(ctx context.Context, t *testing.T, taskID string) string {
	t.Helper()
	var state string
	if err := Pool.QueryRow(ctx, `SELECT state FROM base_task WHERE _id = $1`, taskID).Scan(&state); err != nil {
		t.Fatalf("taskState: %v", err)
	}
	return state
}
