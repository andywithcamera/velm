package main

import (
	"net/http"
	"strings"

	"velm/internal/db"
	"velm/internal/utils"
)

// /map retired (spec: Form-Embedded Relationship Map, phase 3). Old links
// redirect to the source record's form, where the Related tab now lives.
// Accepted shapes: /map?entity=<uuid>&depth=N and /map (falls back to the
// entity list).
func handleMapRedirect(w http.ResponseWriter, r *http.Request) {
	entityID := strings.TrimSpace(r.URL.Query().Get("entity"))
	if entityID != "" && utils.IsValidUUID(entityID) && entityExists(r, entityID) {
		http.Redirect(w, r, "/f/base_entity/"+entityID, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/t/base_entity", http.StatusFound)
}

func entityExists(r *http.Request, entityID string) bool {
	var one int
	err := db.Pool.QueryRow(r.Context(),
		`SELECT 1 FROM base_entity WHERE _id = $1::uuid AND _deleted_at IS NULL`, entityID).Scan(&one)
	return err == nil
}
