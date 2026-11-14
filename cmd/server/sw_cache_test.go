package main

import (
	"os"
	"strings"
	"testing"
)

// The service worker used to serve /static/ cache-first, justified by a comment
// claiming those URLs were "immutable, versioned". They are not: templates
// request /static/js/push-client.js with no ?v= and VERSION was never bumped
// from 'velm-sw-v1'. A device that had loaded an asset once kept it forever,
// overriding the origin's Cache-Control: no-cache, so every front-end fix
// shipped after install was invisible (#348). These tests pin the two
// properties that made that possible.

// staticBranchSource returns the sw.js text of the /static/ handling branch.
func staticBranchSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../web/static/sw.js")
	if err != nil {
		t.Fatalf("read sw.js: %v", err)
	}
	src := string(raw)
	i := strings.Index(src, "startsWith('/static/')")
	if i < 0 {
		t.Fatal("sw.js no longer has a /static/ branch; update this ratchet")
	}
	end := strings.Index(src[i:], "\n  }")
	if end < 0 {
		t.Fatal("could not delimit the /static/ branch in sw.js")
	}
	return src[i : i+end]
}

// A cache-first branch calls caches.match before fetch, so the network never
// gets a say in whether the cached copy is current.
func TestServiceWorkerStaticAssetsAreNetworkFirst(t *testing.T) {
	branch := staticBranchSource(t)
	fetchAt := strings.Index(branch, "fetch(event.request)")
	matchAt := strings.Index(branch, "caches.match(event.request)")
	if fetchAt < 0 {
		t.Fatalf("static branch never fetches:\n%s", branch)
	}
	if matchAt >= 0 && matchAt < fetchAt {
		t.Fatalf("static branch serves the cache before the network (cache-first):\n%s", branch)
	}
	if !strings.Contains(branch, "catch") {
		t.Fatalf("static branch has no offline fallback:\n%s", branch)
	}
}

// An update check answered by an HTTP cache never reaches the origin, so a
// changed worker waits for max-age to expire instead of being installed. The
// handler is registered on http.DefaultServeMux, so the real registration is
// what must be asserted rather than a copy of it.
func TestServiceWorkerRouteServesUncachedWorker(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	i := strings.Index(string(src), `"/sw.js"`)
	if i < 0 {
		t.Fatal("routes.go no longer registers /sw.js")
	}
	end := strings.Index(string(src)[i:], "})))")
	if end < 0 {
		t.Fatal("could not delimit the /sw.js handler")
	}
	handlerSrc := string(src)[i : i+end]
	if !strings.Contains(handlerSrc, `Cache-Control", "no-cache"`) {
		t.Fatalf("/sw.js handler does not set Cache-Control: no-cache:\n%s", handlerSrc)
	}
	if !strings.Contains(handlerSrc, `Service-Worker-Allowed", "/"`) {
		t.Fatalf("/sw.js handler lost its root scope header")
	}
}
