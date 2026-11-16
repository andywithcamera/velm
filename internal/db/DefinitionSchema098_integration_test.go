package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Requirements section 1 (migration 098): the definition tables become a tree
// with per-block assignment, and every definition authored before 098 keeps
// spawning exactly as it did. These tests read the real Postgres schema rather
// than a model of it, because the acceptance criteria are constraints and
// column definitions, and a mocked constraint proves nothing.

func schemaTestDB(t *testing.T) context.Context {
	t.Helper()
	return taskEngineTestDB(t)
}

func schemaTestDefinition(t *testing.T, ctx context.Context, slug string, container bool) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition (slug, display_name, is_container)
		VALUES ($1, $1, $2)
		RETURNING _id::text`, slug, container).Scan(&id); err != nil {
		t.Fatalf("seed definition %q: %v", slug, err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM base_task_definition WHERE _id = $1`, id)
	})
	return id
}

func schemaTestBlock(t *testing.T, ctx context.Context, containerID, childID string, sortOrder int) string {
	t.Helper()
	var id string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, sort_order)
		VALUES ($1, $2, $3)
		RETURNING _id::text`, containerID, childID, sortOrder).Scan(&id); err != nil {
		t.Fatalf("insert block: %v", err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM base_task_definition_child WHERE _id = $1`, id)
	})
	return id
}

func columnInfo(t *testing.T, ctx context.Context, table, column string) (dataType, columnDefault string, nullable bool) {
	t.Helper()
	err := Pool.QueryRow(ctx, `
		SELECT data_type, COALESCE(column_default, ''), is_nullable = 'YES'
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, column).Scan(&dataType, &columnDefault, &nullable)
	if err != nil {
		t.Fatalf("column %s.%s: %v", table, column, err)
	}
	return dataType, columnDefault, nullable
}

// foreignKeyTarget returns the table a column's foreign key points at, or ""
// when the column has no foreign key. AC-2 and AC-3 name REFERENCES, not just
// a uuid column: a bare uuid would accept an id from anywhere.
func foreignKeyTarget(t *testing.T, ctx context.Context, table, column string) string {
	t.Helper()
	var target string
	err := Pool.QueryRow(ctx, `
		SELECT confrelid::regclass::text
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY (c.conkey)
		WHERE c.contype = 'f' AND t.relname = $1 AND a.attname = $2`, table, column).Scan(&target)
	if err != nil {
		return ""
	}
	return target
}

// AC-1: 098 is applied by the runner against a database that was at 097 and
// a second run is a no-op, which is what "applies with no error" means for a
// migration that will meet existing installations.
func TestMigration098Applied(t *testing.T) {
	ctx := schemaTestDB(t)

	var checksum string
	if err := Pool.QueryRow(ctx, `
		SELECT COALESCE(checksum, '') FROM _schema_migration
		WHERE version = '098_definition_block_tree.sql'`).Scan(&checksum); err != nil {
		t.Fatalf("098 is not recorded in _schema_migration: %v", err)
	}
	if checksum == "" {
		t.Fatal("098 is recorded without a checksum; an edited migration would go unnoticed")
	}

	// The runner has already applied it once for this test database. A second
	// run must skip it rather than re-running DDL, and must not error.
	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("second RunMigrations over 098: %v", err)
	}
}

// AC-2 and AC-3: the tree columns exist with the declared types and defaults.
func TestDefinitionBlockTreeColumns(t *testing.T) {
	ctx := schemaTestDB(t)

	for _, tc := range []struct {
		column   string
		dataType string
		nullable bool
	}{
		{"parent_block_id", "uuid", true},
		{"position", "integer", false},
		{"block_kind", "text", false},
		{"block_type", "text", true},
		{"config", "jsonb", false},
		{"assignment_group_id", "uuid", true},
		{"assignment_strategy", "text", true},
		{"assigned_user_id", "uuid", true},
		{"depends_on_block_id", "ARRAY", false},
	} {
		dataType, def, nullable := columnInfo(t, ctx, "base_task_definition_child", tc.column)
		if dataType != tc.dataType {
			t.Errorf("%s type = %q, want %q", tc.column, dataType, tc.dataType)
		}
		if nullable != tc.nullable {
			t.Errorf("%s nullable = %v, want %v", tc.column, nullable, tc.nullable)
		}
		if !nullable && def == "" {
			t.Errorf("%s is NOT NULL but has no default; pre-098 inserts would fail", tc.column)
		}
	}

	// The defaults are what make a pre-098 INSERT legal: an old writer knows
	// none of these columns and must still get a valid top-level task block.
	if _, def, _ := columnInfo(t, ctx, "base_task_definition_child", "block_kind"); !strings.Contains(def, "'task'") {
		t.Errorf("block_kind default = %q, want 'task'", def)
	}
	if _, def, _ := columnInfo(t, ctx, "base_task_definition_child", "position"); !strings.Contains(def, "0") {
		t.Errorf("position default = %q, want 0", def)
	}
	if _, def, _ := columnInfo(t, ctx, "base_task_definition_child", "config"); !strings.Contains(def, "'{}'") {
		t.Errorf("config default = %q, want '{}'", def)
	}

	// AC-2 and AC-3 name REFERENCES for the id columns: nesting must point at
	// another block, and assignment at a real group and user.
	for _, fk := range []struct{ column, target string }{
		{"parent_block_id", "base_task_definition_child"},
		{"assignment_group_id", "_group"},
		{"assigned_user_id", "_user"},
	} {
		if got := foreignKeyTarget(t, ctx, "base_task_definition_child", fk.column); got != fk.target {
			t.Errorf("%s foreign key = %q, want %q", fk.column, got, fk.target)
		}
	}

	// AC-16: exactly one new assignment column on the definition. The group
	// and the named user stay on default_group_id / default_assignee_id; a
	// second group column is the routing defect 098 exists to avoid.
	dataType, _, nullable := columnInfo(t, ctx, "base_task_definition", "assignment_strategy")
	if dataType != "text" || !nullable {
		t.Fatalf("base_task_definition.assignment_strategy = %q nullable=%v, want text nullable", dataType, nullable)
	}
	defID := schemaTestDefinition(t, ctx, "schema-strategy-"+fmt.Sprint(time.Now().UnixNano()), false)
	_, err := Pool.Exec(ctx, `UPDATE base_task_definition SET assignment_strategy = 'least_loaded' WHERE _id = $1`, defID)
	if err == nil {
		t.Fatal("assignment_strategy accepted least_loaded; the six-strategy vocabulary must be a constraint")
	}

	var strayGroupColumn bool
	if err := Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'base_task_definition'
			  AND column_name = 'assignment_group_id'
		)`).Scan(&strayGroupColumn); err != nil {
		t.Fatalf("check base_task_definition columns: %v", err)
	}
	if strayGroupColumn {
		t.Fatal("base_task_definition gained assignment_group_id; default_group_id is the single group source (AC-16, §0.3)")
	}
}

// AC-4: block_kind and block_type are constrained, not free text.
func TestDefinitionBlockKindConstraints(t *testing.T) {
	ctx := schemaTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	container := schemaTestDefinition(t, ctx, "schema-container-"+suffix, true)
	child := schemaTestDefinition(t, ctx, "schema-child-"+suffix, false)

	refuse := func(label, query string, args ...any) {
		t.Helper()
		if _, err := Pool.Exec(ctx, query, args...); err == nil {
			t.Fatalf("%s was accepted; it must be refused by a check constraint", label)
		}
	}

	// A control block with an unknown shape.
	refuse("control block_type=nonsense", `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, block_kind, block_type)
		VALUES ($1, $2, 'control', 'nonsense')`, container, child)

	// A non-control block carrying a control type.
	refuse("task block_type=if", `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, block_kind, block_type)
		VALUES ($1, $2, 'task', 'if')`, container, child)

	// An unknown kind.
	refuse("block_kind=workflow", `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, block_kind)
		VALUES ($1, $2, 'workflow')`, container, child)

	// And the legal shapes are accepted.
	if _, err := Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, block_kind, block_type, position)
		VALUES ($1, $2, 'control', 'if', 10)`, container, child); err != nil {
		t.Fatalf("legal control block refused: %v", err)
	}
}

// AC-10: one container may hold the same child definition twice now that the
// unique constraint is gone; position tells the two apart.
func TestDefinitionChildMayRepeatInContainer(t *testing.T) {
	ctx := schemaTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	container := schemaTestDefinition(t, ctx, "schema-repeat-"+suffix, true)
	child := schemaTestDefinition(t, ctx, "schema-repeat-child-"+suffix, false)

	first := schemaTestBlock(t, ctx, container, child, 10)
	second := schemaTestBlock(t, ctx, container, child, 20)
	if first == second {
		t.Fatal("two blocks share an identity")
	}
	var count int
	if err := Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM base_task_definition_child
		WHERE container_definition_id = $1 AND child_definition_id = $2`, container, child).Scan(&count); err != nil {
		t.Fatalf("count blocks: %v", err)
	}
	if count != 2 {
		t.Fatalf("blocks for repeated child = %d, want 2", count)
	}
}

// AC-10 removes the database guarantee the rows view relied on: with the
// unique constraint gone, AddDefinitionStep must refuse the duplicate itself,
// or the v1 step editor would silently start adding the same child twice.
func TestDefinitionStepAddStaysUniqueInRowsView(t *testing.T) {
	ctx := schemaTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	container := schemaTestDefinition(t, ctx, "schema-add-step-"+suffix, true)
	child := schemaTestDefinition(t, ctx, "schema-add-step-child-"+suffix, false)
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM base_task_definition_child WHERE container_definition_id = $1`, container)
	})

	if err := AddDefinitionStep(ctx, container, child); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := AddDefinitionStep(ctx, container, child); !errors.Is(err, ErrDuplicateDefinitionStep) {
		t.Fatalf("second add = %v, want ErrDuplicateDefinitionStep", err)
	}

	var count int
	if err := Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM base_task_definition_child
		WHERE container_definition_id = $1 AND child_definition_id = $2`, container, child).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows after duplicate refusal = %d, want 1", count)
	}
}

// AC-13 in spirit: position is the sibling order from 098 on, and the flat
// rows view writes sort_order. If the two disagree, the same step reads first
// in one view and second in another, so every write through the rows view must
// move both. Backfilled data satisfies this by construction; this pins the
// writes that come after it.
func TestDefinitionStepWritesKeepPositionWithSortOrder(t *testing.T) {
	ctx := schemaTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	container := schemaTestDefinition(t, ctx, "schema-position-"+suffix, true)
	first := schemaTestDefinition(t, ctx, "schema-position-a-"+suffix, false)
	second := schemaTestDefinition(t, ctx, "schema-position-b-"+suffix, false)
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM base_task_definition_child WHERE container_definition_id = $1`, container)
	})

	if err := AddDefinitionStep(ctx, container, first); err != nil {
		t.Fatalf("add first: %v", err)
	}
	if err := AddDefinitionStep(ctx, container, second); err != nil {
		t.Fatalf("add second: %v", err)
	}

	mismatched := func() int {
		t.Helper()
		var n int
		if err := Pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM base_task_definition_child
			WHERE container_definition_id = $1 AND position <> sort_order`, container).Scan(&n); err != nil {
			t.Fatalf("count mismatches: %v", err)
		}
		return n
	}
	if n := mismatched(); n != 0 {
		t.Fatalf("%d rows added by the rows view have position <> sort_order", n)
	}

	if _, err := MoveDefinitionStep(ctx, container, second, "up"); err != nil {
		t.Fatalf("move up: %v", err)
	}
	if n := mismatched(); n != 0 {
		t.Fatalf("%d rows have position <> sort_order after a move", n)
	}

	// The move really happened, and it moved both columns.
	var order []string
	rows, err := Pool.Query(ctx, `
		SELECT child_definition_id::text FROM base_task_definition_child
		WHERE container_definition_id = $1 ORDER BY position, sort_order, _created_at`, container)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan order: %v", err)
		}
		order = append(order, id)
	}
	rows.Close()
	if len(order) != 2 || order[0] != second || order[1] != first {
		t.Fatalf("order after move = %v, want the second step first", order)
	}
}

// AC-11: the edge column is an indexed array of block rows.
func TestDefinitionBlockEdgeColumnIsIndexed(t *testing.T) {
	ctx := schemaTestDB(t)
	var indexDef string
	err := Pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public'
		  AND tablename = 'base_task_definition_child'
		  AND indexname = 'idx_base_task_definition_child_depends'`).Scan(&indexDef)
	if err != nil {
		t.Fatalf("index idx_base_task_definition_child_depends: %v", err)
	}
	if !strings.Contains(indexDef, "USING gin") || !strings.Contains(indexDef, "depends_on_block_id") {
		t.Fatalf("index is %q, want a GIN index on depends_on_block_id", indexDef)
	}
}

// AC-12: the 084 trigger refuses a cycle expressed over block rows, with the
// message class the editor translates, and the legacy definition-id edges keep
// the refusal they had.
func TestDefinitionBlockEdgeCycleRefused(t *testing.T) {
	ctx := schemaTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	container := schemaTestDefinition(t, ctx, "schema-cycle-"+suffix, true)
	leafA := schemaTestDefinition(t, ctx, "schema-cycle-a-"+suffix, false)
	leafB := schemaTestDefinition(t, ctx, "schema-cycle-b-"+suffix, false)

	blockA := schemaTestBlock(t, ctx, container, leafA, 10)
	blockB := schemaTestBlock(t, ctx, container, leafB, 20)

	if _, err := Pool.Exec(ctx, `
		UPDATE base_task_definition_child SET depends_on_block_id = ARRAY[$2::uuid] WHERE _id = $1`,
		blockA, blockB); err != nil {
		t.Fatalf("first block edge refused: %v", err)
	}

	_, err := Pool.Exec(ctx, `
		UPDATE base_task_definition_child SET depends_on_block_id = ARRAY[$2::uuid] WHERE _id = $1`,
		blockB, blockA)
	if err == nil {
		t.Fatal("a block cycle was accepted")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "P0001" || !strings.Contains(pgErr.Message, "dependency cycle") {
		t.Fatalf("cycle error = %v, want P0001 mentioning 'dependency cycle' (the class the editor maps to ErrStepDependencyCycle)", err)
	}

	// The v1 rows editor writes depends_on (definition ids). It must still be
	// refused, and still surface as the sentinel the handlers check.
	if err := UpdateDefinitionStepDepends(ctx, container, leafA, []string{leafB}, ""); err != nil {
		t.Fatalf("first legacy edge refused: %v", err)
	}
	err = UpdateDefinitionStepDepends(ctx, container, leafB, []string{leafA}, "")
	if !errors.Is(err, ErrStepDependencyCycle) {
		t.Fatalf("legacy cycle = %v, want ErrStepDependencyCycle", err)
	}
}
