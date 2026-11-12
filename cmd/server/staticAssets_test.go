package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStaticAssetsAreRevalidated guards issue #188: /static/ must carry its own
// Cache-Control rather than inheriting a long browser/edge default. Filenames
// are not content-hashed, so a long max-age lets a stale JS file run against
// markup from a newer deploy. "no-cache" still allows storage, but every reuse
// must revalidate (the FileServer answers 304 from Last-Modified), so a change
// in production is picked up on the next request.
func TestStaticAssetsAreRevalidated(t *testing.T) {
	t.Chdir("../..")
	handler := newStaticAssetHandler("./web/static")

	req := httptest.NewRequest(http.MethodGet, "/css/ui.css", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /css/ui.css: status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("GET /css/ui.css: Cache-Control = %q, want %q", got, "no-cache")
	}
}

// TestStaticAssetsReturnNotModified confirms revalidation is cheap: an
// If-Modified-Since request against the served file yields 304, so dropping
// the long max-age does not turn every page load into a full asset download.
func TestStaticAssetsReturnNotModified(t *testing.T) {
	t.Chdir("../..")
	handler := newStaticAssetHandler("./web/static")

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/css/ui.css", nil))
	lastModified := first.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Fatal("GET /css/ui.css: no Last-Modified header, cannot revalidate")
	}

	req := httptest.NewRequest(http.MethodGet, "/css/ui.css", nil)
	req.Header.Set("If-Modified-Since", lastModified)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional GET /css/ui.css: status = %d, want 304", rec.Code)
	}
}

// TestStaticAssetsStillServeAndMiss checks the wrapper did not change what the
// file server does: an existing asset is served, a missing one is a 404.
func TestStaticAssetsStillServeAndMiss(t *testing.T) {
	t.Chdir("../..")
	handler := newStaticAssetHandler("./web/static")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/js/navigation-guard.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /js/navigation-guard.js: status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/js/deleted-asset.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /js/deleted-asset.js: status = %d, want 404", rec.Code)
	}
}
