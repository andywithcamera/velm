package main

// Task catalog (2026-11-09): one page where every task definition can be
// found, searched, and started. The catalog is the front door for spawning
// work; the board is where spawned work is tracked.

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

type taskCatalogEntry struct {
	Slug        string
	DisplayName string
	Description string
	Category    string
	IsContainer bool
}

type taskCatalogCategory struct {
	Name    string
	Entries []taskCatalogEntry
}

func loadTaskCatalog(ctx context.Context, query string) ([]taskCatalogCategory, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT slug, display_name, description, category, is_container
		 FROM base_task_definition
		 WHERE _deleted_at IS NULL
		 ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	needle := strings.ToLower(strings.TrimSpace(query))
	byCategory := map[string][]taskCatalogEntry{}
	categories := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var e taskCatalogEntry
		var category string
		if err := rows.Scan(&e.Slug, &e.DisplayName, &e.Description, &category, &e.IsContainer); err != nil {
			return nil, err
		}
		if category == "" {
			category = "general"
		}
		if needle != "" &&
			!strings.Contains(strings.ToLower(e.DisplayName), needle) &&
			!strings.Contains(strings.ToLower(e.Description), needle) &&
			!strings.Contains(strings.ToLower(e.Slug), needle) {
			continue
		}
		if !seen[category] {
			seen[category] = true
			categories = append(categories, category)
		}
		byCategory[category] = append(byCategory[category], e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(categories)

	out := make([]taskCatalogCategory, 0, len(categories))
	for _, name := range categories {
		out = append(out, taskCatalogCategory{Name: name, Entries: byCategory[name]})
	}
	return out, nil
}

func handleTaskCatalogPage(w http.ResponseWriter, r *http.Request) {
	if db.Pool == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}
	viewData := newViewData(w, r, "/task-catalog", "Task Catalog", "Operations")
	catalog, err := loadTaskCatalog(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, "Failed to load task catalog", http.StatusInternalServerError)
		return
	}
	total := 0
	for _, c := range catalog {
		total += len(c.Entries)
	}
	viewData["View"] = "task-catalog"
	viewData["TaskCatalogCategories"] = catalog
	viewData["TaskCatalogQuery"] = strings.TrimSpace(r.URL.Query().Get("q"))
	viewData["TaskCatalogTotal"] = total
	viewData["TaskCatalogOrderURL"] = "/task-catalog/order"
	if r.Header.Get("HX-Request") == "true" {
		// HTMX search swaps only the results region, not the whole page.
		if err := templates.ExecuteTemplate(w, "task-catalog-results", viewData); err != nil {
			http.Error(w, "Failed to render task catalog", http.StatusInternalServerError)
		}
		return
	}
	if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
		http.Error(w, "Failed to render task catalog", http.StatusInternalServerError)
	}
}

// handleTaskCatalogOrder spawns a definition instance from the catalog's
// "Start" form. Same engine path as POST /api/task-instances; the catalog
// just wraps it in a form so the experience needs no client scripting.
func handleTaskCatalogOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(r.PostFormValue("definition_slug"))
	if slug == "" {
		http.Error(w, "definition_slug is required", http.StatusBadRequest)
		return
	}
	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthenticated", http.StatusUnauthorized)
		return
	}
	rootID, err := runtimeSpawnTaskBySlug(r.Context(), db.Pool, slug, "", userID, nil)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<div class="ui-catalog-order-error">Could not start ` + htmlEscapeText(slug) + `: ` + htmlEscapeText(err.Error()) + `</div>`))
		return
	}
	number := ""
	if root, _, derr := runtimeDescribeTaskInstace(r.Context(), db.Pool, rootID); derr == nil {
		number = root.Number
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<div class="ui-catalog-order-done">Started as <a href="/f/base_task/` + htmlEscapeText(rootID) + `">` +
		htmlEscapeText(number) + `</a>. Track it on the <a href="/task">board</a>.</div>`))
}

func htmlEscapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;")
	return r.Replace(s)
}
