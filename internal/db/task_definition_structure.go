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
			position = index * 10
		}
		config := map[string]any{}
		for key, value := range b.Config {
			config[key] = value
		}
		// The label is part of the document: the canvas and the plumbing
		// errors name the author's words, not a uuid.
		if label := strings.TrimSpace(b.Label); label != "" {
			config["label"] = label
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

// DocumentBlock is one block of a stored definition tree, with the facts the
// canvas needs to render it: the child definition's name and whether that
// child is a container (so a chain is visible before it is ordered), and the
// block's own assignment override. Requirements AC-69 to AC-73.
type DocumentBlock struct {
	ID                 string
	ParentID           string
	ChildDefinitionID  string
	Slug               string
	DisplayName        string
	IsContainer        bool
	ChildStepCount     int
	InputSchemaYAML    string
	VariablesYAML      string
	Kind               string
	Type               string
	Label              string
	Position           int
	Config             map[string]any
	InputMapping       map[string]any
	DependsOnBlockIDs  []string
	AssignmentGroupID  string
	AssignmentStrategy string
	AssignedUserID     string
}

// LoadDefinitionDocument reads a definition's block tree in document order,
// joined to the child definitions. It is the one loader behind the canvas and
// the raw view (Requirements AC-70).
func LoadDefinitionDocument(ctx context.Context, containerDefinitionID string) ([]DocumentBlock, error) {
	rows, err := Pool.Query(ctx, `
		SELECT c._id::text, COALESCE(c.parent_block_id::text, ''),
			COALESCE(c.child_definition_id::text, ''),
			COALESCE(d.slug, ''), COALESCE(d.display_name, ''),
			COALESCE(d.is_container, false),
			(SELECT count(*) FROM base_task_definition_child cc
			  WHERE cc.container_definition_id = c.child_definition_id),
			COALESCE(d.input_schema_yaml, ''), COALESCE(d.variables_yaml, ''),
			COALESCE(c.block_kind, 'task'), COALESCE(c.block_type, ''),
			COALESCE(c.config->>'label', ''), c.position, c.config, c.input_mapping,
			COALESCE(c.depends_on_block_id, '{}'),
			COALESCE(c.assignment_group_id::text, ''),
			COALESCE(c.assignment_strategy, ''),
			COALESCE(c.assigned_user_id::text, '')
		FROM base_task_definition_child c
		LEFT JOIN base_task_definition d ON d._id = c.child_definition_id
		WHERE c.container_definition_id = $1
		ORDER BY c.position, c.sort_order, c._created_at`, containerDefinitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var blocks []DocumentBlock
	for rows.Next() {
		var b DocumentBlock
		var cfg []byte
		if err := rows.Scan(&b.ID, &b.ParentID, &b.ChildDefinitionID,
			&b.Slug, &b.DisplayName, &b.IsContainer, &b.ChildStepCount,
			&b.InputSchemaYAML, &b.VariablesYAML, &b.Kind, &b.Type, &b.Label,
			&b.Position, &cfg, &b.InputMapping, &b.DependsOnBlockIDs,
			&b.AssignmentGroupID, &b.AssignmentStrategy, &b.AssignedUserID); err != nil {
			return nil, err
		}
		b.Config = map[string]any{}
		if len(cfg) > 0 {
			_ = json.Unmarshal(cfg, &b.Config)
		}
		if b.InputMapping == nil {
			b.InputMapping = map[string]any{}
		}
		blocks = append(blocks, b)
	}
	return blocks, rows.Err()
}

// StoredStructureDocument turns the stored tree back into the document the
// canvas posts. Used by the order gate (Requirements AC-86) so a definition
// that cannot be saved cannot be started either. Keys are block ids because
// the stored tree has no document-local keys.
func StoredStructureDocument(ctx context.Context, containerDefinitionID string) ([]StructureBlock, error) {
	blocks, err := LoadDefinitionDocument(ctx, containerDefinitionID)
	if err != nil {
		return nil, err
	}
	document := make([]StructureBlock, 0, len(blocks))
	location := make(map[string]DocumentBlock, len(blocks))
	for _, b := range blocks {
		location[b.ID] = b
	}
	for _, b := range blocks {
		label := b.Label
		if label == "" {
			label = b.DisplayName
		}
		parent := b.ParentID
		if parent != "" {
			parent = parent
		}
		document = append(document, StructureBlock{
			Key: b.ID, Parent: parent, Kind: b.Kind, Type: b.Type,
			Slug: b.Slug, Label: label, Position: b.Position,
			Config: b.Config, Mapping: b.InputMapping,
			DependsOn: b.DependsOnBlockIDs,
		})
	}
	return document, nil
}
