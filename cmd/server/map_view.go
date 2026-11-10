package main

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"velm/internal/relmap"

	"velm/internal/auth"
	"velm/internal/db"
)

// Aliases to the shared graph loader in internal/relmap.
type (
	relationshipMapNode  = relmap.Node
	relationshipMapGraph = relmap.Graph
)

const (
	relationshipMapDefaultDepth = relmap.DefaultDepth
	relationshipMapMinDepth     = relmap.MinDepth
	relationshipMapMaxDepth     = relmap.MaxDepth
)

var (
	errRelationshipMapEntityNotFound  = relmap.ErrEntityNotFound
	errRelationshipMapEntityForbidden = relmap.ErrEntityForbidden
)

func loadRelationshipMapGraph(ctx context.Context, userID, rootID string, maxDepth int) (relationshipMapGraph, relationshipMapNode, error) {
	return relmap.LoadGraph(ctx, userID, rootID, maxDepth)
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
	// encoding/json escapes '<', '>' and '&' by default, so a record name
	// containing "</script>" is emitted as \u003c/script\u003e and cannot close
	// the surrounding script element.
	viewData["MapGraphJSON"] = template.JS(string(graphJSON)) // #nosec G203 -- json.Marshal escapes HTML by default
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
