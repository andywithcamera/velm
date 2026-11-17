package db

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Requirements AC-17 to AC-20. A definition authored before 098 -- built here
// with raw SQL that names none of 098's columns -- must spawn exactly the same
// tree before and after 098 is applied.
//
// The test does not compare against a hand-written expectation, because a
// hand-written expectation is a second model of the engine and can be wrong in
// the same direction as the code. It builds a scratch database at 097, spawns
// the definition, takes a whole-tree snapshot, applies 098, spawns it again,
// and requires the two snapshots to be equal. The editor is never involved: no
// cmd/server import and no AddDefinitionStep call. That is AC-18.

// migTestApplyUpTo applies every embedded migration lexicographically before
// wantVersion, reproducing RunMigrations' one special case (042's system tables
// must exist before 043+ reference _user). It stops before wantVersion so the
// caller can observe the schema the migration is about to change.
func migTestApplyUpTo(t *testing.T, ctx context.Context, wantVersion string) {
	t.Helper()
	files, err := loadMigrationFiles()
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	versions := orderMigrationFiles(files)
	for _, version := range versions {
		if version >= wantVersion {
			break
		}
		if _, err := Pool.Exec(ctx, string(files[version])); err != nil {
			t.Fatalf("apply %s: %v", version, err)
		}
		if version == "042_system_app.sql" {
			if err := EnsureRequiredSystemTablesAfter042(ctx); err != nil {
				t.Fatalf("system tables after 042: %v", err)
			}
		}
	}
}

func migTestApplyOne(t *testing.T, ctx context.Context, version string) {
	t.Helper()
	files, err := loadMigrationFiles()
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	content, ok := files[version]
	if !ok {
		t.Fatalf("migration %s is not embedded", version)
	}
	if _, err := Pool.Exec(ctx, string(content)); err != nil {
		t.Fatalf("apply %s: %v", version, err)
	}
}

func seedPreMigrationDefinitions(t *testing.T, ctx context.Context) {
	t.Helper()
	var root, stage, leaf string
	for _, d := range []struct {
		slug      string
		container bool
		target    *string
	}{
		{"pre098_root", true, &root},
		{"pre098_stage", true, &stage},
		{"pre098_leaf", false, &leaf},
	} {
		if err := Pool.QueryRow(ctx, `
			INSERT INTO base_task_definition (slug, display_name, is_container)
			VALUES ($1, $2, $3) RETURNING _id::text`,
			d.slug, strings.ReplaceAll(d.slug, "_", " "), d.container).Scan(d.target); err != nil {
			t.Fatalf("seed %s: %v", d.slug, err)
		}
	}

	// A pre-098 row: only the columns 078 and 084 knew about.
	if _, err := Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, sort_order, input_mapping)
		VALUES ($1, $2, 10, '{"server_name":"$inputs.server_name"}'::jsonb)`, root, stage); err != nil {
		t.Fatalf("seed root->stage: %v", err)
	}
	if _, err := Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, sort_order, input_mapping)
		VALUES ($1, $2, 10, '{"server_name":"$inputs.server_name"}'::jsonb)`, stage, leaf); err != nil {
		t.Fatalf("seed stage->leaf: %v", err)
	}
	// A dependent sibling: it must stay pending because the stage never
	// closes. State per row is half of what AC-17 compares.
	if _, err := Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, sort_order, depends_on, depends_on_reason)
		VALUES ($1, $2, 20, ARRAY[$3::uuid], '')`, root, leaf, stage); err != nil {
		t.Fatalf("seed root->leaf dependency: %v", err)
	}
}

// spawnSnapshot is the whole tree below one root, in a form that can be
// compared across two spawns: ids, numbers and timestamps are the only columns
// AC-18 permits to differ, and parent links and titles carry ids/numbers, so
// they are replaced by tree-relative values.
type spawnSnapshot struct {
	Root  string
	Nodes []snapshotNode
}

type snapshotNode struct {
	Parent int
	Row    map[string]any
}

var taskNumberInTitle = regexp.MustCompile(`\((?:TASK|ENT)-[0-9]+\)`)

func takeSpawnSnapshot(t *testing.T, ctx context.Context, rootTaskID string) spawnSnapshot {
	t.Helper()
	rows, err := Pool.Query(ctx, `
		WITH RECURSIVE tree AS (
			SELECT _id, parent_task_id FROM base_task WHERE _id = $1
			UNION ALL
			SELECT c._id, c.parent_task_id
			FROM base_task c JOIN tree t ON c.parent_task_id = t._id
		)
		SELECT to_jsonb(b) FROM base_task b JOIN tree t ON t._id = b._id`, rootTaskID)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	defer rows.Close()

	type rawNode struct {
		row     map[string]any
		parent  string
		defID   string
		created time.Time
		title   string
		id      string
	}
	byID := map[string]*rawNode{}
	children := map[string][]*rawNode{}
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			t.Fatalf("scan snapshot row: %v", err)
		}
		var row map[string]any
		if err := json.Unmarshal(encoded, &row); err != nil {
			t.Fatalf("decode snapshot row: %v", err)
		}
		node := &rawNode{row: row}
		node.id, _ = row["_id"].(string)
		node.parent, _ = row["parent_task_id"].(string)
		node.defID, _ = row["definition_id"].(string)
		node.title, _ = row["title"].(string)
		if created, ok := row["_created_at"].(string); ok {
			node.created, _ = time.Parse(time.RFC3339Nano, created)
		}
		byID[node.id] = node
		children[node.parent] = append(children[node.parent], node)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate snapshot rows: %v", err)
	}

	// Exclude exactly the columns AC-18 allows to differ. Everything else is
	// compared, including columns this test does not know the name of.
	excluded := []string{"_id", "number", "_created_at", "_updated_at", "state_changed_at", "started_at", "closed_at"}

	snapshot := spawnSnapshot{Root: "root"}
	var walk func(node *rawNode, parent int)
	walk = func(node *rawNode, parent int) {
		row := make(map[string]any, len(node.row))
		for key, value := range node.row {
			row[key] = value
		}
		for _, key := range excluded {
			delete(row, key)
		}
		row["title"] = taskNumberInTitle.ReplaceAllString(node.title, "(NUMBER)")
		row["parent_task_id"] = parent
		snapshot.Nodes = append(snapshot.Nodes, snapshotNode{Parent: parent, Row: row})
		// Capture this node's own ordinal before descending: using the last
		// appended index would give a later sibling the previous subtree's
		// deepest node as its parent.
		self := len(snapshot.Nodes) - 1

		siblings := children[node.id]
		sort.SliceStable(siblings, func(i, j int) bool {
			if siblings[i].defID != siblings[j].defID {
				return siblings[i].defID < siblings[j].defID
			}
			if !siblings[i].created.Equal(siblings[j].created) {
				return siblings[i].created.Before(siblings[j].created)
			}
			return siblings[i].id < siblings[j].id
		})
		for _, child := range siblings {
			walk(child, self)
		}
	}
	walk(byID[rootTaskID], 0)
	return snapshot
}

func TestPreMigrationDefinitionSpawnsIdentically(t *testing.T) {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}

	ctx := context.Background()
	dbName := fmt.Sprintf("velm_pre098_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create scratch database: %v", err)
	}
	scratchURL := databaseURLFor(t, baseURL, dbName)
	t.Setenv("DATABASE_URL", scratchURL)
	os.Setenv("DATABASE_URL", scratchURL)
	if err := ConnectToDB(); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("ConnectToDB: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		CloseDB()
		if _, err := admin.Exec(bg, "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("cleanup: drop scratch database: %v", err)
		}
		_ = admin.Close(bg)
	})

	// --- 097, as a pre-098 installation would be. ---------------------------
	migTestApplyUpTo(t, ctx, "098_definition_block_tree.sql")
	seedPreMigrationDefinitions(t, ctx)

	// AC-19: a pre-098 leaf spawns one task and no children.
	beforeLeafID, err := SpawnTaskInstanceBySlug(ctx, Pool, "pre098_leaf", "", "", map[string]any{"server_name": "leaf-only"})
	if err != nil {
		t.Fatalf("spawn leaf at 097: %v", err)
	}
	var leafChildren int
	if err := Pool.QueryRow(ctx, `SELECT COUNT(*) FROM base_task WHERE parent_task_id = $1`, beforeLeafID).Scan(&leafChildren); err != nil {
		t.Fatalf("count leaf children: %v", err)
	}
	if leafChildren != 0 {
		t.Fatalf("pre-098 leaf spawned %d children, want 0", leafChildren)
	}

	// AC-20: the nested container, three levels deep.
	beforeRootID, err := SpawnTaskInstanceBySlug(ctx, Pool, "pre098_root", "", "", map[string]any{"server_name": "web-01"})
	if err != nil {
		t.Fatalf("spawn nested container at 097: %v", err)
	}
	before := takeSpawnSnapshot(t, ctx, beforeRootID)
	if len(before.Nodes) != 4 {
		t.Fatalf("pre-098 tree has %d nodes, want 4 (root, stage, stage leaf, dependent leaf): %#v", len(before.Nodes), before.Nodes)
	}
	// AC-20: assert the three levels explicitly. Comparing 097 with 098 alone
	// would also pass if both flattened the tree, so name the edges: root at
	// level 0, stage under the root, one leaf under the stage, and the
	// dependent leaf a sibling of the stage.
	definitionSlugs := map[string]string{}
	rows, err := Pool.Query(ctx, `SELECT _id::text, slug FROM base_task_definition WHERE slug LIKE 'pre098%'`)
	if err != nil {
		t.Fatalf("read definition ids: %v", err)
	}
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			t.Fatalf("scan definition id: %v", err)
		}
		definitionSlugs[id] = slug
	}
	rows.Close()

	if before.Nodes[0].Parent != 0 {
		t.Fatalf("root parent ordinal = %d, want 0", before.Nodes[0].Parent)
	}
	stageOrdinal := -1
	for index, node := range before.Nodes {
		slug := definitionSlugs[node.Row["definition_id"].(string)]
		if slug == "pre098_stage" {
			stageOrdinal = index
			if node.Parent != 0 {
				t.Fatalf("stage parent ordinal = %d, want 0 (under the root)", node.Parent)
			}
		}
	}
	if stageOrdinal < 0 {
		t.Fatal("spawned tree has no stage task")
	}
	var leavesUnderStage, leavesUnderRoot int
	for _, node := range before.Nodes {
		if definitionSlugs[node.Row["definition_id"].(string)] != "pre098_leaf" {
			continue
		}
		switch node.Parent {
		case stageOrdinal:
			leavesUnderStage++
		case 0:
			leavesUnderRoot++
		}
	}
	if leavesUnderStage != 1 || leavesUnderRoot != 1 {
		t.Fatalf("leaf placement = %d under the stage, %d under the root; want 1 and 1", leavesUnderStage, leavesUnderRoot)
	}

	// --- Apply 098 in place. ------------------------------------------------
	migTestApplyOne(t, ctx, "098_definition_block_tree.sql")

	// AC-13 and AC-14: the backfill leaves every top-level row a task block
	// at its sort_order, and never rewrites sort_order.
	var wrongKind, wrongPosition, mismatchedEdges int
	if err := Pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE parent_block_id IS NULL AND (block_kind <> 'task' OR block_type IS NOT NULL OR position <> sort_order)),
			COUNT(*) FILTER (WHERE position <> sort_order),
			COUNT(*) FILTER (WHERE cardinality(depends_on) <> cardinality(depends_on_block_id))
		FROM base_task_definition_child`).Scan(&wrongKind, &wrongPosition, &mismatchedEdges); err != nil {
		t.Fatalf("backfill check: %v", err)
	}
	if wrongKind != 0 {
		t.Fatalf("%d top-level blocks were not backfilled as task blocks at their sort_order", wrongKind)
	}
	if wrongPosition != 0 {
		t.Fatalf("%d blocks have position <> sort_order; 098 must not touch sort_order (AC-14)", wrongPosition)
	}
	if mismatchedEdges != 0 {
		t.Fatalf("%d blocks have a different number of edges in depends_on and depends_on_block_id (AC-15)", mismatchedEdges)
	}

	// AC-15: the backfilled edge points at the row, in the same container,
	// whose child definition is the one the legacy edge named.
	var resolvedToStage bool
	if err := Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM base_task_definition_child dependent
			JOIN base_task_definition_child dep_block
			  ON dep_block._id = ANY (dependent.depends_on_block_id)
			JOIN base_task_definition container_def ON container_def._id = dependent.container_definition_id
			JOIN base_task_definition dep_def ON dep_def._id = dep_block.child_definition_id
			WHERE container_def.slug = 'pre098_root'
			  AND dep_def.slug = 'pre098_stage'
			  AND dep_block.container_definition_id = dependent.container_definition_id
		)`).Scan(&resolvedToStage); err != nil {
		t.Fatalf("edge resolution: %v", err)
	}
	if !resolvedToStage {
		t.Fatal("depends_on_block_id was not backfilled to the stage's block row")
	}

	// --- The same definition, now on 098. -----------------------------------
	afterLeafID, err := SpawnTaskInstanceBySlug(ctx, Pool, "pre098_leaf", "", "", map[string]any{"server_name": "leaf-only"})
	if err != nil {
		t.Fatalf("spawn leaf at 098: %v", err)
	}
	if err := Pool.QueryRow(ctx, `SELECT COUNT(*) FROM base_task WHERE parent_task_id = $1`, afterLeafID).Scan(&leafChildren); err != nil {
		t.Fatalf("count leaf children after 098: %v", err)
	}
	if leafChildren != 0 {
		t.Fatalf("leaf spawned %d children after 098, want 0", leafChildren)
	}

	afterRootID, err := SpawnTaskInstanceBySlug(ctx, Pool, "pre098_root", "", "", map[string]any{"server_name": "web-01"})
	if err != nil {
		t.Fatalf("spawn nested container at 098: %v", err)
	}
	after := takeSpawnSnapshot(t, ctx, afterRootID)

	if !reflect.DeepEqual(before, after) {
		beforeJSON, _ := json.MarshalIndent(before, "", "  ")
		afterJSON, _ := json.MarshalIndent(after, "", "  ")
		t.Fatalf("a pre-098 definition does not spawn identically after 098\n--- 097 ---\n%s\n--- 098 ---\n%s", beforeJSON, afterJSON)
	}

	// AC-17 names variables.inputs explicitly, and a whole-row comparison can
	// be satisfied by two empty objects. Assert the mapped value is really
	// there on both stages.
	for _, taskID := range []string{beforeRootID, afterRootID} {
		var input string
		if err := Pool.QueryRow(ctx, `
			SELECT COALESCE(variables #>> '{inputs,server_name}', '')
			FROM base_task
			WHERE definition_id = (SELECT _id FROM base_task_definition WHERE slug = 'pre098_stage')
			  AND parent_task_id = $1`, taskID).Scan(&input); err != nil {
			t.Fatalf("stage input for root %s: %v", taskID, err)
		}
		if input != "web-01" {
			t.Fatalf("stage variables.inputs.server_name = %q, want web-01", input)
		}
	}
}
