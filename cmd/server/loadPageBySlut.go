package main

import (
	"context"
	"database/sql"
	"fmt"
	"velm/internal/db"
)

// loadPageBySlug fetches page contents by page slug.
func loadPageBySlug(name string) (pageData, error) {
	row := db.Pool.QueryRow(context.Background(), "SELECT name, slug, content, COALESCE(editor_mode, 'wysiwyg'), COALESCE(status, 'draft') FROM _page WHERE slug = $1", name)

	var pageName, slug, content, editorMode, status string
	err := row.Scan(&pageName, &slug, &content, &editorMode, &status)
	if err != nil {
		if err == sql.ErrNoRows {
			return pageData{}, fmt.Errorf("page not found")
		}
		return pageData{}, fmt.Errorf("error fetching page: %v", err)
	}

	return pageData{
		Name:       pageName,
		Slug:       slug,
		Content:    content,
		EditorMode: editorMode,
		Status:     status,
	}, nil
}
