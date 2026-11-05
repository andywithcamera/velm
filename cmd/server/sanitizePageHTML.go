package main

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// pageHTMLPolicy is the single allowlist policy applied to page content before
// it is handed to the template as template.HTML. template.HTML disables Go's
// contextual escaping, so this policy is the only barrier between stored page
// content and the browser — it must never be bypassed.
//
// Built from bluemonday's UGCPolicy: it drops <script>, <style>, <iframe> and
// every on* event handler regardless of how the attribute is quoted, and
// strips javascript:/data: URLs. Additions are made below; the one thing that
// cannot be added through the policy API is a narrower `id` rule, which is
// enforced by the post-pass in sanitizePageHTML.
var pageHTMLPolicy = newPageHTMLPolicy()

// pageHTMLElementIDPattern is the shape an id must have to survive: an in-page
// anchor target. It starts with a letter so a value cannot be interpreted as a
// number, and it admits no spaces, quotes, or attribute-shaped text.
//
// This pattern is enforced in stripDisallowedElementIDs, not by the policy.
// bluemonday's UGCPolicy registers its own unanchored `id` rule via
// AllowStandardAttributes, AllowAttrs(...).Globally() appends rather than
// replaces, and sanitising takes the first matching rule — so a narrower rule
// declared here would be inert. Do not re-add it to newPageHTMLPolicy: a rule
// that never runs is worse than no rule, because it reads as a guarantee.
var pageHTMLElementIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,63}$`)

// pageHTMLClassNamePattern constrains class values to a space-separated list of
// inert tokens (the app's ui-* vocabulary and Tailwind utility classes).
var pageHTMLClassNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(\s+[A-Za-z0-9_-]+)*$`)

func newPageHTMLPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// UGCPolicy deliberately refuses class so that authors cannot restyle
	// themselves. Velm pages are authored against the app's own stylesheet, so
	// class is allowed -- it is inert and cannot execute script. This one does
	// work: UGC registers no class rule, so this is the only entry for it.
	p.AllowAttrs("class").Matching(pageHTMLClassNamePattern).Globally()
	return p
}

// sanitizePageHTML returns page content made safe for rendering as raw HTML.
func sanitizePageHTML(input string) string {
	return stripDisallowedElementIDs(pageHTMLPolicy.Sanitize(input))
}

// stripDisallowedElementIDs removes every id attribute whose value does not
// match pageHTMLElementIDPattern.
//
// The allowlist cannot express this: UGCPolicy's built-in `id` rule is
// unanchored, attribute policies append, and the first match wins, so the
// permissive rule always answers first. Removing the attribute afterwards is
// the only enforcement available in bluemonday 1.0.27.
//
// What this is worth: no page content currently needs an id to be inert --
// the values are escaped and quoted either way -- but a caller downstream, or
// CSS in an embedded stylesheet, could be made to act on an id an author
// chose. Restricting the vocabulary keeps ids meaningless unless they are
// genuine anchor targets.
func stripDisallowedElementIDs(sanitized string) string {
	if !strings.Contains(sanitized, "id") {
		return sanitized
	}
	// Parse in a <div> context so a fragment is not wrapped in <html><body>.
	context := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(sanitized), context)
	if err != nil {
		// Unparseable output from a sanitiser should be impossible; if it
		// happens, refuse the content rather than pass it through unchecked.
		return ""
	}

	var b strings.Builder
	for _, n := range nodes {
		removeDisallowedIDs(n)
		if err := html.Render(&b, n); err != nil {
			return ""
		}
	}
	return b.String()
}

func removeDisallowedIDs(n *html.Node) {
	if n.Type == html.ElementNode {
		kept := n.Attr[:0]
		for _, a := range n.Attr {
			if a.Namespace == "" && a.Key == "id" && !pageHTMLElementIDPattern.MatchString(a.Val) {
				continue
			}
			kept = append(kept, a)
		}
		n.Attr = kept
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		removeDisallowedIDs(c)
	}
}
