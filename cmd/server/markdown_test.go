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

func TestRenderMarkdownLinkifiesBareURLs(t *testing.T) {
	// Task briefs are issue text: the URL arrives bare, and CommonMark leaves
	// it as text. It is the most useful thing in the brief, so it must be
	// clickable.
	rendered := string(renderMarkdownToSafeHTML("GitHub: #216 https://github.com/andywithcamera/velm/issues/216"))
	want := `<a href="https://github.com/andywithcamera/velm/issues/216" target="_blank" rel="noopener noreferrer">https://github.com/andywithcamera/velm/issues/216</a>`
	if !strings.Contains(rendered, want) {
		t.Fatalf("bare URL not linkified: %s", rendered)
	}

	// An explicit markdown link is not linkified a second time.
	rendered = string(renderMarkdownToSafeHTML("[site](https://example.com/docs)"))
	if strings.Count(rendered, "<a ") != 1 {
		t.Fatalf("explicit link double-linked: %s", rendered)
	}

	// Trailing punctuation belongs to the sentence, not the URL.
	rendered = string(renderMarkdownToSafeHTML("see https://example.com/docs, then stop"))
	if !strings.Contains(rendered, `href="https://example.com/docs"`) {
		t.Fatalf("trailing comma kept inside the href: %s", rendered)
	}

	// A closing parenthesis is dropped only when it is unbalanced.
	rendered = string(renderMarkdownToSafeHTML("(see https://example.com/docs)"))
	if !strings.Contains(rendered, `href="https://example.com/docs"`) {
		t.Fatalf("sentence parenthesis kept inside the href: %s", rendered)
	}

	// Nothing but http(s) is ever auto-linked: the pattern is the allowlist.
	rendered = string(renderMarkdownToSafeHTML("javascript:alert(1) and file:///etc/passwd"))
	if strings.Contains(rendered, "<a ") {
		t.Fatalf("non-http scheme auto-linked: %s", rendered)
	}
}

func TestRenderMarkdownOrderedListsAndQuotes(t *testing.T) {
	rendered := string(renderMarkdownToSafeHTML("1. Create a member\n2. Grant the group\n3. Observe the denial"))
	if !strings.Contains(rendered, "<ol><li>Create a member</li><li>Grant the group</li><li>Observe the denial</li></ol>") {
		t.Fatalf("ordered list not rendered as <ol>: %s", rendered)
	}

	// A bullet list followed by a numbered list must not merge: the numbering
	// the author typed is the instructions' order.
	rendered = string(renderMarkdownToSafeHTML("- a\n- b\n\n1. first\n2. second"))
	if !strings.Contains(rendered, "<ul><li>a</li><li>b</li></ul>") || !strings.Contains(rendered, "<ol><li>first</li><li>second</li></ol>") {
		t.Fatalf("list types merged: %s", rendered)
	}

	rendered = string(renderMarkdownToSafeHTML("> The scope wins over the role."))
	if !strings.Contains(rendered, "<blockquote>The scope wins over the role.</blockquote>") {
		t.Fatalf("blockquote not rendered: %s", rendered)
	}
}
