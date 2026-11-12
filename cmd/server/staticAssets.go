package main

import "net/http"

// staticAssetCacheControl is what /static/ responses carry (issue #188).
//
// The asset filenames under web/static are not content-hashed, so they cannot
// be cached immutable: /static/js/navigation-guard.js means whatever the
// current deploy built. Without an explicit header the handler inherits the
// CDN's default (Cloudflare's max-age=14400), which let a browser run a
// four-hour-old JS file against freshly deployed markup after a release.
//
// no-cache keeps the cheap path — the file may be stored, and http.FileServer
// answers a conditional request with 304 Not Modified from Last-Modified — but
// forbids reuse without revalidation, so a deploy takes effect on the next
// page load. Getting the long max-age back requires hashed filenames and an
// asset manifest first.
const staticAssetCacheControl = "no-cache"

// newStaticAssetHandler serves root and pins the cache policy for every asset.
func newStaticAssetHandler(root string) http.Handler {
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", staticAssetCacheControl)
		files.ServeHTTP(w, r)
	})
}
