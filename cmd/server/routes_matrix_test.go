package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"velm/internal/auth"
	"velm/internal/authz"
)

// accessClass is the authorization requirement a route is registered with.
type accessClass string

const (
	accessPublic accessClass = "public"
	accessAuthed accessClass = "authed"
	accessView   accessClass = "view"
	accessWrite  accessClass = "write"
	accessAdmin  accessClass = "admin"
	accessAgent  accessClass = "agent"
)

// routeAccess is the reviewed expectation for every route in routes.go.
//
// It is a specification, not a snapshot: if you change a route's guard, change
// this table in the same commit so the diff shows the security decision. The
// test fails in both directions, so a new route with no entry here fails, and
// an entry with no route fails.
var routeAccess = map[string]accessClass{
	// Public: reachable without a session by design.
	"/":                   accessPublic,
	"/health":             accessPublic,
	"/healthz":            accessPublic,
	"/version":            accessPublic,
	"/static/":            accessPublic,
	"/sw.js":              accessPublic,
	"/offline.html":       accessPublic,
	"/login":              accessPublic,
	"/login/":             accessPublic,
	"/auth/login":         accessPublic,
	"/auth/login/":        accessPublic,
	"/api/monitor/client": accessPublic,
	// The observability webhook authenticates by HMAC signature in the handler,
	// so the route itself carries no session or permission gate.
	"/api/observability/webhook": accessPublic,

	// /forbidden must be reachable by any authenticated user, including one
	// who just got denied, or the redirect loops.
	"/forbidden": accessAuthed,

	// Agent-or-session: the two runtime endpoints, see the comment in routes.go.
	"/api/app-runtime/call": accessAgent,
	"/api/":                 accessAgent,

	// Agent-or-session with a write gate: task instance spawn (issue #224).
	// Accepts an agent bearer token, but the caller still needs the write
	// permission in scope, like any /api/save/ client.
	"/api/task-instances": accessWrite,
	"/mcp":                accessWrite,

	// Admin-only: minting agent API tokens (issue #240 phase 1). Session
	// admins only — an agent token must not be able to mint more tokens.
	"/api/agent-tokens": accessAdmin,

	// Read.
	"/p/":                              accessView,
	"/map":                             accessView,
	"/task-catalog":                    accessView,
	"/task-catalog/order":              accessWrite,
	"/task":                            accessView,
	"/task/":                           accessView,
	"/t/":                              accessView,
	"/f/":                              accessView,
	"/f/graph/":                        accessView,
	"/api/links/":                      accessWrite,
	"/docs":                            accessView,
	"/docs/manage":                     accessView,
	"/d/":                              accessView,
	"/knowledge":                       accessView,
	"/logout":                          accessView,
	"/api/menu/":                       accessView,
	"/api/search/":                     accessView,
	"/api/export/":                     accessView,
	"/api/reference-options":           accessView,
	"/api/realtime/stream":             accessView,
	"/api/realtime/record-version":     accessView,
	"/api/realtime/presence/heartbeat": accessView,
	"/api/push/config":                 accessPublic,
	"/api/push/subscribe":              accessView,
	"/api/push/unsubscribe":            accessView,
	"/api/notifications/panel":         accessView,
	"/api/notifications/read-all":      accessView,
	"/api/notifications/open":          accessView,
	"/api/preferences/list-view":       accessView,
	"/api/preferences/theme":           accessView,
	"/api/preferences/list-view/saved": accessView,
	"/api/form/security-preview":       accessView,
	"/api/docs/preview":                accessView,
	"/api/docs/wikilink-search":        accessView,
	"/api/scripts/scope":               accessView,

	// Write: mutates records, pages or schema.
	"/builder/schema":               accessWrite,
	"/builder/pages":                accessWrite,
	"/admin/app-editor":             accessWrite,
	"/api/save/":                    accessWrite,
	"/api/comments/add":             accessWrite,
	"/api/bulk/update/":             accessWrite,
	"/api/delete/":                  accessWrite,
	"/api/restore/":                 accessWrite,
	"/api/builder/table/create":     accessWrite,
	"/api/builder/table/update":     accessWrite,
	"/api/builder/table/delete":     accessWrite,
	"/api/builder/column/create":    accessWrite,
	"/api/builder/column/update":    accessWrite,
	"/api/builder/column/delete":    accessWrite,
	"/api/builder/schema/apply":     accessWrite,
	"/api/app-editor/app/create":    accessWrite,
	"/api/app-editor/object/save":   accessWrite,
	"/api/app-editor/object/delete": accessWrite,
	"/api/app-editor/publish":       accessWrite,
	"/api/pages/save":               accessWrite,
	"/api/root-route/save":          accessWrite,
	"/api/docs/library/save":        accessWrite,
	"/api/docs/library/archive":     accessWrite,
	"/api/docs/article/save":        accessWrite,
	"/api/docs/article/archive":     accessWrite,

	// Admin: identity, roles, providers, scripts and audit surfaces.
	"/admin/access":                        accessAdmin,
	"/admin/audit":                         accessAdmin,
	"/admin/monitoring":                    accessAdmin,
	"/admin/scripts":                       accessAdmin,
	"/admin/run-script":                    accessAdmin,
	"/api/ai/connections":                  accessAdmin,
	"/api/ai/connections/{id}":             accessAdmin,
	"/api/ai/connections/{id}/models/sync": accessAdmin,
	"/api/scripts/test":                    accessAdmin,
	"/api/scripts/run-adhoc":               accessAdmin,
	"/api/authz/roles/create":              accessAdmin,
	"/api/authz/permissions/create":        accessAdmin,
	"/api/authz/group-memberships/grant":   accessAdmin,
	"/api/authz/group-memberships/revoke":  accessAdmin,
	"/api/authz/role-permissions/grant":    accessAdmin,
	"/api/authz/role-permissions/revoke":   accessAdmin,
}

// parseRegisteredRoutes reads routes.go and reports the access class each
// route is registered with, straight from the source rather than from a
// hand-maintained copy.
func parseRegisteredRoutes(t *testing.T) map[string]accessClass {
	t.Helper()

	fset := token.NewFileSet()

	// Parse the whole package, not just routes.go: route patterns are sometimes
	// package constants declared next to their handler.
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}

	consts := map[string]string{}
	var files []*ast.File
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			files = append(files, file)
			ast.Inspect(file, func(n ast.Node) bool {
				spec, ok := n.(*ast.ValueSpec)
				if !ok {
					return true
				}
				for i, ident := range spec.Names {
					if i >= len(spec.Values) {
						continue
					}
					if lit, ok := spec.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if value, err := strconv.Unquote(lit.Value); err == nil {
							consts[ident.Name] = value
						}
					}
				}
				return true
			})
		}
	}

	routes := map[string]accessClass{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "http" || selector.Sel.Name != "Handle" {
				return true
			}

			pattern := ""
			switch arg := call.Args[0].(type) {
			case *ast.BasicLit:
				pattern, _ = strconv.Unquote(arg.Value)
			case *ast.Ident:
				pattern = consts[arg.Name]
			}
			if pattern == "" {
				t.Errorf("route registered at %s has a pattern the scanner cannot read", fset.Position(call.Pos()))
				return true
			}
			if _, dup := routes[pattern]; dup {
				t.Errorf("route %q registered twice", pattern)
			}
			routes[pattern] = classifyHandler(t, fset, call.Args[1])
			return true
		})
	}

	return routes
}

func classifyHandler(t *testing.T, fset *token.FileSet, handler ast.Expr) accessClass {
	t.Helper()

	call, ok := handler.(*ast.CallExpr)
	if !ok {
		t.Errorf("handler at %s is not a call; the scanner cannot classify it", fset.Position(handler.Pos()))
		return accessPublic
	}
	// auth.RequireAuth(...) is the authenticated-but-not-permission-gated chain
	// used by /forbidden.
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "auth" && selector.Sel.Name == "RequireAuth" {
			return accessAuthed
		}
		return accessPublic
	}

	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return accessPublic
	}

	switch ident.Name {
	case "withAuth":
		if len(call.Args) == 0 {
			t.Errorf("withAuth at %s has no permission argument", fset.Position(call.Pos()))
			return accessPublic
		}
		sel, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok {
			t.Errorf("withAuth permission at %s is not a constant", fset.Position(call.Pos()))
			return accessPublic
		}
		switch sel.Sel.Name {
		case "PermissionView":
			return accessView
		case "PermissionWrite":
			return accessWrite
		case "PermissionAdmin":
			return accessAdmin
		default:
			t.Errorf("withAuth at %s uses unknown permission %q", fset.Position(call.Pos()), sel.Sel.Name)
			return accessPublic
		}
	case "withAuthOrAgentOnly":
		return accessAgent
	case "withAuthOrAgentWrite":
		// Agent-or-session chain plus the write permission gate; same authz
		// surface as withAuth(write) (issue #224).
		return accessWrite
	}
	return accessPublic
}

// TestRouteAccessTableMatchesSource is the completeness check: every route in
// routes.go is in routeAccess, every entry in routeAccess is a real route, and
// the class matches what the code actually registers.
func TestRouteAccessTableMatchesSource(t *testing.T) {
	routes := parseRegisteredRoutes(t)
	if len(routes) < 50 {
		t.Fatalf("scanner found only %d routes; routes.go parser is probably broken", len(routes))
	}

	var unlisted, stale []string
	for pattern := range routes {
		if _, ok := routeAccess[pattern]; !ok {
			unlisted = append(unlisted, pattern)
		}
	}
	for pattern := range routeAccess {
		if _, ok := routes[pattern]; !ok {
			stale = append(stale, pattern)
		}
	}
	sort.Strings(unlisted)
	sort.Strings(stale)

	for _, pattern := range unlisted {
		t.Errorf("route %q is registered but missing from routeAccess: add it with the access class you intend", pattern)
	}
	for _, pattern := range stale {
		t.Errorf("routeAccess lists %q but routes.go does not register it: remove the entry", pattern)
	}

	for pattern, actual := range routes {
		expected, ok := routeAccess[pattern]
		if !ok {
			continue
		}
		if actual != expected {
			t.Errorf("route %q is registered as %q but routeAccess says %q", pattern, actual, expected)
		}
	}
}

// TestRoutePermissionMatrix drives the real permission middleware for every
// route that declares one, across the three roles, and asserts the handler is
// reached only when the role holds the required permission.
//
// Session freshness and CSRF are exercised by their own tests; this test is
// about the route-to-permission decision, so the resolver is stubbed and no
// database is involved.
func TestRoutePermissionMatrix(t *testing.T) {
	routes := parseRegisteredRoutes(t)

	roles := map[string]map[string]bool{
		"anonymous": {},
		"viewer":    {"view": true},
		"editor":    {"view": true, "write": true},
		"admin":     {"view": true, "write": true, "admin": true},
	}

	original := authz.PermissionResolver
	t.Cleanup(func() { authz.PermissionResolver = original })

	sentinel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	patterns := make([]string, 0, len(routes))
	for pattern := range routes {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)

	for _, pattern := range patterns {
		required := routes[pattern]
		if required != accessView && required != accessWrite && required != accessAdmin {
			continue
		}

		for role, granted := range roles {
			role, granted := role, granted
			authz.PermissionResolver = func(_ context.Context, userID, permission, _ string) (bool, error) {
				if userID == "" {
					return false, nil
				}
				return granted[permission], nil
			}

			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				sentinel.ServeHTTP(w, r)
			})

			handler := authz.RequirePermission(authz.Permission(required), next)
			req := httptest.NewRequest(http.MethodGet, pattern, nil)
			if role != "anonymous" {
				req = req.WithContext(auth.WithUserContext(req.Context(), "user-1", "u@example.com", "User", role))
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			shouldReach := granted[string(required)]
			if reached != shouldReach {
				t.Errorf("%s %s: reached=%v, want %v", pattern, role, reached, shouldReach)
			}
			if !shouldReach && rec.Code == http.StatusNoContent {
				t.Errorf("%s %s: denied but wrote a success response", pattern, role)
			}
			if !shouldReach && !strings.Contains(rec.Header().Get("Location"), "/forbidden") {
				t.Errorf("%s %s: denial did not redirect to /forbidden (got %q)", pattern, role, rec.Header().Get("Location"))
			}
		}
	}
}

// TestPublicAndAgentRoutesCarryNoPermissionGate guards the other direction: a
// route classified public or agent must not be wrapped in withAuth.
func TestPublicAndAgentRoutesCarryNoPermissionGate(t *testing.T) {
	routes := parseRegisteredRoutes(t)
	for pattern, class := range routes {
		if class == accessPublic || class == accessAgent {
			if expected := routeAccess[pattern]; expected != class {
				t.Errorf("route %q changed class: source %q, table %q", pattern, class, expected)
			}
		}
	}
}
