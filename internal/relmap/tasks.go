package relmap

import (
	"context"
	"sort"

	"velm/internal/db"
)

// taskRef is a minimal task row for graph purposes. Task nodes ride the same
// graph payload as entities: they enter through base_task_entity links, then
// their hierarchy (ancestors, children) is attached around them.
type taskRef struct {
	ID       string
	Number   string
	Title    string
	State    string
	ParentID string
}

func loadTaskRefsByID(ctx context.Context, ids []string) ([]taskRef, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := db.Pool.Query(ctx, `
		SELECT _id::text, number, title, state::text,
		       COALESCE(parent_task_id::text, '')
		FROM base_task
		WHERE _id::text = ANY($1) AND _deleted_at IS NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []taskRef
	for rows.Next() {
		var t taskRef
		if err := rows.Scan(&t.ID, &t.Number, &t.Title, &t.State, &t.ParentID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// taskNodesForEntities returns task nodes and hierarchy edges for tasks linked
// to the given entities: the tasks themselves, their ancestor chains, and
// their direct children. Edges: parent-of (task→task), linked-to (task→entity).
func taskNodesForEntities(ctx context.Context, entityIDs []string) ([]Node, []Edge, error) {
	if len(entityIDs) == 0 {
		return nil, nil, nil
	}
	linkRows, err := db.Pool.Query(ctx, `
		SELECT DISTINCT te.task_id::text, te.entity_id::text
		FROM base_task_entity te
		JOIN base_task t ON t._id = te.task_id AND t._deleted_at IS NULL
		WHERE te.entity_id::text = ANY($1) AND te._deleted_at IS NULL`, entityIDs)
	if err != nil {
		return nil, nil, err
	}
	taskEntity := map[string][]string{}
	var linked []string
	for linkRows.Next() {
		var taskID, entityID string
		if err := linkRows.Scan(&taskID, &entityID); err != nil {
			linkRows.Close()
			return nil, nil, err
		}
		taskEntity[taskID] = append(taskEntity[taskID], entityID)
		linked = append(linked, taskID)
	}
	linkRows.Close()
	if err := linkRows.Err(); err != nil {
		return nil, nil, err
	}
	linked = dedupe(linked)
	if len(linked) == 0 {
		return nil, nil, nil
	}

	// Ancestor chains: walk parent_task_id upward from each linked task.
	ancestorSet := map[string]bool{}
	for _, taskID := range linked {
		parent := taskID
		for hop := 0; hop < 32; hop++ { // depth cap guards against corrupt cycles
			var next string
			err := db.Pool.QueryRow(ctx, `
				SELECT COALESCE(parent_task_id::text, '')
				FROM base_task WHERE _id::text = $1 AND _deleted_at IS NULL`, parent).Scan(&next)
			if err != nil || next == "" || ancestorSet[next] {
				break
			}
			ancestorSet[next] = true
			parent = next
		}
	}
	// Direct children of everything we already hold.
	hold := dedupe(append(append([]string{}, linked...), keysOf(ancestorSet)...))
	childRows, err := db.Pool.Query(ctx, `
		SELECT _id::text FROM base_task
		WHERE parent_task_id::text = ANY($1) AND _deleted_at IS NULL`, hold)
	if err != nil {
		return nil, nil, err
	}
	var childIDs []string
	for childRows.Next() {
		var id string
		if err := childRows.Scan(&id); err != nil {
			childRows.Close()
			return nil, nil, err
		}
		childIDs = append(childIDs, id)
	}
	childRows.Close()
	if err := childRows.Err(); err != nil {
		return nil, nil, err
	}

	allTasks, err := loadTaskRefsByID(ctx, dedupe(append(hold, childIDs...)))
	if err != nil {
		return nil, nil, err
	}
	byID := map[string]taskRef{}
	for _, t := range allTasks {
		byID[t.ID] = t
	}

	var nodes []Node
	for _, t := range byID {
		nodes = append(nodes, Node{
			ID:             "task:" + t.ID,
			Number:         t.Number,
			Name:           t.Title,
			EntityType:     "Task",
			LifecycleState: t.State,
			Level:          1,
			Distance:       1,
			RecordURL:      "/f/base_task/" + t.ID,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Number < nodes[j].Number })

	var edges []Edge
	for _, t := range byID {
		if t.ParentID != "" {
			if _, ok := byID[t.ParentID]; ok {
				edges = append(edges, Edge{
					ID:               "tpar-" + t.ID,
					SourceEntityID:   "task:" + t.ParentID,
					TargetEntityID:   "task:" + t.ID,
					RelationshipType: "parent-of",
					Status:           "active",
				})
			}
		}
	}
	for taskID, ents := range taskEntity {
		if _, ok := byID[taskID]; !ok {
			continue
		}
		for _, eid := range ents {
			edges = append(edges, Edge{
				ID:               "tent-" + taskID + "-" + eid,
				SourceEntityID:   "task:" + taskID,
				TargetEntityID:   eid,
				RelationshipType: "linked-to",
				Status:           "active",
			})
		}
	}
	return nodes, edges, nil
}

func keysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

func dedupe(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
