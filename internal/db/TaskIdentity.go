package db

import (
	"context"
	"regexp"
	"strings"
)

// Task identity (#272).
//
// A spawned instance used to be titled with its definition's display name,
// which means every run of "Deploy" is called "Deploy" and every run of a
// container is called "Feature Request". On a board of 28 such cards the
// only differentiator was a badge. Instances now carry a composed title:
//
//	root     Feature Request (TASK-000119)
//	child    Feature Request · Deploy (TASK-000109)
//
// Composition happens once, at spawn, so every surface — board, list, form,
// search, CSV, map labels — reads the same string without a display hook.

// instanceTitle composes the stored title for a new instance.
func instanceTitle(ctx context.Context, q scriptQuerier, def *TaskDefinition, parentTaskID string) string {
	parentTaskID = strings.TrimSpace(parentTaskID)
	if parentTaskID == "" {
		return def.DisplayName
	}
	rootName, err := rootInstanceName(ctx, q, parentTaskID)
	if err != nil || rootName == "" {
		return def.DisplayName
	}
	return rootName + " · " + def.DisplayName
}

// composeInstanceTitle appends the task number to a composed title.
func composeInstanceTitle(title, number string) string {
	title = strings.TrimSpace(title)
	number = strings.TrimSpace(number)
	if number == "" {
		return title
	}
	if strings.Contains(title, "("+number+")") {
		return title
	}
	return title + " (" + number + ")"
}

// rootInstanceName returns the name of the top-most ancestor instance: its
// definition's display name where it has one, else its own title with any
// trailing number removed.
func rootInstanceName(ctx context.Context, q scriptQuerier, taskID string) (string, error) {
	var name string
	err := q.QueryRow(ctx, `
		WITH RECURSIVE chain AS (
			SELECT _id, parent_task_id, definition_id, title, 0 AS depth
			FROM base_task
			WHERE _id = $1 AND _deleted_at IS NULL
			UNION ALL
			SELECT p._id, p.parent_task_id, p.definition_id, p.title, c.depth + 1
			FROM base_task p
			JOIN chain c ON p._id = c.parent_task_id
			WHERE p._deleted_at IS NULL AND c.depth < 32
		)
		SELECT COALESCE(NULLIF(BTRIM(d.display_name), ''), c.title)
		FROM chain c
		LEFT JOIN base_task_definition d ON d._id = c.definition_id
		ORDER BY c.depth DESC
		LIMIT 1`, taskID).Scan(&name)
	if err != nil {
		return "", err
	}
	return stripTitleNumber(name), nil
}

var titleNumberSuffix = regexp.MustCompile(`\s*\((?:TASK|ENT)-[0-9]+\)\s*$`)

// stripTitleNumber removes a trailing " (TASK-000123)" so an ancestor's
// number is not inherited into a descendant's composed title.
func stripTitleNumber(title string) string {
	return strings.TrimSpace(titleNumberSuffix.ReplaceAllString(title, ""))
}
