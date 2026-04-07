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
