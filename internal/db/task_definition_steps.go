package db

// Step mutations for the task definition editor (slice 3 of #358).
//
// A container's steps live in base_task_definition_child: an ordered list of
// child definitions with an optional dependency edge and an input mapping.
// Until now that table was writable only by SQL. These helpers are the data
// layer the editor's step handlers use; every rule about whether a write is
// allowed is decided in the handler, except the two the database owns and
// must own: the unique key on (container, child) and 084's acyclicity trigger.
// Those two are translated here into sentinel errors so a handler can say
// what happened in English.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"gopkg.in/yaml.v3"
)

var (
	// ErrDuplicateDefinitionStep reports a second copy of the same child in one
	// container. 078 enforced this with uq_base_task_definition_child; 098
	// drops that constraint because a tree may legitimately hold the same
	// definition twice (two reboot_server blocks), so the rows view checks it
	// explicitly and keeps the refusal its callers already translate.
	ErrDuplicateDefinitionStep = errors.New("that definition is already a step in this container")

	// ErrStepDependencyCycle reports 084's base_task_child_edge_acyclic trigger
	// refusing a dependency that would close a loop.
	ErrStepDependencyCycle = errors.New("that dependency would create a cycle")
)

// TaskClosureReasons is the vocabulary a depends_on_reason may hold. It mirrors
// the reasons a task can close with (084's chk_base_task_closure_reason);
// an edge waiting on anything else could never be satisfied.
var TaskClosureReasons = []string{"completed", "cancelled", "rejected", "failed"}

// IsTaskClosureReason reports whether reason is in the closure vocabulary.
func IsTaskClosureReason(reason string) bool {
	for _, candidate := range TaskClosureReasons {
		if candidate == reason {
			return true
		}
	}
	return false
}

// GetTaskDefinitionBySlugIncludingDeleted resolves a child slug for the step
// editor. A soft-deleted definition still has its link row (094 deletes
// softly), so the caller needs to tell "absent" from "deleted" and say which
// one it is in the refusal.
func GetTaskDefinitionBySlugIncludingDeleted(ctx context.Context, slug string) (TaskDefinitionRow, string, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return TaskDefinitionRow{}, "", ErrTaskDefinitionNotFound
	}
	var row TaskDefinitionRow
	var deleted bool
	err := Pool.QueryRow(ctx, `
		SELECT `+taskDefinitionColumns+`, (d._deleted_at IS NOT NULL)
		FROM base_task_definition d
		WHERE d.slug = $1`, slug).
		Scan(&row.ID, &row.Slug, &row.DisplayName, &row.Description,
			&row.IsContainer, &row.IsActive, &row.InputSchemaYAML, &row.VariablesYAML,
			&row.DefaultGroupID, &row.AssignmentStrategy, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskDefinitionRow{}, "", ErrTaskDefinitionNotFound
	}
	if err != nil {
		return TaskDefinitionRow{}, "", err
	}
	status := ""
	if deleted {
		status = "deleted"
	}
	return row, status, nil
}

// AddDefinitionStep appends a child at the end of the container's order:
// sort_order is the current maximum plus ten, leaving room to insert between
// two steps later without renumbering.
func AddDefinitionStep(ctx context.Context, containerID, childID string) error {
	tag, err := Pool.Exec(ctx, `
		WITH next_position AS (
			SELECT COALESCE(MAX(sort_order), 0) + 10 AS position
			FROM base_task_definition_child
			WHERE container_definition_id = $1
		)
		INSERT INTO base_task_definition_child (container_definition_id, child_definition_id, sort_order, position)
		SELECT $1, $2, position, position
		FROM next_position
		WHERE NOT EXISTS (
			SELECT 1 FROM base_task_definition_child
			WHERE container_definition_id = $1 AND child_definition_id = $2
		)`, containerID, childID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "uq_base_task_definition_child" {
			return ErrDuplicateDefinitionStep
		}
		return err
	}
	// 098 removed the unique constraint that used to make this refusal, so the
	// rows view decides it here: a row was inserted only when none existed.
	if tag.RowsAffected() == 0 {
		return ErrDuplicateDefinitionStep
	}
	return nil
}

// RemoveDefinitionStep hard-deletes the link row. 078 has no soft-delete
// columns: a removed step is gone, which is what the author asked for.
func RemoveDefinitionStep(ctx context.Context, containerID, childID string) error {
	_, err := Pool.Exec(ctx, `
		DELETE FROM base_task_definition_child
		WHERE container_definition_id = $1 AND child_definition_id = $2`, containerID, childID)
	return err
}

// StepDependants returns the slugs of every step in the container listing
// childID in its depends_on. Removing a step another step waits on would leave
// an edge that can never be satisfied, so the handler refuses and names them.
func StepDependants(ctx context.Context, containerID, childID string) ([]string, error) {
	rows, err := Pool.Query(ctx, `
		SELECT child.slug
		FROM base_task_definition_child c
		JOIN base_task_definition child ON child._id = c.child_definition_id
		WHERE c.container_definition_id = $1
		  AND $2::uuid = ANY(c.depends_on)
		ORDER BY c.sort_order, c._created_at`, containerID, childID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

// MoveDefinitionStep swaps the step's sort_order with the adjacent row in
// engine order, in one transaction. First-up and last-down are a no-op and
// report false. When two adjacent steps share a sort_order the swap would be
// invisible, so the pair is given distinct values instead: order is what the
// author sees, sort_order is only how it is stored.
func MoveDefinitionStep(ctx context.Context, containerID, childID, direction string) (bool, error) {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT child_definition_id::text, sort_order
		FROM base_task_definition_child
		WHERE container_definition_id = $1
		ORDER BY sort_order, _created_at
		FOR UPDATE`, containerID)
	if err != nil {
		return false, err
	}
	type orderedStep struct {
		id        string
		sortOrder int
	}
	ordered := []orderedStep{}
	for rows.Next() {
		var step orderedStep
		if err := rows.Scan(&step.id, &step.sortOrder); err != nil {
			rows.Close()
			return false, err
		}
		ordered = append(ordered, step)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	index := -1
	for i, step := range ordered {
		if step.id == childID {
			index = i
			break
		}
	}
	if index < 0 {
		return false, ErrTaskDefinitionNotFound
	}

	target := index + 1
	if direction == "up" {
		target = index - 1
	}
	if target < 0 || target >= len(ordered) {
		return false, nil
	}

	earlier, later := ordered[index], ordered[target]
	if direction == "up" {
		earlier, later = ordered[target], ordered[index]
	}
	firstValue, secondValue := earlier.sortOrder, later.sortOrder
	if firstValue == secondValue {
		secondValue = firstValue + 1
	} else {
		firstValue, secondValue = secondValue, firstValue
	}
	for _, update := range []struct {
		id    string
		value int
	}{{earlier.id, firstValue}, {later.id, secondValue}} {
		// position is the tree's sibling order from 098 on (Design Vision 4).
		// The rows view below still stores sort_order, and the two must agree
		// or the same step would read first in one view and second in another.
		if _, err := tx.Exec(ctx, `
			UPDATE base_task_definition_child SET sort_order = $3, position = $3
			WHERE container_definition_id = $1 AND child_definition_id = $2`,
			containerID, update.id, update.value); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateDefinitionStepDepends writes the dependency edge. An empty reason is
// stored as the empty string, which 084 reads as "any successful close". The
// acyclicity trigger decides whether the edge is legal; nothing here checks
// for cycles, so there is only one arbiter.
func UpdateDefinitionStepDepends(ctx context.Context, containerID, childID string, dependsOn []string, reason string) error {
	if dependsOn == nil {
		dependsOn = []string{}
	}
	_, err := Pool.Exec(ctx, `
		UPDATE base_task_definition_child
		SET depends_on = $3::text[]::uuid[], depends_on_reason = $4
		WHERE container_definition_id = $1 AND child_definition_id = $2`,
		containerID, childID, dependsOn, reason)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" &&
			strings.Contains(pgErr.Message, "dependency cycle") {
			return ErrStepDependencyCycle
		}
		return err
	}
	return nil
}

// UpdateDefinitionStepMapping replaces the mapping. Values are stored exactly as
// submitted: the engine is the consumer and a literal's spaces or a "$5.00" are
// significant to it.
func UpdateDefinitionStepMapping(ctx context.Context, containerID, childID string, mapping map[string]string) error {
	if mapping == nil {
		mapping = map[string]string{}
	}
	encoded, err := json.Marshal(mapping)
	if err != nil {
		return err
	}
	_, err = Pool.Exec(ctx, `
		UPDATE base_task_definition_child
		SET input_mapping = $3::jsonb
		WHERE container_definition_id = $1 AND child_definition_id = $2`,
		containerID, childID, string(encoded))
	return err
}

// TaskVariableDeclarations is the part of variables_yaml the editor shows.
// Only outputs are read by the engine today; inputs and locals are declared in
// the column and read by nothing, so the editor shows them read-only rather
// than pretending they do something.
type TaskVariableDeclarations struct {
	Outputs []string
	Inputs  []string
	Locals  []string
}

// ParseTaskVariableDeclarations reads the declaration lists out of
// variables_yaml. An empty column is an empty declaration, not an error.
func ParseTaskVariableDeclarations(raw string) (TaskVariableDeclarations, error) {
	declaration := TaskVariableDeclarations{}
	if strings.TrimSpace(raw) == "" {
		return declaration, nil
	}
	parsed := variableDeclarations{}
	if err := yaml.Unmarshal([]byte(raw), &parsed); err != nil {
		return declaration, fmt.Errorf("read variables: %w", err)
	}
	clean := func(names []string) []string {
		out := make([]string, 0, len(names))
		for _, name := range names {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	}
	declaration.Outputs = clean(parsed.Outputs)
	declaration.Inputs = clean(parsed.Inputs)
	declaration.Locals = clean(parsed.Locals)
	return declaration, nil
}
