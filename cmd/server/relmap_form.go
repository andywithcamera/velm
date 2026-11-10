package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/relmap"
)

// handleFormGraph serves GET /f/graph/{table}/{id}?depth=N — the graph payload
// for the form-embedded relationship map (spec: Form-Embedded Relationship Map,
// phase 1). The root is resolved from the record on screen:
//
//   - base_entity: the record itself.
//   - base_task: the task's first base_task_entity target (task nodes hang off
//     shared entities in phase 1; hierarchy containers arrive in phase 2).
//   - any other table: the registry entity whose source pair
//     (source_system = table, source_record_id = id) matches, else
//     external_ref lookup, else 404-equivalent empty state.
func handleFormGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/f/graph/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	table, id := parts[0], parts[1]
	if !db.IsValidRecordID(table, id) {
		http.Error(w, "Invalid record ID", http.StatusBadRequest)
		return
	}

	depth := relmap.ParseDepth(r.URL.Query().Get("depth"))
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	ctx := r.Context()

	rootID, rootKind, ok := resolveFormGraphRoot(ctx, table, id)
	if !ok {
		writeFormGraph(w, map[string]any{"root_kind": "none", "nodes": []any{}, "edges": []any{}, "depth": depth})
		return
	}

	graph, root, err := relmap.LoadGraph(ctx, userID, rootID, depth)
	switch {
	case err == nil:
	case strings.Contains(err.Error(), "not found"):
		http.Error(w, "Not found", http.StatusNotFound)
		return
	case strings.Contains(err.Error(), "forbidden"):
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	default:
		http.Error(w, "Failed to load graph", http.StatusInternalServerError)
		return
	}
	writeFormGraph(w, map[string]any{
		"root_kind":        rootKind,
		"root_id":          root.ID,
		"centre_entity_id": root.ID,
		"depth":            depth,
		"nodes":            graph.Nodes,
		"edges":            graph.Edges,
	})
}

func writeFormGraph(w http.ResponseWriter, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// resolveFormGraphRoot maps (table, record id) to a graph root entity.
func resolveFormGraphRoot(ctx context.Context, table, id string) (rootID, rootKind string, ok bool) {
	switch table {
	case "base_entity":
		return id, "entity", true
	case "base_task":
		var entityID string
		err := db.Pool.QueryRow(ctx,
			`SELECT te.entity_id FROM base_task_entity te
			 JOIN base_entity e ON e._id = te.entity_id AND e._deleted_at IS NULL
			 WHERE te.task_id = $1
			 ORDER BY te._created_at
			 LIMIT 1`, id).Scan(&entityID)
		if err != nil || strings.TrimSpace(entityID) == "" {
			return "", "", false
		}
		return entityID, "task", true
	default:
		var entityID string
		err := db.Pool.QueryRow(ctx,
			`SELECT _id FROM base_entity
			 WHERE _deleted_at IS NULL AND source_system = $1 AND source_record_id = $2
			 ORDER BY _created_at LIMIT 1`, table, id).Scan(&entityID)
		if err == nil && strings.TrimSpace(entityID) != "" {
			return entityID, "entity", true
		}
		err = db.Pool.QueryRow(ctx,
			`SELECT _id FROM base_entity
			 WHERE _deleted_at IS NULL AND external_ref = $1
			 ORDER BY _created_at LIMIT 1`, table+"/"+id).Scan(&entityID)
		if err == nil && strings.TrimSpace(entityID) != "" {
			return entityID, "entity", true
		}
		return "", "", false
	}
}
