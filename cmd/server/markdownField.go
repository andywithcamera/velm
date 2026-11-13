package main

import (
	"strings"

	"velm/internal/db"
)

// markdownFieldView is the data the "markdown-field" template renders, and the
// contract with web/static/js/markdown-field.js. Every markdown surface builds
// one of these rather than hand-writing the markup, so a description field and
// a docs article get identical behaviour by construction.
type markdownFieldView struct {
	ID           string
	Name         string
	Value        string
	Placeholder  string
	Readonly     bool
	Required     bool
	Hidden       bool
	AutoExpand   bool
	Size         string // "" for a form field, "editor" for a document body
	DefaultMode  string // "" auto, "write" or "preview"
	FieldName    string // data-field-name, read by form validation
	BaseRequired bool   // data-base-required, form validation baseline
}

// buildFormMarkdownFields builds one view per markdown column on a record form.
// Keyed by column name because the form template ranges over its columns and
// looks the field up as it goes.
func buildFormMarkdownFields(columns []db.Column, configs map[string]formFieldConfig, values map[string]string, formReadonly bool) map[string]markdownFieldView {
	out := make(map[string]markdownFieldView)
	for _, col := range columns {
		cfg, ok := configs[col.NAME]
		if !ok || cfg.Kind != "markdown" {
			continue
		}
		readonly := col.IS_READONLY || cfg.ReadOnly || formReadonly
		required := !col.IS_NULLABLE && !readonly && !col.IS_HIDDEN
		out[col.NAME] = markdownFieldView{
			ID:           col.NAME,
			Name:         col.NAME,
			Value:        values[col.NAME],
			Placeholder:  "Write markdown…",
			Readonly:     readonly,
			Required:     required,
			Hidden:       col.IS_HIDDEN,
			AutoExpand:   true,
			FieldName:    col.NAME,
			BaseRequired: required,
		}
	}
	return out
}

// docsArticleMarkdownField is the article body as the shared field renders it.
// A docs page is an editor — the reader lives at /docs/<slug> — so the body
// opens in source mode rather than as prose.
func docsArticleMarkdownField(body string) markdownFieldView {
	return markdownFieldView{
		ID:          "docs-markdown-source",
		Name:        "markdown_body",
		Value:       body,
		Placeholder: "Start writing… (type [[ to link any record)",
		AutoExpand:  true,
		Size:        "editor",
		DefaultMode: "write",
	}
}

// InitialMode is what the field paints in before any JavaScript runs, so prose
// does not flash its source box while the page is still loading. The script
// takes over from the same rule.
func (v markdownFieldView) InitialMode() string {
	if v.Readonly {
		return "preview"
	}
	switch v.DefaultMode {
	case "write", "preview":
		return v.DefaultMode
	}
	if strings.TrimSpace(v.Value) != "" {
		return "preview"
	}
	return "write"
}

// StatusText is the one-line hint under the field bar.
func (v markdownFieldView) StatusText() string {
	if v.Readonly {
		return "Markdown. Read only."
	}
	return "Markdown. Click to edit."
}
