package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"velm/internal/db"
)

func TestNormalizeDocsRoleInputDeduplicatesAndSorts(t *testing.T) {
	got := normalizeDocsRoleInput("editor, writer\nadmin ; editor")

	want := []string{"admin", "editor", "writer"}
	if len(got) != len(want) {
		t.Fatalf("len(normalizeDocsRoleInput()) = %d, want %d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalizeDocsRoleInput()[%d] = %q, want %q (%#v)", i, got[i], want[i], got)
		}
	}
}

func TestDocsAccessContextArticleUsesLibraryReadRolesWhenArticleRolesBlank(t *testing.T) {
	access := &docsAccessContext{
		userID:        "user-1",
		apps:          map[string]db.RegisteredApp{},
		roleSets:      map[string]map[string]bool{},
		globalRoleSet: map[string]bool{"reader": true},
	}
	library := docsLibrary{
		Slug:       "ops",
		Visibility: "private",
		ReadRoles:  []string{"reader"},
	}
	article := docsArticle{}

	if !access.canReadArticle(library, article) {
		t.Fatal("expected library read roles to grant article access")
	}
}

func TestDocsAccessContextArticleReadRolesOverrideLibraryRoles(t *testing.T) {
	access := &docsAccessContext{
		userID:        "user-1",
		apps:          map[string]db.RegisteredApp{},
		roleSets:      map[string]map[string]bool{},
		globalRoleSet: map[string]bool{"reader": true},
	}
	library := docsLibrary{
		Slug:       "ops",
		Visibility: "app",
		ReadRoles:  []string{"reader"},
	}
	article := docsArticle{
		ReadRoles: []string{"manager"},
	}

	if access.canReadArticle(library, article) {
		t.Fatal("expected article read roles to override library roles")
	}
}

func TestHandleLegacyDocsRedirectPreservesQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/knowledge?library=ops&article=runbook", nil)
	recorder := httptest.NewRecorder()

	handleLegacyDocsRedirect(recorder, req)

	response := recorder.Result()
	if response.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusMovedPermanently)
	}
	if got := response.Header.Get("Location"); got != "/docs?library=ops&article=runbook" {
		t.Fatalf("location = %q, want %q", got, "/docs?library=ops&article=runbook")
	}
}

func TestParseDocsArticleID(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{name: "valid", path: "/d/123e4567-e89b-12d3-a456-426614174000", want: "123e4567-e89b-12d3-a456-426614174000"},
		{name: "valid trailing slash", path: "/d/123e4567-e89b-12d3-a456-426614174000/", want: "123e4567-e89b-12d3-a456-426614174000"},
		{name: "invalid numeric id", path: "/d/42", wantErr: true},
		{name: "invalid nested path", path: "/d/123e4567-e89b-12d3-a456-426614174000/edit", wantErr: true},
		{name: "invalid missing id", path: "/d/", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDocsArticleID(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseDocsArticleID(%q) error = nil, want error", tc.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDocsArticleID(%q) error = %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("parseDocsArticleID(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestMarkdownLinkMatchesDocsArticle(t *testing.T) {
	article := docsArticle{
		ID:          "123e4567-e89b-12d3-a456-426614174000",
		LibrarySlug: "ops",
		Slug:        "runbook",
	}

	tests := []struct {
		name string
		href string
		want bool
	}{
		{name: "canonical reader link", href: "/d/123e4567-e89b-12d3-a456-426614174000", want: true},
		{name: "absolute canonical reader link", href: "https://velm.dev/d/123e4567-e89b-12d3-a456-426614174000#part-1", want: true},
		{name: "legacy docs query link", href: "/docs?library=ops&article=runbook", want: true},
		{name: "different article", href: "/d/123e4567-e89b-12d3-a456-426614174001", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := markdownLinkMatchesDocsArticle(tc.href, article); got != tc.want {
				t.Fatalf("markdownLinkMatchesDocsArticle(%q) = %t, want %t", tc.href, got, tc.want)
			}
		})
	}
}
