package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"velm/internal/auth"
	"velm/internal/db"
)

const taskSearchResultLimit = 8
const docsSearchResultLimit = 8

func handleSearch(w http.ResponseWriter, r *http.Request) {
	rawQuery := strings.TrimSpace(r.URL.Query().Get("q"))
	queryTerm, tableFilter := parseSearchShortcut(rawQuery)
	query := strings.ToLower(queryTerm)

	var results []MenuItem

	// Search menuItems
	for _, item := range menuItems {
		if strings.Contains(strings.ToLower(item.Title), query) {
			results = append(results, withSearchShortcut(item, tableFilter))
		}
	}

	if strings.EqualFold(auth.UserRoleFromRequest(r), "admin") {
		for _, link := range adminLinks {
			if strings.Contains(strings.ToLower(link.Title), query) {
				results = append(results, withSearchShortcut(link, tableFilter))
			}
		}
	}

	taskResults, err := searchTaskResults(r.Context(), query, taskSearchResultLimit)
	if err != nil {
		http.Error(w, "Database search failed", http.StatusInternalServerError)
		log.Println("Task search error:", err)
		return
	}
	results = append(results, taskResults...)

	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID != "" {
		docResults, err := searchDocsResults(r.Context(), userID, query, docsSearchResultLimit)
		if err != nil {
			http.Error(w, "Database search failed", http.StatusInternalServerError)
			log.Println("Docs search error:", err)
			return
		}
		results = append(results, docResults...)
	}

	// 🔥 Search the DB (example: _page table with title & slug)
	rows, err := db.Pool.Query(r.Context(),
		`SELECT name, _id FROM _user WHERE LOWER(name) LIKE '%' || $1 || '%'`,
		query,
	)
	if err != nil {
		http.Error(w, "Database search failed", http.StatusInternalServerError)
		log.Println("DB search error:", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var name, _id string
		if err := rows.Scan(&name, &_id); err != nil {
			log.Println("Row scan error:", err)
			continue
		}
		results = append(results, MenuItem{Title: name, Href: "/f/_user/" + _id})
	}

	data := map[string]any{
		"Results": results,
	}

	err = templates.ExecuteTemplate(w, "search-results.html", data)
	if err != nil {
		http.Error(w, "Error rendering search results", http.StatusInternalServerError)
	}
}

func searchTaskResults(ctx context.Context, query string, limit int) ([]MenuItem, error) {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" || limit <= 0 || db.Pool == nil {
		return nil, nil
	}

	taskTables := db.ListTableQuerySources(ctx, "base_task")
	sqlQuery, err := buildTaskSearchQuery(taskTables)
	if err != nil || sqlQuery == "" {
		return nil, err
	}

	rows, err := db.Pool.Query(ctx, sqlQuery, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]MenuItem, 0, limit)
	for rows.Next() {
		var tableName, id, number, title string
		if err := rows.Scan(&tableName, &id, &number, &title); err != nil {
			return nil, err
		}
		results = append(results, MenuItem{
			Title: buildTaskSearchTitle(number, title),
			Href:  "/f/" + tableName + "/" + id,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func searchDocsResults(ctx context.Context, userID, query string, limit int) ([]MenuItem, error) {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" || limit <= 0 {
		return nil, nil
	}

	docsCtx, err := loadDocsViewContext(ctx, userID, false)
	if err != nil {
		return nil, err
	}

	articles, err := listDocsArticles(ctx, "", query, true)
	if err != nil {
		return nil, err
	}
	articles = decorateDocsArticles(filterReadableDocsArticles(docsCtx.Access, articles, docsCtx.LibraryBySlug))
	if len(articles) > limit {
		articles = articles[:limit]
	}

	results := make([]MenuItem, 0, len(articles))
	for _, article := range articles {
		results = append(results, MenuItem{
			Title: buildDocSearchTitle(article.Number, article.Title),
			Href:  article.ReaderHref,
		})
	}
	return results, nil
}

func buildDocSearchTitle(number, title string) string {
	number = strings.TrimSpace(number)
	title = strings.TrimSpace(title)
	switch {
	case number != "" && title != "":
		return number + " - " + title
	case title != "":
		return title
	default:
		return number
	}
}

func buildTaskSearchQuery(tableNames []string) (string, error) {
	selects := make([]string, 0, len(tableNames))
	for _, tableName := range tableNames {
		quotedTable, err := db.QuoteIdentifier(tableName)
		if err != nil {
			return "", err
		}
		selects = append(selects, fmt.Sprintf(
			`SELECT '%s' AS table_name,
			        _id,
			        COALESCE(number, '') AS number,
			        COALESCE(title, '') AS title,
			        _updated_at,
			        _created_at
			   FROM %s
			  WHERE _deleted_at IS NULL
			    AND (
			          LOWER(COALESCE(title, '')) LIKE '%%' || $1 || '%%'
			       OR LOWER(COALESCE(number, '')) LIKE '%%' || $1 || '%%'
			    )`,
			tableName,
			quotedTable,
		))
	}
	if len(selects) == 0 {
		return "", nil
	}

	return fmt.Sprintf(
		`SELECT table_name, _id, number, title
		   FROM (
		         %s
		        ) task_search_results
		  ORDER BY _updated_at DESC NULLS LAST, _created_at DESC NULLS LAST, number ASC
		  LIMIT $2`,
		strings.Join(selects, "\nUNION ALL\n"),
	), nil
}

func buildTaskSearchTitle(number, title string) string {
	number = strings.TrimSpace(number)
	title = strings.TrimSpace(title)
	switch {
	case number != "" && title != "":
		return number + " - " + title
	case title != "":
		return title
	default:
		return number
	}
}

func parseSearchShortcut(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}

	base, filter, found := strings.Cut(raw, "?")
	if !found {
		return raw, ""
	}

	base = strings.TrimSpace(base)
	filter = strings.TrimSpace(filter)
	if base == "" {
		return raw, ""
	}
	return base, filter
}

func withSearchShortcut(item MenuItem, tableFilter string) MenuItem {
	tableFilter = strings.TrimSpace(tableFilter)
	if tableFilter == "" || !strings.HasPrefix(item.Href, "/t/") {
		return item
	}

	parsed, err := url.Parse(item.Href)
	if err != nil {
		return item
	}

	values := parsed.Query()
	values.Set("q", tableFilter)
	parsed.RawQuery = values.Encode()

	item.Href = parsed.String()
	return item
}
