package db

// Structural writes for a definition (Process Designer, Plan T4).
//
// The runtime reads the block tree; the canvas and the raw view are the two
// ways to write it (Requirements §4, AC-74). Both send the same document to
// one function, so the two cannot disagree about what was saved.
//
// Validation that only needs the payload lives in the handler, which is where
// a person sees the message. This file does the two things that need the
// database: resolve a task block's slug to a definition id, and replace the
// tree inside one transaction so a refused write leaves the previous document
// untouched.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// StructureBlock is one block of a submitted definition document. Key is the
// document-local identifier used by children to name their parent and by
// dependencies to name an edge; it is never stored.
type StructureBlock struct {
	Key       string         `json:"key"`
	Parent    string         `json:"parent"`
	Kind      string         `json:"kind"`
	Type      string         `json:"type"`
	Slug      string         `json:"slug"`
	Label     string         `json:"label"`
	Position  int            `json:"position"`
	Config    map[string]any `json:"config"`
	DependsOn []string       `json:"depends_on"`
	Mapping   map[string]any `json:"input_mapping"`
}

// SaveDefinitionStructure replaces a definition's block tree with blocks.
// The trigger, control and stage blocks call no definition; a task block must
// name a live definition by slug, which the caller has already validated.
func SaveDefinitionStructure(ctx context.Context, containerDefinitionID string, blocks []StructureBlock) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	definitionIDBySlug := map[string]string{}
	for _, b := range blocks {
		slug := strings.TrimSpace(b.Slug)
		if b.Kind != "task" || slug == "" {
			continue
		}
		if _, seen := definitionIDBySlug[slug]; seen {
			continue
		}
		var id string
		if err := tx.QueryRow(ctx,
			`SELECT _id::text FROM base_task_definition WHERE slug = $1 AND _deleted_at IS NULL`,
			slug).Scan(&id); err != nil {
			return fmt.Errorf("block %q: definition %q could not be resolved: %w", b.Label, slug, err)
		}
		definitionIDBySlug[slug] = id
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM base_task_definition_child WHERE container_definition_id = $1`,
		containerDefinitionID); err != nil {
		return err
	}

	blockIDByKey := map[string]string{}
	// Insert parents before children: the document order the caller sends is
	// a tree in pre-order, so one pass resolves every parent that exists.
	for index, b := range blocks {
		parentID := any(nil)
		if key := strings.TrimSpace(b.Parent); key != "" {
			id, found := blockIDByKey[key]
			if !found {
				return fmt.Errorf("block %q: its parent %q is not in the document", b.Label, key)
			}
			parentID = id
		}
		var childID any
		if id, found := definitionIDBySlug[strings.TrimSpace(b.Slug)]; found {
			childID = id
		}
		position := b.Position
		if position == 0 {
			position = (index + 1) * 10
		}
		config := b.Config
		if config == nil {
			config = map[string]any{}
		}
		configJSON, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("block %q: config is not valid JSON: %w", b.Label, err)
		}
		mapping := b.Mapping
		if mapping == nil {
			mapping = map[string]any{}
		}
		mappingJSON, err := json.Marshal(mapping)
		if err != nil {
			return fmt.Errorf("block %q: input_mapping is not valid JSON: %w", b.Label, err)
		}
		var blockID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO base_task_definition_child
				(container_definition_id, child_definition_id, parent_block_id,
				 position, sort_order, block_kind, block_type, config, input_mapping)
			VALUES ($1, $2, $3, $4, $4, $5, NULLIF($6, ''), $7::jsonb, $8::jsonb)
			RETURNING _id::text`,
			containerDefinitionID, childID, parentID, position, b.Kind,
			strings.TrimSpace(b.Type), string(configJSON), string(mappingJSON)).Scan(&blockID); err != nil {
			return fmt.Errorf("block %q: %w", b.Label, err)
		}
		if key := strings.TrimSpace(b.Key); key != "" {
			blockIDByKey[key] = blockID
		}
	}

	// Edges last: every block id is known now. An edge to a key outside the
	// document is the caller's error, named rather than stored as a dangling
	// reference.
	for _, b := range blocks {
		if len(b.DependsOn) == 0 {
			continue
		}
		blockID, found := blockIDByKey[strings.TrimSpace(b.Key)]
		if !found {
			return fmt.Errorf("block %q: a dependency needs the block to have a key", b.Label)
		}
		ids := make([]string, 0, len(b.DependsOn))
		for _, key := range b.DependsOn {
			id, ok := blockIDByKey[strings.TrimSpace(key)]
			if !ok {
				return fmt.Errorf("block %q: depends on %q, which is not in the document", b.Label, key)
			}
			ids = append(ids, id)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE base_task_definition_child SET depends_on_block_id = $2 WHERE _id = $1`,
			blockID, ids); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
