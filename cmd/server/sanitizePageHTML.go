package main

import (
	"regexp"

	"github.com/microcosm-cc/bluemonday"
)

// pageHTMLPolicy is the single allowlist policy applied to page content before
// it is handed to the template as template.HTML. template.HTML disables Go's
// contextual escaping, so this policy is the only barrier between stored page
// content and the browser — it must never be bypassed.
//
// Built from bluemonday's UGCPolicy: it drops <script>, <style>, <iframe> and
// every on* event handler regardless of how the attribute is quoted, and
// strips javascript:/data: URLs. Only additions are made below, never
// removals.
var pageHTMLPolicy = newPageHTMLPolicy()

// pageHTMLElementIDPattern constrains id values to something inert: an
// in-page anchor target. Letters, digits, hyphen and underscore only.
var pageHTMLElementIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// pageHTMLClassNamePattern constrains class values to a space-separated list of
// inert tokens (the app's ui-* vocabulary and Tailwind utility classes).
var pageHTMLClassNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(\s+[A-Za-z0-9_-]+)*$`)

func newPageHTMLPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// UGCPolicy deliberately refuses class so that authors cannot restyle
	// themselves. Velm pages are authored against the app's own stylesheet, so
	// class and id are allowed — both are inert; neither can execute script.
	p.AllowAttrs("class").Matching(pageHTMLClassNamePattern).Globally()
	p.AllowAttrs("id").Matching(pageHTMLElementIDPattern).Globally()
	return p
}

// sanitizePageHTML returns page content made safe for rendering as raw HTML.
func sanitizePageHTML(input string) string {
	return pageHTMLPolicy.Sanitize(input)
}
