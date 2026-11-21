package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"velm/internal/db"
)

// The unauthenticated landing target (#372) is what lets a published page act
// as the site homepage for a visitor who has never signed in.

func TestNormalizeUnauthenticatedRouteTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  string
	}{
		{"login", "login"},
		{"/login", "login"},
		{"/LOGIN/", "login"},
		{"Page:Home", "page:home"},
		{"/p/home", "/p/home"},
		{"table:_property", "table:_property"},
		{"/t/_property", "/t/_property"},
		{"", ""},
		{"home", ""},
		{"/home", ""}, // a bare path is not a landing target
		{"//evil.example", ""},
	}
	for _, tc := range cases {
		if got := normalizeUnauthenticatedRouteTarget(tc.input); got != tc.want {
			t.Errorf("normalizeUnauthenticatedRouteTarget(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// A cleared new property must fall back to the legacy name, not to "login":
// that is what makes upgrading a no-op for an install that already set one.
func TestResolveUnauthenticatedRootRouteTargetFallsBackToLegacyProperty(t *testing.T) {
	original := propertyItems
	t.Cleanup(func() { propertyItems = original })

	propertyItems = map[string]any{
		unauthenticatedRootRoutePropertyKey: "",
		legacyRootRoutePropertyKey:          "page:home",
	}
	if got := resolveUnauthenticatedRootRouteTarget(context.Background()); got != "page:home" {
		t.Fatalf("resolved %q, want the legacy page target", got)
	}

	propertyItems = map[string]any{
		unauthenticatedRootRoutePropertyKey: "page:welcome",
		legacyRootRoutePropertyKey:          "page:home",
	}
	if got := resolveUnauthenticatedRootRouteTarget(context.Background()); got != "page:welcome" {
		t.Fatalf("resolved %q, want the explicit unauthenticated target to win", got)
	}

	propertyItems = map[string]any{}
	if got := resolveUnauthenticatedRootRouteTarget(context.Background()); got != "login" {
		t.Fatalf("resolved %q with nothing set, want login", got)
	}
}

func TestSaveRootRouteTargetScopesUnauthenticatedTarget(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)

	original := propertyItems
	t.Cleanup(func() { propertyItems = original })

	if _, err := db.Pool.Exec(ctx, `DELETE FROM _property WHERE key IN ($1, $2, $3)`,
		unauthenticatedRootRoutePropertyKey, authenticatedRootRoutePropertyKey, legacyRootRoutePropertyKey); err != nil {
		t.Fatalf("clear properties: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM _property WHERE key IN ($1, $2)`,
			unauthenticatedRootRoutePropertyKey, authenticatedRootRoutePropertyKey)
	})

	form := url.Values{
		"scope":             {"unauthenticated"},
		"root_route_target": {"page:home"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/root-route/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handleSaveRootRouteTarget(recorder, req)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("save = %d, want 303 (body: %s)", recorder.Code, recorder.Body.String())
	}

	var value string
	if err := db.Pool.QueryRow(ctx, `SELECT value FROM _property WHERE key = $1`, unauthenticatedRootRoutePropertyKey).Scan(&value); err != nil {
		t.Fatalf("read property: %v", err)
	}
	if value != "page:home" {
		t.Fatalf("stored %q, want page:home", value)
	}

	var legacyCount int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(1) FROM _property WHERE key = $1`, legacyRootRoutePropertyKey).Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy: %v", err)
	}
	if legacyCount != 0 {
		t.Error("unauthenticated save also wrote the legacy property")
	}
}

// A landing target that is not a page, table or login is refused rather than
// stored: an open redirect at / is the one thing this must never become.
func TestSaveRootRouteTargetRejectsOpenRedirect(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)

	form := url.Values{
		"scope":             {"unauthenticated"},
		"root_route_target": {"//evil.example.com"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/root-route/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handleSaveRootRouteTarget(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("save of an absolute URL = %d, want 400", recorder.Code)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(1) FROM _property WHERE key = $1`, unauthenticatedRootRoutePropertyKey).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Error("a rejected target was written to the property table")
	}
}

// With the property pointing at a page, a guest is routed into the page branch
// and not to the login form. The page table itself is created by the app sync
// rather than by a migration, so this asserts the routing decision: a missing
// page is a 404 from the page renderer, never a login page.
func TestRootRouteRoutesGuestToPageBranch(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)

	slug := "zz-guest-home-missing"
	if err := setProperty(ctx, unauthenticatedRootRoutePropertyKey, "page:"+slug); err != nil {
		t.Fatalf("set property: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM _property WHERE key = $1`, unauthenticatedRootRoutePropertyKey)
	})

	original := propertyItems
	t.Cleanup(func() { propertyItems = original })
	_ = loadPropertiesFromDB()

	recorder := httptest.NewRecorder()
	handleRootRoute(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("guest GET / with a page target = %d, want 404 from the page renderer", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Page not found") {
		t.Fatalf("guest GET / did not reach the page renderer (body: %s)", recorder.Body.String())
	}
}

// The default is unchanged: with no property set, a guest gets the login page.
func TestRootRouteDefaultsToLoginForGuest(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)

	if _, err := db.Pool.Exec(ctx, `DELETE FROM _property WHERE key IN ($1, $2)`,
		unauthenticatedRootRoutePropertyKey, legacyRootRoutePropertyKey); err != nil {
		t.Fatalf("clear properties: %v", err)
	}

	original := propertyItems
	t.Cleanup(func() { propertyItems = original })
	propertyItems = map[string]any{}

	recorder := httptest.NewRecorder()
	handleRootRoute(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("guest GET / = %d, want 200 (login page)", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "Page not found") {
		t.Error("guest GET / rendered the page renderer; want the login page")
	}
}
