package main

import (
	"strings"
	"testing"
)

func TestSanitizeURLForHTML(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantHref     string
		wantExternal bool
	}{
		{name: "internal path", raw: "/d/123e4567-e89b-12d3-a456-426614174000", wantHref: "/d/123e4567-e89b-12d3-a456-426614174000", wantExternal: false},
		{name: "relative internal path", raw: "docs?library=ops&article=runbook", wantHref: "docs?library=ops&amp;article=runbook", wantExternal: false},
		{name: "anchor", raw: "#part-1", wantHref: "#part-1", wantExternal: false},
		{name: "external link", raw: "https://example.com/docs", wantHref: "https://example.com/docs", wantExternal: true},
		{name: "unsupported scheme", raw: "javascript:alert(1)", wantHref: "#", wantExternal: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotHref, gotExternal := sanitizeURLForHTML(tc.raw)
			if gotHref != tc.wantHref || gotExternal != tc.wantExternal {
				t.Fatalf("sanitizeURLForHTML(%q) = (%q, %t), want (%q, %t)", tc.raw, gotHref, gotExternal, tc.wantHref, tc.wantExternal)
			}
		})
	}
}

func TestRenderMarkdownToSafeHTMLUsesInternalAndExternalLinkPolicies(t *testing.T) {
	rendered := string(renderMarkdownToSafeHTML("[Doc](/d/123e4567-e89b-12d3-a456-426614174000)\n[Site](https://example.com/docs)"))

	if strings.Contains(rendered, `href="/d/123e4567-e89b-12d3-a456-426614174000" target="_blank"`) {
		t.Fatalf("internal doc link unexpectedly opens in a new tab: %s", rendered)
	}
	if !strings.Contains(rendered, `href="https://example.com/docs" target="_blank" rel="noopener noreferrer"`) {
		t.Fatalf("external link missing new-tab attributes: %s", rendered)
	}
}

func TestMarkdownLinksToDocsArticleMatchesWikilinks(t *testing.T) {
	article := docsArticle{Title: "Deployment Guide", Number: "DOC-007"}

	tests := []struct {
		name     string
		markdown string
		want     bool
	}{
		{"wikilink by title matches", "See [[Deployment Guide]] for steps.", true},
		{"wikilink by number matches", "Read [[DOC-007]] first.", true},
		{"wikilink case-insensitive", "Check [[deployment guide]].", true},
		{"wikilink with display text matches", "See [[Deployment Guide|the guide]].", true},
		{"unrelated wikilink no match", "See [[Other Article]].", false},
		{"regular markdown link to /d/ path matches", "[Guide](/d/abc123)", false}, // no UUID, just path check
		{"empty markdown no match", "", false},
	}

	// Reuse the resolved href logic — for the URL-based cases we need a real article ID.
	// The wikilink cases don't need a real ID so we can test them directly.
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := markdownLinksToDocsArticle(tc.markdown, article)
			if got != tc.want {
				t.Errorf("markdownLinksToDocsArticle(%q) = %v, want %v", tc.markdown, got, tc.want)
			}
		})
	}
}

func TestRenderMarkdownWikilinks(t *testing.T) {
	resolver := func(title string) string {
		if title == "Target Article" {
			return "/d/abc123"
		}
		return ""
	}

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "resolved wikilink becomes link",
			input:    "See [[Target Article]] for details.",
			contains: `<a href="/d/abc123" class="docs-wikilink">Target Article</a>`,
		},
		{
			name:     "unresolved wikilink becomes styled span",
			input:    "See [[Unknown Record]] here.",
			contains: `class="docs-wikilink docs-wikilink-unresolved"`,
		},
		{
			name:     "wikilink with display text uses display text",
			input:    "See [[Target Article|click here]] for details.",
			contains: `>click here</a>`,
		},
		{
			name:     "no resolver renders unresolved span",
			input:    "[[Some Article]]",
			contains: `docs-wikilink-unresolved`,
		},
		{
			name:     "wikilink does not interfere with regular links",
			input:    "[Link](https://example.com) and [[Target Article]]",
			contains: `href="https://example.com"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var result string
			if tc.name == "no resolver renders unresolved span" {
				result = string(renderMarkdownToSafeHTML(tc.input))
			} else {
				result = string(renderMarkdownToSafeHTMLResolved(tc.input, resolver))
			}
			if !strings.Contains(result, tc.contains) {
				t.Errorf("expected output to contain %q\ngot: %s", tc.contains, result)
			}
		})
	}
}
