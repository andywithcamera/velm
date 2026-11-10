package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/utils"
)

// Form link management (spec: Form-Embedded Relationship Map, phase 2):
// inline add/remove of task–entity links and entity–entity relationships
// directly from the map panel. All endpoints are JSON POST, CSRF-checked,
// and gated on write permission in the app that owns the tables.
//
// POST /api/links/task-entity      {task_id, entity_id, op: link|unlink}
// POST /api/links/entity-rel       {source_entity_id, target_entity_id, relationship_type, description?, op: link|unlink}
// POST /api/links/task-rel         {task_id, depends_on_task_id?, ...} — reserved for phase 3
func handleFormLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var payload struct {
		Op               string `json:"op"`
		TaskID           string `json:"task_id"`
		EntityID         string `json:"entity_id"`
		SourceEntityID   string `json:"source_entity_id"`
		TargetEntityID   string `json:"target_entity_id"`
		RelationshipType string `json:"relationship_type"`
		Description      string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if payload.Op != "link" && payload.Op != "unlink" {
		http.Error(w, "Invalid op", http.StatusBadRequest)
		return
	}

	switch strings.TrimPrefix(r.URL.Path, "/api/links/") {
	case "task-entity":
		if !utils.IsValidUUID(payload.TaskID) || !utils.IsValidUUID(payload.EntityID) {
			http.Error(w, "Invalid IDs", http.StatusBadRequest)
			return
		}
		if err := linkTaskEntity(r, userID, payload); err != nil {
			writeLinkError(w, err)
			return
		}
	case "entity-rel":
		if !utils.IsValidUUID(payload.SourceEntityID) || !utils.IsValidUUID(payload.TargetEntityID) {
			http.Error(w, "Invalid IDs", http.StatusBadRequest)
			return
		}
		if err := linkEntityRelationship(r, userID, payload); err != nil {
			writeLinkError(w, err)
			return
		}
	default:
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func linkTaskEntity(r *http.Request, userID string, payload struct {
	Op               string `json:"op"`
	TaskID           string `json:"task_id"`
	EntityID         string `json:"entity_id"`
	SourceEntityID   string `json:"source_entity_id"`
	TargetEntityID   string `json:"target_entity_id"`
	RelationshipType string `json:"relationship_type"`
	Description      string `json:"description"`
}) error {
	if payload.Op == "link" {
		_, err := db.Pool.Exec(r.Context(), `
			INSERT INTO base_task_entity (task_id, entity_id, _created_by, _updated_by)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $3::uuid)
			ON CONFLICT (task_id, entity_id) DO UPDATE
				SET _deleted_at = NULL, _updated_at = NOW(), _updated_by = $3::uuid`,
			payload.TaskID, payload.EntityID, userID)
		return err
	}
	_, err := db.Pool.Exec(r.Context(), `
		UPDATE base_task_entity SET _deleted_at = NOW(), _deleted_by = $3::uuid, _updated_at = NOW(), _updated_by = $3::uuid
		WHERE task_id = $1::uuid AND entity_id = $2::uuid AND _deleted_at IS NULL`,
		payload.TaskID, payload.EntityID, userID)
	return err
}

func linkEntityRelationship(r *http.Request, userID string, payload struct {
	Op               string `json:"op"`
	TaskID           string `json:"task_id"`
	EntityID         string `json:"entity_id"`
	SourceEntityID   string `json:"source_entity_id"`
	TargetEntityID   string `json:"target_entity_id"`
	RelationshipType string `json:"relationship_type"`
	Description      string `json:"description"`
}) error {
	relType := strings.TrimSpace(payload.RelationshipType)
	if relType == "" || len(relType) > 64 {
		return errLinkInvalid("relationship_type is required (max 64 chars)")
	}
	if payload.SourceEntityID == payload.TargetEntityID {
		return errLinkInvalid("an entity cannot relate to itself")
	}
	if payload.Op == "link" {
		_, err := db.Pool.Exec(r.Context(), `
			INSERT INTO base_entity_relationship
				(source_entity_id, target_entity_id, relationship_type, status, description, _created_by, _updated_by)
			VALUES ($1::uuid, $2::uuid, $3, 'active', NULLIF($4, ''), $5::uuid, $5::uuid)`,
			payload.SourceEntityID, payload.TargetEntityID, relType, strings.TrimSpace(payload.Description), userID)
		return err
	}
	// Unlink by direction pair (either direction) when no edge id is provided.
	_, err := db.Pool.Exec(r.Context(), `
		UPDATE base_entity_relationship
		SET _deleted_at = NOW(), _deleted_by = $3::uuid, _updated_at = NOW(), _updated_by = $3::uuid
		WHERE _deleted_at IS NULL
		  AND ((source_entity_id = $1::uuid AND target_entity_id = $2::uuid)
		    OR (source_entity_id = $2::uuid AND target_entity_id = $1::uuid))`,
		payload.SourceEntityID, payload.TargetEntityID, userID)
	return err
}

type linkError struct{ msg string }

func (e linkError) Error() string { return e.msg }

func errLinkInvalid(msg string) error { return linkError{msg: msg} }

func writeLinkError(w http.ResponseWriter, err error) {
	if _, ok := err.(linkError); ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	log.Printf("form-links error: %v", err)
	http.Error(w, "Internal error", http.StatusInternalServerError)
}
