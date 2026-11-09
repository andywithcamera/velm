package db

import (
	"context"
	"fmt"
	"strings"
)

// MCP-facing helpers (issue #240): thin, addressable-by-name queries the MCP
// tool layer can call without knowing table internals. They accept task
// references as either a uuid or a TASK-n number, so remote agents can use
// the human-visible identifier.

// ResolveTaskRef maps a task reference (uuid or TASK-n number) to its uuid.
// Empty or ambiguous results return an error.
func ResolveTaskRef(ctx context.Context, q scriptQuerier, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty task reference")
	}
	var id string
	err := q.QueryRow(ctx, `
		SELECT _id::text FROM base_task
		WHERE _deleted_at IS NULL AND (_id::text = $1 OR upper(number) = upper($1))
		LIMIT 2`, ref).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("resolve task %q: %w", ref, err)
	}
	return id, nil
}

// ListTaskInstances returns the most recent non-deleted tasks, optionally
// filtered to one or more states. Results are newest first.
func ListTaskInstances(ctx context.Context, q scriptQuerier, states []string, limit int) ([]TaskInstanceSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	query := `SELECT _id::text, number, title, state FROM base_task WHERE _deleted_at IS NULL`
	args := []any{}
	if len(states) > 0 {
		query += ` AND state = ANY($1)`
		args = append(args, states)
	}
	query += fmt.Sprintf(` ORDER BY _created_at DESC LIMIT %d`, limit)
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskInstanceSummary{}
	for rows.Next() {
		var t TaskInstanceSummary
		if err := rows.Scan(&t.ID, &t.Number, &t.Title, &t.State); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
