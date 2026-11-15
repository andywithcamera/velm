package main

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"velm/internal/auth"
	"velm/internal/authz"

	"github.com/gorilla/sessions"
)

// ensureTestSessionStore installs the cookie store the page renderers need for
// their CSRF token. Bootstrap does this at start-up; a handler test that renders
// a full page needs it too.
func ensureTestSessionStore() {
	if store == nil {
		store = sessions.NewCookieStore(
			[]byte("0123456789abcdef0123456789abcdef"),
			[]byte("0123456789abcdef0123456789abcdef"),
		)
	}
	if templates == nil {
		// The package runs with cmd/server as its working directory.
		tpl := template.Must(template.ParseGlob("../../web/templates/*.html"))
		template.Must(tpl.ParseGlob("../../web/templates/components/*.html"))
		templates = tpl
	}
}

// Slice 1 of the task definition editor: the list, creating a definition, and
// the basics section. These tests need no database; the ones that do live in
// handleTaskDefinitions_integration_test.go and are gated on TEST_DATABASE_URL.

func TestValidateTaskDefinitionSlug(t *testing.T) {
	cases := []struct {
		name  string
		slug  string
		valid bool
	}{
		{"hyphenated is the column's own example", "reboot-server", true},
		{"digits and letters", "os-upgrade-2", true},
		{"single character", "a", true},
		{"leading digit", "2fa-reset", true},
		{"empty", "", false},
		{"uppercase", "Reboot-Server", false},
		{"leading hyphen", "-reboot", false},
		{"underscore", "reboot_server", false},
		{"space", "reboot server", false},
		{"trailing newline", "reboot\n", false},
		{"too long", strings.Repeat("a", taskDefinitionSlugMaxLength+1), false},
		{"at the limit", strings.Repeat("a", taskDefinitionSlugMaxLength), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := validateTaskDefinitionSlug(tc.slug)
			if tc.valid && reason != "" {
				t.Fatalf("slug %q refused: %s", tc.slug, reason)
			}
			if !tc.valid && reason == "" {
				t.Fatalf("slug %q accepted but should not be", tc.slug)
			}
		})
	}
}

// A refusal has to say what shape a slug takes, or the author is left guessing
// which of the column's rules they broke.
func TestSlugRefusalNamesTheRule(t *testing.T) {
	if reason := validateTaskDefinitionSlug("Reboot_Server"); !strings.Contains(reason, "lowercase") {
		t.Fatalf("slug refusal %q does not describe the accepted shape", reason)
	}
}

// The create page must write nothing and carry the CSRF token and the slug
// control; the render function is exercised directly so no connection is
// needed.
func TestNewDefinitionPageRendersForm(t *testing.T) {
	ensureTestSessionStore()
	viewData := func() map[string]any {
		r := httptest.NewRequest("GET", "/task-definitions/new", nil)
		w := httptest.NewRecorder()
		data := newViewData(w, r, "/task-definitions/new", "New Task Definition", "Operations")
		data["View"] = "task-definition-new"
		data["NewSlug"] = ""
		data["NewDisplayName"] = ""
		data["NewDescription"] = ""
		data["NewIsContainer"] = false
		data["FormError"] = ""
		return data
	}()

	rec := httptest.NewRecorder()
	writeTaskDefinitionPage(rec, "task-definition-new-view", viewData, 200)
	body := rec.Body.String()

	for _, want := range []string{`name="slug"`, `name="display_name"`, `name="description"`, `name="is_container"`, `name="csrf_token"`, `action="/task-definitions"`} {
		if !strings.Contains(body, want) {
			t.Errorf("create form is missing %s", want)
		}
	}
}

// The three templates this slice adds must exist and parse as a set, the same
// way layout.html loads them at start-up.
func TestTaskDefinitionTemplatesParse(t *testing.T) {
	parsed, err := template.ParseGlob("../../web/templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	for _, name := range []string{
		"task-definitions-view",
		"task-definition-new-view",
		"task-definition-editor-view",
		"task-definition-input-row",
		"task-definition-inputs-card",
	} {
		if parsed.Lookup(name) == nil {
			t.Errorf("template %q is not defined", name)
		}
	}
}

// AC-68: the new routes carry the permission they claim, and downgrading any of
// them turns this test red. The read pages must reach a view-only identity; the
// write actions must not.
func TestTaskDefinitionRoutesRequireTheirStatedPermission(t *testing.T) {
	routes := parseRegisteredRoutes(t)

	expected := map[string]accessClass{
		"GET /task-definitions":                        accessView,
		"POST /task-definitions":                       accessWrite,
		"GET /task-definitions/new":                    accessView,
		"GET /task-definitions/{slug}":                 accessView,
		"POST /task-definitions/{slug}/basics":         accessWrite,
		"GET /task-definitions/{slug}/inputs/rows/new": accessView,
		"POST /task-definitions/{slug}/inputs":         accessWrite,
		"POST /task-definitions/{slug}/inputs/raw":     accessWrite,
	}
	for pattern, want := range expected {
		if got := routeAccess[pattern]; got != want {
			t.Fatalf("routeAccess[%q] = %q, want %q", pattern, got, want)
		}
		if got, ok := routes[pattern]; !ok {
			t.Fatalf("route %q is not registered", pattern)
		} else if got != want {
			t.Fatalf("route %q is registered as %q, want %q", pattern, got, want)
		}
	}

	original := authz.PermissionResolver
	t.Cleanup(func() { authz.PermissionResolver = original })

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})

	for pattern, want := range expected {
		method, path := splitMethodPattern(pattern)
		for role, granted := range map[string]map[string]bool{
			"viewer": {"view": true},
			"editor": {"view": true, "write": true},
		} {
			authz.PermissionResolver = func(_ context.Context, userID, permission, _ string) (bool, error) {
				if userID == "" {
					return false, nil
				}
				return granted[permission], nil
			}
			reached = false
			handler := authz.RequirePermission(authz.Permission(want), next)
			req := httptest.NewRequest(method, path, nil)
			req = req.WithContext(auth.WithUserContext(req.Context(), "user-1", "u@example.com", "User", role))
			handler.ServeHTTP(httptest.NewRecorder(), req)

			shouldReach := granted[string(want)]
			if reached != shouldReach {
				t.Errorf("%s as %s: reached=%v, want %v (the route's permission is %q)", pattern, role, reached, shouldReach, want)
			}
		}
	}
}

// AC-40: the write actions are registered behind PermissionWrite and a CSRF
// check, read straight from routes.go so the test fails if the guard is edited
// away.
func TestTaskDefinitionWriteRoutesCarryCSRF(t *testing.T) {
	source, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	for _, pattern := range []string{
		"POST /task-definitions",
		"POST /task-definitions/{slug}/basics",
		"POST /task-definitions/{slug}/inputs",
		"POST /task-definitions/{slug}/inputs/raw",
	} {
		found := false
		for _, line := range strings.Split(string(source), "\n") {
			if !strings.Contains(line, `"`+pattern+`"`) {
				continue
			}
			found = true
			if !strings.Contains(line, "withAuth(authz.PermissionWrite") {
				t.Errorf("route %q is not gated on PermissionWrite", pattern)
			}
			if !strings.Contains(line, "auth.RequireCSRF") {
				t.Errorf("route %q is not wrapped in auth.RequireCSRF", pattern)
			}
		}
		if !found {
			t.Errorf("route %q is not registered in routes.go", pattern)
		}
	}
}
