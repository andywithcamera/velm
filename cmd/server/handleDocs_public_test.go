package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"velm/internal/db"
)

// The public flag (#372) has one job: a visitor with no session can read an
// article that is flagged public and published, and nothing else. These tests
// pin both halves — the grant and every way it must not leak.

func TestGuestDocsAccessContextIsNotAuthenticated(t *testing.T) {
	access := guestDocsAccessContext()
	if !access.isGuest() {
		t.Fatal("guestDocsAccessContext did not produce a guest context")
	}
	if access.authenticated {
		t.Error("guest context reports authenticated")
	}
}

func TestGuestDocsAccessReadsOnlyPublicPublishedArticles(t *testing.T) {
	access := guestDocsAccessContext()
	library := docsLibrary{Slug: "ops", Name: "Ops", Status: "active", Visibility: "private"}

	cases := []struct {
		name    string
		article docsArticle
		want    bool
	}{
		{"public and published", docsArticle{Status: "published", IsPublic: true}, true},
		{"public but draft", docsArticle{Status: "draft", IsPublic: true}, false},
		{"public but archived", docsArticle{Status: "archived", IsPublic: true}, false},
		{"published but not public", docsArticle{Status: "published", IsPublic: false}, false},
		{"neither", docsArticle{Status: "draft", IsPublic: false}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := access.canReadArticle(library, tc.article); got != tc.want {
				t.Fatalf("guest canReadArticle = %v, want %v", got, tc.want)
			}
		})
	}
}

// A library visibility of "public" must not be a second, hidden route to guest
// read access: the article flag is the gate.
func TestGuestDocsAccessIgnoresPublicLibraryVisibility(t *testing.T) {
	access := guestDocsAccessContext()
	library := docsLibrary{Slug: "handbook", Status: "active", Visibility: "public"}

	if access.canReadLibrary(library) {
		t.Error("guest can read a library directly; only flagged articles should be reachable")
	}
	if access.canReadArticle(library, docsArticle{Status: "published"}) {
		t.Error("guest read a non-public article through a public library")
	}
}

// The guest context has no roles, and canCreateInLibrary grants when no create
// roles are configured. Without an explicit guard a guest would hold write
// permission on every library. This is the regression test for that.
func TestGuestDocsAccessGrantsNoWrites(t *testing.T) {
	access := guestDocsAccessContext()
	library := docsLibrary{Slug: "ops", Status: "active", Visibility: "public"}
	article := docsArticle{Status: "published", IsPublic: true}

	if access.canCreateInLibrary(library) {
		t.Error("guest can create in a library")
	}
	if access.canEditLibrary(library) {
		t.Error("guest can edit a library")
	}
	if access.canDeleteLibrary(library) {
		t.Error("guest can delete a library")
	}
	if access.canEditArticle(library, article) {
		t.Error("guest can edit an article")
	}
	if access.canDeleteArticle(library, article) {
		t.Error("guest can delete an article")
	}
}

// An authenticated user with no roles is not a guest: they keep the library
// visibility and ownership fallbacks the docs system has always given them.
func TestEmptyRoleUserIsNotTreatedAsGuest(t *testing.T) {
	access := &docsAccessContext{
		userID:        "user-1",
		authenticated: true,
		apps:          map[string]db.RegisteredApp{},
		roleSets:      map[string]map[string]bool{},
		globalRoleSet: map[string]bool{},
	}
	if access.isGuest() {
		t.Fatal("a signed-in user with no roles was classified as a guest")
	}
	library := docsLibrary{Slug: "ops", Status: "active", Visibility: "public"}
	if !access.canReadArticle(library, docsArticle{Status: "published"}) {
		t.Error("signed-in user lost read access to a public-visibility library")
	}
}

func TestDocsFormBool(t *testing.T) {
	cases := map[string]bool{
		"":       false,
		"0":      false,
		"1":      true,
		"on":     true,
		"true":   true,
		"TRUE":   true,
		"yes":    true,
		"public": true,
		"nope":   false,
	}
	for raw, want := range cases {
		form := "is_public=" + raw
		req := httptest.NewRequest(http.MethodPost, "/api/docs/article/save", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := req.ParseForm(); err != nil {
			t.Fatalf("ParseForm(%q): %v", raw, err)
		}
		if got := docsFormBool(req, "is_public"); got != want {
			t.Errorf("docsFormBool(%q) = %v, want %v", raw, got, want)
		}
	}

	// An unchecked checkbox sends no field at all.
	req := httptest.NewRequest(http.MethodPost, "/api/docs/article/save", strings.NewReader("title=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	if docsFormBool(req, "is_public") {
		t.Error("absent checkbox read as true")
	}
}
