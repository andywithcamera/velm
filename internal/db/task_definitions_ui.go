package db

// Read and write helpers for the task definition editor (PR: Task Definition
// Editor v1). Definitions are business data, but until now the only way to
// author them was hand-written YAML through the generic table CRUD or SQL
// against the box. These helpers are the data layer the editor's handlers use:
// they keep the list to one query regardless of how many definitions exist, so
// a catalog page cannot become N+1 by accident.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrTaskDefinitionNotFound is returned when a slug does not name a live
// definition. Soft-deleted rows (094) are not found: the slug is reusable, so
// treating them as visible would resurrect a definition the user deleted.
var ErrTaskDefinitionNotFound = errors.New("task definition not found")

// ErrDuplicateTaskDefinitionSlug reports the live-slug unique index
// (uq_base_task_definition_slug_live, 094:21-22) refusing a second definition
// with the same slug.
var ErrDuplicateTaskDefinitionSlug = errors.New("a definition with that slug already exists")

// TaskDefinitionRow is one row of the definitions list. ChildCount is the
// number of steps this definition calls; it is 0 for a leaf and is never read
// per row.
type TaskDefinitionRow struct {
	ID              string
	Slug            string
	DisplayName     string
	Description     string
	IsContainer     bool
	IsActive        bool
	InputSchemaYAML string
	// VariablesYAML is the declaration column the outputs list reads. Only its
	// outputs: list is used by the engine today.
	VariablesYAML string
	ChildCount    int
	// Assignment (098, Requirements §3): the group the definition routes to
	// and the strategy that picks a person from it. Empty means unset.
	DefaultGroupID     string
	AssignmentStrategy string
}

// InputCount is the number of declared requester inputs. A schema that fails to
// parse reports -1 so the list shows a dash rather than hiding the definition:
// a malformed schema is a definition that cannot be started, which is exactly
// what an author needs to see.
func (r TaskDefinitionRow) InputCount() int {
	fields, err := ParseTaskInputSchema(r.InputSchemaYAML)
	if err != nil {
		return -1
	}
	return len(fields)
}

// TaskDefinitionStep is one row of base_task_definition_child joined to the
// child definition, in the order the engine promotes steps.
type TaskDefinitionStep struct {
	ChildDefinitionID string
	ChildSlug         string
	ChildDisplayName  string
	ChildDeleted      bool
	// ChildInputSchemaYAML is the child's requester form. The step editor uses
	// it to name the mapping keys and to decide whether a key can be checked at
	// all (AC-59, AC-60); it is joined here so the editor stays one query.
	ChildInputSchemaYAML string
	SortOrder            int
	DependsOn            []string
	DependsOnReason      string
	InputMapping         map[string]string
}

const taskDefinitionColumns = `d._id::text, d.slug, d.display_name, d.description,
	d.is_container, d.is_active, COALESCE(d.input_schema_yaml, ''),
	COALESCE(d.variables_yaml, ''), COALESCE(d.default_group_id::text, ''),
	COALESCE(d.assignment_strategy, '')`

// escapeLike makes a user's search text a literal needle rather than a pattern:
// without this, typing "%" matches every definition and typing "_" matches any
// single character.
func escapeLike(needle string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(needle)
}

// ListTaskDefinitionRows returns every live definition with its step count.
// One query, whatever the row count: the child counts come from a single
// grouped subquery rather than a count per definition (AC-8).
func ListTaskDefinitionRows(ctx context.Context, query string) ([]TaskDefinitionRow, error) {
	needle := escapeLike(strings.ToLower(strings.TrimSpace(query)))
	rows, err := Pool.Query(ctx, `
		SELECT `+taskDefinitionColumns+`, COALESCE(c.child_count, 0)
		FROM base_task_definition d
		LEFT JOIN (
			SELECT container_definition_id, COUNT(*) AS child_count
			FROM base_task_definition_child
			GROUP BY container_definition_id
		) c ON c.container_definition_id = d._id
		WHERE d._deleted_at IS NULL
		  AND ($1 = ''
		       OR LOWER(d.slug) LIKE '%' || $1 || '%'
		       OR LOWER(d.display_name) LIKE '%' || $1 || '%')
		ORDER BY d.display_name, d.slug`, needle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TaskDefinitionRow{}
	for rows.Next() {
		var row TaskDefinitionRow
		if err := rows.Scan(&row.ID, &row.Slug, &row.DisplayName, &row.Description,
			&row.IsContainer, &row.IsActive, &row.InputSchemaYAML, &row.VariablesYAML,
			&row.DefaultGroupID, &row.AssignmentStrategy, &row.ChildCount); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetTaskDefinitionBySlug returns one live definition. A soft-deleted slug is
// ErrTaskDefinitionNotFound.
func GetTaskDefinitionBySlug(ctx context.Context, slug string) (TaskDefinitionRow, error) {
	var row TaskDefinitionRow
	err := Pool.QueryRow(ctx, `
		SELECT `+taskDefinitionColumns+`,
		       (SELECT COUNT(*) FROM base_task_definition_child c
		        WHERE c.container_definition_id = d._id)
		FROM base_task_definition d
		WHERE d.slug = $1 AND d._deleted_at IS NULL`, strings.TrimSpace(slug)).
		Scan(&row.ID, &row.Slug, &row.DisplayName, &row.Description,
			&row.IsContainer, &row.IsActive, &row.InputSchemaYAML, &row.VariablesYAML,
			&row.DefaultGroupID, &row.AssignmentStrategy, &row.ChildCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskDefinitionRow{}, ErrTaskDefinitionNotFound
	}
	if err != nil {
		return TaskDefinitionRow{}, err
	}
	return row, nil
}

// ListDefinitionSteps returns a container's steps in the rows view's order:
// sort_order first, then _created_at to break ties. (The block tree orders by
// parent and then position at run time; the canvas reads that order through
// LoadDefinitionDocument, so the two views can differ in presentation but never
// in what is stored.) A child
// deleted softly still has its link row (094 deletes softly while the FK is
// ON DELETE CASCADE), so the row is returned with ChildDeleted set and the
// editor can show it as broken rather than pretending it runs.
func ListDefinitionSteps(ctx context.Context, definitionID string) ([]TaskDefinitionStep, error) {
	rows, err := Pool.Query(ctx, `
		SELECT c.child_definition_id::text, child.slug, child.display_name,
		       (child._deleted_at IS NOT NULL),
		       COALESCE(child.input_schema_yaml, ''),
		       c.sort_order,
		       COALESCE(c.depends_on::text[], ARRAY[]::text[]),
		       COALESCE(c.depends_on_reason, ''),
		       COALESCE(c.input_mapping, '{}'::jsonb)
		FROM base_task_definition_child c
		JOIN base_task_definition child ON child._id = c.child_definition_id
		WHERE c.container_definition_id = $1
		ORDER BY c.sort_order, c._created_at`, definitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TaskDefinitionStep{}
	for rows.Next() {
		var step TaskDefinitionStep
		var mapping []byte
		if err := rows.Scan(&step.ChildDefinitionID, &step.ChildSlug, &step.ChildDisplayName,
			&step.ChildDeleted, &step.ChildInputSchemaYAML, &step.SortOrder, &step.DependsOn,
			&step.DependsOnReason, &mapping); err != nil {
			return nil, err
		}
		step.InputMapping = decodeInputMapping(mapping)
		out = append(out, step)
	}
	return out, rows.Err()
}

// decodeInputMapping keeps every stored value as text. The engine passes
// non-string values through untouched, but the editor edits text, and a value
// it cannot represent must still be shown rather than dropped.
func decodeInputMapping(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return out
	}
	for key, value := range decoded {
		switch typed := value.(type) {
		case string:
			out[key] = typed
		case nil:
			out[key] = ""
		default:
			out[key] = fmt.Sprint(typed)
		}
	}
	return out
}

// CreateTaskDefinition inserts a definition with an empty input schema. The
// rest of the columns take their 076 defaults: in particular is_active is TRUE,
// so a new definition is immediately startable from the catalog.
func CreateTaskDefinition(ctx context.Context, slug, displayName, description string, isContainer bool) (TaskDefinitionRow, error) {
	var row TaskDefinitionRow
	err := Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition (slug, display_name, description, is_container)
		VALUES ($1, $2, $3, $4)
		RETURNING `+strings.ReplaceAll(taskDefinitionColumns, "d.", "")+`, 0`,
		strings.TrimSpace(slug), strings.TrimSpace(displayName), strings.TrimSpace(description), isContainer).
		Scan(&row.ID, &row.Slug, &row.DisplayName, &row.Description,
			&row.IsContainer, &row.IsActive, &row.InputSchemaYAML, &row.VariablesYAML,
			&row.DefaultGroupID, &row.AssignmentStrategy, &row.ChildCount)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_base_task_definition_slug_live" {
			return TaskDefinitionRow{}, ErrDuplicateTaskDefinitionSlug
		}
		return TaskDefinitionRow{}, err
	}
	return row, nil
}

// UpdateTaskDefinitionInputSchema writes the requester form's YAML exactly as
// given. The caller has already run one of the two validators: the row editor's
// lossless gate and ParseTaskInputSchema, or a raw save's ParseTaskInputSchema
// alone (AC-37). This helper adds no rules of its own, so there is only ever one
// set of them.
func UpdateTaskDefinitionInputSchema(ctx context.Context, slug, raw string) error {
	tag, err := Pool.Exec(ctx, `
		UPDATE base_task_definition
		SET input_schema_yaml = $2, _updated_at = NOW()
		WHERE slug = $1 AND _deleted_at IS NULL`, strings.TrimSpace(slug), raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTaskDefinitionNotFound
	}
	return nil
}

// UpdateTaskDefinitionBasics writes the four editable columns of the basics
// section. The slug is deliberately not among them: it is the stable reference
// other definitions and spawn rules hold.
func UpdateTaskDefinitionBasics(ctx context.Context, slug, displayName, description string, isContainer, isActive bool, defaultGroupID, assignmentStrategy string) error {
	tag, err := Pool.Exec(ctx, `
		UPDATE base_task_definition
		SET display_name = $2, description = $3, is_container = $4,
		    is_active = $5, default_group_id = $6, assignment_strategy = $7,
		    _updated_at = NOW()
		WHERE slug = $1 AND _deleted_at IS NULL`,
		strings.TrimSpace(slug), strings.TrimSpace(displayName), strings.TrimSpace(description),
		isContainer, isActive, nullIfEmpty(defaultGroupID), nullIfEmpty(assignmentStrategy))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTaskDefinitionNotFound
	}
	return nil
}

// nullIfEmpty keeps an unset form field as SQL NULL rather than an empty
// string: assignment_group_id and assignment_strategy are nullable columns.
func nullIfEmpty(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
