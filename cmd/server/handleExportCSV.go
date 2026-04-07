package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"velm/internal/db"
	"velm/internal/utils"
)

func handleExportCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(r.URL.Path, "/api/export/")))
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if !requireAdminTableAccess(w, r, tableName) {
		return
	}
	if db.GetTable(tableName).ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	quotedTable, err := db.QuoteIdentifier(tableName)
	if err != nil {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}

	schemaRows, err := db.Pool.Query(context.Background(), "SELECT * FROM "+quotedTable+" LIMIT 0")
	if err != nil {
		http.Error(w, "Failed to read table schema", http.StatusInternalServerError)
		return
	}
	defer schemaRows.Close()

	fieldDescriptions := schemaRows.FieldDescriptions()
	cols := make([]string, len(fieldDescriptions))
	quotedCols := make([]string, len(fieldDescriptions))
	colSet := make(map[string]bool, len(fieldDescriptions))
	for i, fd := range fieldDescriptions {
		name := string(fd.Name)
		cols[i] = name
		colSet[name] = true
		quotedCol, err := db.QuoteIdentifier(name)
		if err != nil {
			http.Error(w, "Invalid column schema", http.StatusInternalServerError)
			return
		}
		quotedCols[i] = quotedCol
	}

	queryVals := r.URL.Query()
	serverFilter := strings.TrimSpace(queryVals.Get("q"))
	showDeleted := strings.TrimSpace(queryVals.Get("deleted")) == "1"
	maxRows := 50000
	if rawLimit := strings.TrimSpace(queryVals.Get("limit")); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil && parsed > 0 && parsed <= 200000 {
			maxRows = parsed
		}
	}

	whereClause := ""
	args := make([]any, 0, len(cols)+1)
	clauses := make([]string, 0, 2)
	if colSet["_deleted_at"] && !showDeleted {
		clauses = append(clauses, "_deleted_at IS NULL")
	}
	if serverFilter != "" {
		placeholder := len(args) + 1
		parts := make([]string, 0, len(quotedCols))
		for _, col := range quotedCols {
			parts = append(parts, fmt.Sprintf("CAST(%s AS TEXT) ILIKE $%d", col, placeholder))
		}
		clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		args = append(args, "%"+serverFilter+"%")
	}
	if len(clauses) > 0 {
		whereClause = " WHERE " + strings.Join(clauses, " AND ")
	}

	query := "SELECT * FROM " + quotedTable + whereClause + fmt.Sprintf(" LIMIT %d", maxRows)
	rows, err := db.Pool.Query(context.Background(), query, args...)
	if err != nil {
		http.Error(w, "Failed to query table", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	filename := fmt.Sprintf("%s_%s.csv", tableName, time.Now().UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	writer := csv.NewWriter(w)
	defer writer.Flush()

	if err := writer.Write(cols); err != nil {
		http.Error(w, "Failed to write csv", http.StatusInternalServerError)
		return
	}

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			http.Error(w, "Failed to scan row", http.StatusInternalServerError)
			return
		}
		record := make([]string, len(cols))
		for i := range values {
			record[i] = fmt.Sprint(utils.NormalizeValue(values[i]))
		}
		if err := writer.Write(record); err != nil {
			http.Error(w, "Failed to write csv", http.StatusInternalServerError)
			return
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "Failed to stream rows", http.StatusInternalServerError)
		return
	}
}
