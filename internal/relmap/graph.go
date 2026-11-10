// Package relmap loads the relationship-map graph shared by the /map page and
// the per-form graph endpoint (form-embedded relationship map, phase 1).
package relmap

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"velm/internal/db"

	"github.com/jackc/pgx/v5"
)

const (
	DefaultDepth = 2
	MinDepth     = 1
	MaxDepth     = 4
)

var (
	ErrEntityNotFound  = errors.New("relationship map entity not found")
	ErrEntityForbidden = errors.New("relationship map entity forbidden")
)

type EntityRecord struct {
	ID                string
	Number            string
	Name              string
	Description       string
	EntityType        string
	LifecycleState    string
	OperationalStatus string
	Criticality       string
	SourceSystem      string
	SourceRecordID    string
	ExternalRef       string
	AssetTag          string
	SerialNumber      string
	OwnerEntityID     string
	ResponsibleGroup  string
	ResponsibleUser   string
}

type RelationshipRecord struct {
	ID               string
	SourceEntityID   string
	TargetEntityID   string
	RelationshipType string
	Status           string
	Description      string
	EffectiveFrom    string
	EffectiveTo      string
}

type Node struct {
	ID                string `json:"id"`
	Number            string `json:"number"`
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
	EntityType        string `json:"entity_type"`
	LifecycleState    string `json:"lifecycle_state"`
	OperationalStatus string `json:"operational_status"`
	Criticality       string `json:"criticality"`
	Level             int    `json:"level"`
	Distance          int    `json:"distance"`
	Degree            int    `json:"degree"`
	IncomingCount     int    `json:"incoming_count"`
	OutgoingCount     int    `json:"outgoing_count"`
	Selected          bool   `json:"selected"`
	RecordURL         string `json:"record_url"`
}

type Edge struct {
	ID               string `json:"id"`
	SourceEntityID   string `json:"source_entity_id"`
	TargetEntityID   string `json:"target_entity_id"`
	RelationshipType string `json:"relationship_type"`
	Status           string `json:"status"`
	Description      string `json:"description,omitempty"`
}

type Graph struct {
	CenterEntityID string `json:"center_entity_id"`
	Depth          int    `json:"depth"`
	MinLevel       int    `json:"min_level"`
	MaxLevel       int    `json:"max_level"`
	CeilingEntity  string `json:"ceiling_entity_id"`
	FloorEntity    string `json:"floor_entity_id"`
	Nodes          []Node `json:"nodes"`
	Edges          []Edge `json:"edges"`
}

func LoadGraph(ctx context.Context, userID, rootID string, maxDepth int) (Graph, Node, error) {
	root, found, err := loadRelationshipMapEntityByID(ctx, rootID)
	if err != nil {
		return Graph{}, Node{}, err
	}
	if !found {
		return Graph{}, Node{}, ErrEntityNotFound
	}

	entityEvaluator, err := db.LoadTableSecurityEvaluator(ctx, "base_entity", userID)
	if err != nil {
		return Graph{}, Node{}, err
	}
	if entityEvaluator != nil && !entityEvaluator.AllowsRecord("R", root.securityRecord()) {
		return Graph{}, Node{}, ErrEntityForbidden
	}

	relationshipEvaluator, err := db.LoadTableSecurityEvaluator(ctx, "base_entity_relationship", userID)
	if err != nil {
		return Graph{}, Node{}, err
	}

	levels := map[string]int{rootID: 0}
	distances := map[string]int{rootID: 0}
	discoveredIDs := map[string]bool{rootID: true}
	relationshipsByID := map[string]RelationshipRecord{}
	frontier := map[string]int{rootID: 0}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		frontierIDs := sortedRelationshipMapKeys(frontier)
		rows, err := loadRelationshipMapRelationshipsTouchingEntities(ctx, frontierIDs)
		if err != nil {
			return Graph{}, Node{}, err
		}

		nextFrontier := map[string]int{}
		for _, row := range rows {
			relationshipsByID[row.ID] = row

			if sourceLevel, ok := frontier[row.SourceEntityID]; ok {
				AdoptLevel(levels, distances, nextFrontier, row.TargetEntityID, sourceLevel+1, depth+1)
				discoveredIDs[row.TargetEntityID] = true
			}
			if targetLevel, ok := frontier[row.TargetEntityID]; ok {
				AdoptLevel(levels, distances, nextFrontier, row.SourceEntityID, targetLevel-1, depth+1)
				discoveredIDs[row.SourceEntityID] = true
			}
		}
		frontier = nextFrontier
	}

	entityRows, err := loadRelationshipMapEntitiesByID(ctx, sortedRelationshipMapBoolKeys(discoveredIDs))
	if err != nil {
		return Graph{}, Node{}, err
	}

	visibleEntities := map[string]EntityRecord{}
	for _, row := range entityRows {
		if entityEvaluator != nil && !entityEvaluator.AllowsRecord("R", row.securityRecord()) {
			continue
		}
		visibleEntities[row.ID] = row
	}
	if _, ok := visibleEntities[rootID]; !ok {
		return Graph{}, Node{}, ErrEntityForbidden
	}

	edges := make([]Edge, 0, len(relationshipsByID))
	incomingCounts := map[string]int{}
	outgoingCounts := map[string]int{}
	connectedNodes := map[string]bool{rootID: true}

	for _, row := range sortedRelationshipMapRelationships(relationshipsByID) {
		if relationshipEvaluator != nil && !relationshipEvaluator.AllowsRecord("R", row.securityRecord()) {
			continue
		}
		if _, ok := visibleEntities[row.SourceEntityID]; !ok {
			continue
		}
		if _, ok := visibleEntities[row.TargetEntityID]; !ok {
			continue
		}

		edges = append(edges, Edge{
			ID:               row.ID,
			SourceEntityID:   row.SourceEntityID,
			TargetEntityID:   row.TargetEntityID,
			RelationshipType: row.RelationshipType,
			Status:           row.Status,
			Description:      row.Description,
		})
		outgoingCounts[row.SourceEntityID]++
		incomingCounts[row.TargetEntityID]++
		connectedNodes[row.SourceEntityID] = true
		connectedNodes[row.TargetEntityID] = true
	}

	nodes := make([]Node, 0, len(visibleEntities))
	minLevel := 0
	maxLevel := 0
	for _, entityID := range sortedRelationshipMapEntityIDs(visibleEntities, levels) {
		if !connectedNodes[entityID] {
			continue
		}
		row := visibleEntities[entityID]
		level := levels[entityID]
		if len(nodes) == 0 || level < minLevel {
			minLevel = level
		}
		if len(nodes) == 0 || level > maxLevel {
			maxLevel = level
		}
		nodes = append(nodes, Node{
			ID:                row.ID,
			Number:            row.Number,
			Name:              row.Name,
			Description:       row.Description,
			EntityType:        row.EntityType,
			LifecycleState:    row.LifecycleState,
			OperationalStatus: row.OperationalStatus,
			Criticality:       row.Criticality,
			Level:             level,
			Distance:          distances[entityID],
			Degree:            incomingCounts[entityID] + outgoingCounts[entityID],
			IncomingCount:     incomingCounts[entityID],
			OutgoingCount:     outgoingCounts[entityID],
			Selected:          entityID == rootID,
			RecordURL:         "/f/base_entity/" + entityID,
		})
	}

	// Task layer: tasks linked to any visible entity, with hierarchy
	// (ancestors, siblings via shared parents, children). Task nodes are
	// additive to the entity graph; a task failure must not break it.
	taskNodes, taskEdges, taskErr := taskNodesForEntities(ctx, visibleEntityIDs(visibleEntities))
	if taskErr == nil {
		taskSet := map[string]bool{}
		for _, n := range taskNodes {
			taskSet[n.ID] = true
		}
		nodeIDs := map[string]bool{}
		for _, n := range nodes {
			nodeIDs[n.ID] = true
		}
		for _, e := range taskEdges {
			if taskSet[e.SourceEntityID] && (nodeIDs[e.TargetEntityID] || taskSet[e.TargetEntityID]) {
				edges = append(edges, e)
				if taskSet[e.TargetEntityID] && !nodeIDs[e.TargetEntityID] {
					connectedNodes[e.TargetEntityID] = true
				}
			}
		}
		for _, n := range taskNodes {
			// keep only tasks that ended up connected
			if connectedNodes[n.ID] {
				nodes = append(nodes, n)
			}
		}
	}
	ceilingEntity, floorEntity := SelectTethers(nodes)
	selectedNode := Node{}
	for _, node := range nodes {
		if node.Selected {
			selectedNode = node
			break
		}
	}

	return Graph{
		CenterEntityID: rootID,
		Depth:          maxDepth,
		MinLevel:       minLevel,
		MaxLevel:       maxLevel,
		CeilingEntity:  ceilingEntity,
		FloorEntity:    floorEntity,
		Nodes:          nodes,
		Edges:          edges,
	}, selectedNode, nil
}

func visibleEntityIDs(entities map[string]EntityRecord) []string {
	out := make([]string, 0, len(entities))
	for id := range entities {
		out = append(out, id)
	}
	return out
}

func AdoptLevel(levels, distances, frontier map[string]int, entityID string, candidateLevel, candidateDistance int) {
	if strings.TrimSpace(entityID) == "" {
		return
	}
	existingDistance, ok := distances[entityID]
	if !ok || candidateDistance < existingDistance || (candidateDistance == existingDistance && PreferLevel(levels[entityID], candidateLevel)) {
		distances[entityID] = candidateDistance
		levels[entityID] = candidateLevel
		frontier[entityID] = candidateLevel
	}
}

func PreferLevel(existing, candidate int) bool {
	if intAbs(candidate) != intAbs(existing) {
		return intAbs(candidate) < intAbs(existing)
	}
	if candidate == existing {
		return false
	}
	return candidate < existing
}

func SelectTethers(nodes []Node) (string, string) {
	if len(nodes) == 0 {
		return "", ""
	}
	ceiling := nodes[0]
	floor := nodes[0]
	for _, node := range nodes[1:] {
		if NodeRanksBefore(node, ceiling, true) {
			ceiling = node
		}
		if NodeRanksBefore(node, floor, false) {
			floor = node
		}
	}
	return ceiling.ID, floor.ID
}

func NodeRanksBefore(candidate, current Node, ascending bool) bool {
	if ascending {
		if candidate.Level != current.Level {
			return candidate.Level < current.Level
		}
	} else {
		if candidate.Level != current.Level {
			return candidate.Level > current.Level
		}
	}
	if candidate.Degree != current.Degree {
		return candidate.Degree > current.Degree
	}
	if !strings.EqualFold(candidate.Name, current.Name) {
		return strings.ToLower(candidate.Name) < strings.ToLower(current.Name)
	}
	return candidate.ID < current.ID
}

func loadRelationshipMapEntityByID(ctx context.Context, id string) (EntityRecord, bool, error) {
	query := `
		SELECT
			_id::text,
			number,
			name,
			COALESCE(description, ''),
			entity_type,
			lifecycle_state,
			operational_status,
			criticality,
			COALESCE(source_system, ''),
			COALESCE(source_record_id, ''),
			COALESCE(external_ref, ''),
			COALESCE(asset_tag, ''),
			COALESCE(serial_number, ''),
			COALESCE(owner_entity_id::text, ''),
			COALESCE(responsible_group_id::text, ''),
			COALESCE(responsible_user_id::text, '')
		FROM base_entity
		WHERE _deleted_at IS NULL
		  AND _id::text = $1
		LIMIT 1
	`

	var row EntityRecord
	err := db.Pool.QueryRow(ctx, query, id).Scan(
		&row.ID,
		&row.Number,
		&row.Name,
		&row.Description,
		&row.EntityType,
		&row.LifecycleState,
		&row.OperationalStatus,
		&row.Criticality,
		&row.SourceSystem,
		&row.SourceRecordID,
		&row.ExternalRef,
		&row.AssetTag,
		&row.SerialNumber,
		&row.OwnerEntityID,
		&row.ResponsibleGroup,
		&row.ResponsibleUser,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return EntityRecord{}, false, nil
	}
	if err != nil {
		return EntityRecord{}, false, err
	}
	return row, true, nil
}

func loadRelationshipMapEntitiesByID(ctx context.Context, ids []string) ([]EntityRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	query := fmt.Sprintf(`
		SELECT
			_id::text,
			number,
			name,
			COALESCE(description, ''),
			entity_type,
			lifecycle_state,
			operational_status,
			criticality,
			COALESCE(source_system, ''),
			COALESCE(source_record_id, ''),
			COALESCE(external_ref, ''),
			COALESCE(asset_tag, ''),
			COALESCE(serial_number, ''),
			COALESCE(owner_entity_id::text, ''),
			COALESCE(responsible_group_id::text, ''),
			COALESCE(responsible_user_id::text, '')
		FROM base_entity
		WHERE _deleted_at IS NULL
		  AND _id::text IN (%s)
	`, sqlPlaceholderRange(1, len(ids)))

	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]EntityRecord, 0, len(ids))
	for rows.Next() {
		var row EntityRecord
		if err := rows.Scan(
			&row.ID,
			&row.Number,
			&row.Name,
			&row.Description,
			&row.EntityType,
			&row.LifecycleState,
			&row.OperationalStatus,
			&row.Criticality,
			&row.SourceSystem,
			&row.SourceRecordID,
			&row.ExternalRef,
			&row.AssetTag,
			&row.SerialNumber,
			&row.OwnerEntityID,
			&row.ResponsibleGroup,
			&row.ResponsibleUser,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func loadRelationshipMapRelationshipsTouchingEntities(ctx context.Context, ids []string) ([]RelationshipRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := sqlPlaceholderRange(1, len(ids))
	query := fmt.Sprintf(`
		SELECT
			_id::text,
			source_entity_id::text,
			target_entity_id::text,
			relationship_type,
			status,
			COALESCE(description, ''),
			COALESCE(to_char(effective_from AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), ''),
			COALESCE(to_char(effective_to AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')
		FROM base_entity_relationship
		WHERE _deleted_at IS NULL
		  AND (
			source_entity_id::text IN (%s)
			OR target_entity_id::text IN (%s)
		  )
	`, placeholders, placeholders)

	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]RelationshipRecord, 0, allocHintMul(len(ids), 2))
	for rows.Next() {
		var row RelationshipRecord
		if err := rows.Scan(
			&row.ID,
			&row.SourceEntityID,
			&row.TargetEntityID,
			&row.RelationshipType,
			&row.Status,
			&row.Description,
			&row.EffectiveFrom,
			&row.EffectiveTo,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func sqlPlaceholderRange(start, count int) string {
	parts := make([]string, 0, count)
	for i := 0; i < count; i++ {
		parts = append(parts, fmt.Sprintf("$%d", start+i))
	}
	return strings.Join(parts, ", ")
}

func sortedRelationshipMapKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedRelationshipMapBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key, ok := range values {
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedRelationshipMapRelationships(values map[string]RelationshipRecord) []RelationshipRecord {
	rows := make([]RelationshipRecord, 0, len(values))
	for _, row := range values {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Status != rows[j].Status {
			return rows[i].Status < rows[j].Status
		}
		if rows[i].RelationshipType != rows[j].RelationshipType {
			return rows[i].RelationshipType < rows[j].RelationshipType
		}
		if rows[i].SourceEntityID != rows[j].SourceEntityID {
			return rows[i].SourceEntityID < rows[j].SourceEntityID
		}
		if rows[i].TargetEntityID != rows[j].TargetEntityID {
			return rows[i].TargetEntityID < rows[j].TargetEntityID
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

func sortedRelationshipMapEntityIDs(values map[string]EntityRecord, levels map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := values[keys[i]]
		right := values[keys[j]]
		if levels[left.ID] != levels[right.ID] {
			return levels[left.ID] < levels[right.ID]
		}
		if left.Name != right.Name {
			return strings.ToLower(left.Name) < strings.ToLower(right.Name)
		}
		return left.ID < right.ID
	})
	return keys
}

func intAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (row EntityRecord) securityRecord() map[string]any {
	return map[string]any{
		"_id":                  row.ID,
		"number":               row.Number,
		"name":                 row.Name,
		"description":          nullableString(row.Description),
		"entity_type":          row.EntityType,
		"lifecycle_state":      row.LifecycleState,
		"operational_status":   row.OperationalStatus,
		"criticality":          row.Criticality,
		"source_system":        nullableString(row.SourceSystem),
		"source_record_id":     nullableString(row.SourceRecordID),
		"external_ref":         nullableString(row.ExternalRef),
		"asset_tag":            nullableString(row.AssetTag),
		"serial_number":        nullableString(row.SerialNumber),
		"owner_entity_id":      nullableString(row.OwnerEntityID),
		"responsible_group_id": nullableString(row.ResponsibleGroup),
		"responsible_user_id":  nullableString(row.ResponsibleUser),
	}
}

func (row RelationshipRecord) securityRecord() map[string]any {
	return map[string]any{
		"_id":               row.ID,
		"source_entity_id":  row.SourceEntityID,
		"target_entity_id":  row.TargetEntityID,
		"relationship_type": row.RelationshipType,
		"status":            row.Status,
		"description":       nullableString(row.Description),
		"effective_from":    nullableString(row.EffectiveFrom),
		"effective_to":      nullableString(row.EffectiveTo),
	}
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// allocHintMul returns a safe preallocation hint for repeated expansions.
func allocHintMul(value, factor int) int {
	if value <= 0 || factor <= 0 {
		return 0
	}
	hint := value * factor
	if hint/factor != value {
		return 0
	}
	return hint
}

// ParseDepth clamps a requested depth to the allowed range.
func ParseDepth(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < MinDepth {
		return DefaultDepth
	}
	if value > MaxDepth {
		return MaxDepth
	}
	return value
}
