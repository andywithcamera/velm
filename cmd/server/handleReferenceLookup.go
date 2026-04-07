package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"velm/internal/db"
)

func handleReferenceLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tableName := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table")))
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if db.GetTableContext(r.Context(), tableName).ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	selected := strings.TrimSpace(r.URL.Query().Get("selected"))
	limit := 30
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil {
			limit = parsed
		}
	}

	options := fetchReferenceOptionsFiltered(r.Context(), tableName, query, limit, selected)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"options": options,
	})
}
