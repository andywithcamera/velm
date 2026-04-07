package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"

	"github.com/jackc/pgx/v5"
)

const (
	relationshipMapDefaultDepth = 2
	relationshipMapMinDepth     = 1
	relationshipMapMaxDepth     = 4
)

var (
	errRelationshipMapEntityNotFound  = errors.New("relationship map entity not found")
	errRelationshipMapEntityForbidden = errors.New("relationship map entity forbidden")
)

type relationshipMapEntityRecord struct {
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

type relationshipMapRelationshipRecord struct {
	ID               string
	SourceEntityID   string
	TargetEntityID   string
	RelationshipType string
	Status           string
	Description      string
	EffectiveFrom    string
	EffectiveTo      string
}

type relationshipMapNode struct {
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

type relationshipMapEdge struct {
	ID               string `json:"id"`
	SourceEntityID   string `json:"source_entity_id"`
	TargetEntityID   string `json:"target_entity_id"`
	RelationshipType string `json:"relationship_type"`
	Status           string `json:"status"`
	Description      string `json:"description,omitempty"`
}

type relationshipMapGraph struct {
	CenterEntityID string                `json:"center_entity_id"`
	Depth          int                   `json:"depth"`
	MinLevel       int                   `json:"min_level"`
	MaxLevel       int                   `json:"max_level"`
	CeilingEntity  string                `json:"ceiling_entity_id"`
	FloorEntity    string                `json:"floor_entity_id"`
	Nodes          []relationshipMapNode `json:"nodes"`
	Edges          []relationshipMapEdge `json:"edges"`
}

func handleRelationshipMapPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	entityID := strings.TrimSpace(r.URL.Query().Get("entity"))
	if entityID != "" && !db.IsValidRecordID("base_entity", entityID) {
		http.Error(w, "Invalid entity ID", http.StatusBadRequest)
		return
	}

	depth := parseRelationshipMapDepth(r.URL.Query().Get("depth"))
	viewData := newViewData(w, r, "/map", "Relationship Map", "Builder")
	viewData["Uri"] = requestURIWithDefault(r, "/map")
	viewData["View"] = "map"
	viewData["MapHasEntity"] = entityID != ""
	viewData["MapDepth"] = depth
	viewData["MapDepthMin"] = relationshipMapMinDepth
	viewData["MapDepthMax"] = relationshipMapMaxDepth
	viewData["MapSelectorAction"] = "/map"

	entityConfig := formFieldConfig{
		Kind:                "reference",
		ReferenceTo:         "base_entity",
		ReferenceTableLabel: referenceTableLabelWithContext(ctx, "base_entity"),
	}
	if entityID != "" {
		if selectedOption, ok := fetchReferenceOptionByID(ctx, "base_entity", entityID); ok {
			entityConfig.ReferenceRows = []formReferenceOption{selectedOption}
		}
	}
	viewData["MapEntityFieldConfig"] = entityConfig
	viewData["MapEntityValue"] = entityID

	if entityID == "" {
		if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
			http.Error(w, "Failed to render relationship map", http.StatusInternalServerError)
		}
		return
	}

	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	graph, selected, err := loadRelationshipMapGraph(ctx, userID, entityID, depth)
	switch {
	case errors.Is(err, errRelationshipMapEntityNotFound):
		http.Error(w, "Entity not found", http.StatusNotFound)
		return
	case errors.Is(err, errRelationshipMapEntityForbidden):
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	case err != nil:
		http.Error(w, "Failed to load relationship map", http.StatusInternalServerError)
		return
	}

	graphJSON, err := json.Marshal(graph)
	if err != nil {
		http.Error(w, "Failed to encode relationship map", http.StatusInternalServerError)
		return
	}

	title := "Relationship Map"
	if strings.TrimSpace(selected.Name) != "" {
		title = "Relationship Map: " + strings.TrimSpace(selected.Name)
	}
	viewData["Title"] = title
	viewData["PageTitle"] = title
	viewData["MapGraphJSON"] = template.JS(string(graphJSON))
	viewData["MapSelectedEntity"] = selected
	viewData["MapNodeCount"] = len(graph.Nodes)
	viewData["MapEdgeCount"] = len(graph.Edges)

	if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
		http.Error(w, "Failed to render relationship map", http.StatusInternalServerError)
	}
}

func requestURIWithDefault(r *http.Request, fallback string) string {
	if r == nil || r.URL == nil {
		return fallback
	}
	if uri := strings.TrimSpace(r.URL.RequestURI()); uri != "" {
		return uri
	}
	return fallback
}

func parseRelationshipMapDepth(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return relationshipMapDefaultDepth
	}
	if value < relationshipMapMinDepth {
		return relationshipMapMinDepth
	}
	if value > relationshipMapMaxDepth {
		return relationshipMapMaxDepth
	}
	return value
}

func loadRelationshipMapGraph(ctx context.Context, userID, rootID string, maxDepth int) (relationshipMapGraph, relationshipMapNode, error) {
	root, found, err := loadRelationshipMapEntityByID(ctx, rootID)
	if err != nil {
		return relationshipMapGraph{}, relationshipMapNode{}, err
	}
	if !found {
		return relationshipMapGraph{}, relationshipMapNode{}, errRelationshipMapEntityNotFound
	}

	entityEvaluator, err := db.LoadTableSecurityEvaluator(ctx, "base_entity", userID)
	if err != nil {
		return relationshipMapGraph{}, relationshipMapNode{}, err
	}
	if entityEvaluator != nil && !entityEvaluator.AllowsRecord("R", root.securityRecord()) {
		return relationshipMapGraph{}, relationshipMapNode{}, errRelationshipMapEntityForbidden
	}

	relationshipEvaluator, err := db.LoadTableSecurityEvaluator(ctx, "base_entity_relationship", userID)
	if err != nil {
		return relationshipMapGraph{}, relationshipMapNode{}, err
	}

	levels := map[string]int{rootID: 0}
	distances := map[string]int{rootID: 0}
	discoveredIDs := map[string]bool{rootID: true}
	relationshipsByID := map[string]relationshipMapRelationshipRecord{}
	frontier := map[string]int{rootID: 0}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		frontierIDs := sortedRelationshipMapKeys(frontier)
		rows, err := loadRelationshipMapRelationshipsTouchingEntities(ctx, frontierIDs)
		if err != nil {
			return relationshipMapGraph{}, relationshipMapNode{}, err
		}

		nextFrontier := map[string]int{}
		for _, row := range rows {
			relationshipsByID[row.ID] = row

			if sourceLevel, ok := frontier[row.SourceEntityID]; ok {
				relationshipMapAdoptLevel(levels, distances, nextFrontier, row.TargetEntityID, sourceLevel+1, depth+1)
				discoveredIDs[row.TargetEntityID] = true
			}
			if targetLevel, ok := frontier[row.TargetEntityID]; ok {
				relationshipMapAdoptLevel(levels, distances, nextFrontier, row.SourceEntityID, targetLevel-1, depth+1)
				discoveredIDs[row.SourceEntityID] = true
			}
		}
		frontier = nextFrontier
	}

	entityRows, err := loadRelationshipMapEntitiesByID(ctx, sortedRelationshipMapBoolKeys(discoveredIDs))
	if err != nil {
		return relationshipMapGraph{}, relationshipMapNode{}, err
	}

	visibleEntities := map[string]relationshipMapEntityRecord{}
	for _, row := range entityRows {
		if entityEvaluator != nil && !entityEvaluator.AllowsRecord("R", row.securityRecord()) {
			continue
		}
		visibleEntities[row.ID] = row
	}
	if _, ok := visibleEntities[rootID]; !ok {
		return relationshipMapGraph{}, relationshipMapNode{}, errRelationshipMapEntityForbidden
	}

	edges := make([]relationshipMapEdge, 0, len(relationshipsByID))
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

		edges = append(edges, relationshipMapEdge{
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

	nodes := make([]relationshipMapNode, 0, len(visibleEntities))
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
		nodes = append(nodes, relationshipMapNode{
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

	ceilingEntity, floorEntity := selectRelationshipMapTethers(nodes)
	selectedNode := relationshipMapNode{}
	for _, node := range nodes {
		if node.Selected {
			selectedNode = node
			break
		}
	}

	return relationshipMapGraph{
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

func relationshipMapAdoptLevel(levels, distances, frontier map[string]int, entityID string, candidateLevel, candidateDistance int) {
	if strings.TrimSpace(entityID) == "" {
		return
	}
	existingDistance, ok := distances[entityID]
	if !ok || candidateDistance < existingDistance || (candidateDistance == existingDistance && relationshipMapPreferLevel(levels[entityID], candidateLevel)) {
		distances[entityID] = candidateDistance
		levels[entityID] = candidateLevel
		frontier[entityID] = candidateLevel
	}
}

func relationshipMapPreferLevel(existing, candidate int) bool {
	if intAbs(candidate) != intAbs(existing) {
		return intAbs(candidate) < intAbs(existing)
	}
	if candidate == existing {
		return false
	}
	return candidate < existing
}

func selectRelationshipMapTethers(nodes []relationshipMapNode) (string, string) {
	if len(nodes) == 0 {
		return "", ""
	}
	ceiling := nodes[0]
	floor := nodes[0]
	for _, node := range nodes[1:] {
		if relationshipMapNodeRanksBefore(node, ceiling, true) {
			ceiling = node
		}
		if relationshipMapNodeRanksBefore(node, floor, false) {
			floor = node
		}
	}
	return ceiling.ID, floor.ID
}

func relationshipMapNodeRanksBefore(candidate, current relationshipMapNode, ascending bool) bool {
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
	if strings.ToLower(candidate.Name) != strings.ToLower(current.Name) {
		return strings.ToLower(candidate.Name) < strings.ToLower(current.Name)
	}
	return candidate.ID < current.ID
}

func loadRelationshipMapEntityByID(ctx context.Context, id string) (relationshipMapEntityRecord, bool, error) {
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

	var row relationshipMapEntityRecord
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
		return relationshipMapEntityRecord{}, false, nil
	}
	if err != nil {
		return relationshipMapEntityRecord{}, false, err
	}
	return row, true, nil
}

func loadRelationshipMapEntitiesByID(ctx context.Context, ids []string) ([]relationshipMapEntityRecord, error) {
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

	result := make([]relationshipMapEntityRecord, 0, len(ids))
	for rows.Next() {
		var row relationshipMapEntityRecord
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

func loadRelationshipMapRelationshipsTouchingEntities(ctx context.Context, ids []string) ([]relationshipMapRelationshipRecord, error) {
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

	result := make([]relationshipMapRelationshipRecord, 0, len(ids)*2)
	for rows.Next() {
		var row relationshipMapRelationshipRecord
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

func sortedRelationshipMapRelationships(values map[string]relationshipMapRelationshipRecord) []relationshipMapRelationshipRecord {
	rows := make([]relationshipMapRelationshipRecord, 0, len(values))
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

func sortedRelationshipMapEntityIDs(values map[string]relationshipMapEntityRecord, levels map[string]int) []string {
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

func (row relationshipMapEntityRecord) securityRecord() map[string]any {
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

func (row relationshipMapRelationshipRecord) securityRecord() map[string]any {
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
