package main

import (
	"strings"
	"testing"
)

func TestParseSearchShortcut(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantQuery  string
		wantFilter string
	}{
		{name: "plain query", input: "Users", wantQuery: "Users", wantFilter: ""},
		{name: "table shortcut", input: "Users?Name=Andy Doyle", wantQuery: "Users", wantFilter: "Name=Andy Doyle"},
		{name: "empty filter", input: "Users?", wantQuery: "Users", wantFilter: ""},
		{name: "missing base falls back to raw", input: "?Name=Andy Doyle", wantQuery: "?Name=Andy Doyle", wantFilter: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotQuery, gotFilter := parseSearchShortcut(tt.input)
			if gotQuery != tt.wantQuery || gotFilter != tt.wantFilter {
				t.Fatalf("parseSearchShortcut(%q) = (%q, %q), want (%q, %q)", tt.input, gotQuery, gotFilter, tt.wantQuery, tt.wantFilter)
			}
		})
	}
}

func TestWithSearchShortcut(t *testing.T) {
	tests := []struct {
		name     string
		item     MenuItem
		filter   string
		wantHref string
	}{
		{
			name:     "adds q to table link",
			item:     MenuItem{Title: "Users", Href: "/t/_user"},
			filter:   "Name=Andy Doyle",
			wantHref: "/t/_user?q=Name%3DAndy+Doyle",
		},
		{
			name:     "preserves existing params",
			item:     MenuItem{Title: "Users", Href: "/t/_user?sort=name"},
			filter:   "Name=Andy Doyle",
			wantHref: "/t/_user?q=Name%3DAndy+Doyle&sort=name",
		},
		{
			name:     "ignores non table links",
			item:     MenuItem{Title: "Users", Href: "/admin/access"},
			filter:   "Name=Andy Doyle",
			wantHref: "/admin/access",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := withSearchShortcut(tt.item, tt.filter)
			if got.Href != tt.wantHref {
				t.Fatalf("withSearchShortcut(%q, %q) href = %q, want %q", tt.item.Href, tt.filter, got.Href, tt.wantHref)
			}
		})
	}
}

func TestBuildTaskSearchTitle(t *testing.T) {
	tests := []struct {
		name   string
		number string
		title  string
		want   string
	}{
		{name: "number and title", number: "STOR-000123", title: "Bridge runtime actions", want: "STOR-000123 - Bridge runtime actions"},
		{name: "title only", title: "Bridge runtime actions", want: "Bridge runtime actions"},
		{name: "number only", number: "STOR-000123", want: "STOR-000123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildTaskSearchTitle(tt.number, tt.title); got != tt.want {
				t.Fatalf("buildTaskSearchTitle(%q, %q) = %q, want %q", tt.number, tt.title, got, tt.want)
			}
		})
	}
}

func TestBuildTaskSearchQuery(t *testing.T) {
	query, err := buildTaskSearchQuery([]string{"base_task", "dw_story"})
	if err != nil {
		t.Fatalf("buildTaskSearchQuery() error = %v", err)
	}
	if !strings.Contains(query, `FROM "base_task"`) {
		t.Fatalf("expected base task search source in query, got %q", query)
	}
	if !strings.Contains(query, `FROM "dw_story"`) {
		t.Fatalf("expected derived task search source in query, got %q", query)
	}
	if !strings.Contains(query, `LOWER(COALESCE(title, '')) LIKE '%' || $1 || '%'`) {
		t.Fatalf("expected title predicate in query, got %q", query)
	}
	if !strings.Contains(query, `UNION ALL`) {
		t.Fatalf("expected union query for task sources, got %q", query)
	}
}
