package main

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

var (
	mdLinkPattern     = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	mdBoldPattern     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalicPattern   = regexp.MustCompile(`\*([^*]+)\*`)
	mdCodePattern     = regexp.MustCompile("`([^`]+)`")
	mdWikilinkPattern = regexp.MustCompile(`\[\[([^\]|]+?)(?:\|([^\]]+?))?\]\]`)
)

func renderMarkdownToSafeHTML(raw string) template.HTML {
	return renderMarkdownToSafeHTMLResolved(raw, nil)
}

// renderMarkdownToSafeHTMLResolved renders markdown to safe HTML, optionally resolving
// [[wikilinks]] to record URLs via the provided resolver. If resolver is nil or returns
// an empty string, unresolved wikilinks are rendered as a styled span.
func renderMarkdownToSafeHTMLResolved(raw string, resolver func(string) string) template.HTML {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	listItems := make([]string, 0, 8)
	flushList := func() {
		if len(listItems) == 0 {
			return
		}
		out = append(out, "<ul>"+strings.Join(listItems, "")+"</ul>")
		listItems = listItems[:0]
	}
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			flushList()
			continue
		}
		esc := template.HTMLEscapeString(trim)
		esc = mdCodePattern.ReplaceAllString(esc, "<code>$1</code>")
		esc = mdBoldPattern.ReplaceAllString(esc, "<strong>$1</strong>")
		esc = mdItalicPattern.ReplaceAllString(esc, "<em>$1</em>")
		esc = mdLinkPattern.ReplaceAllStringFunc(esc, func(m string) string {
			sub := mdLinkPattern.FindStringSubmatch(m)
			if len(sub) != 3 {
				return m
			}
			href, external := sanitizeURLForHTML(sub[2])
			if external {
				return `<a href="` + href + `" target="_blank" rel="noopener noreferrer">` + sub[1] + `</a>`
			}
			return `<a href="` + href + `">` + sub[1] + `</a>`
		})
		esc = mdWikilinkPattern.ReplaceAllStringFunc(esc, func(m string) string {
			sub := mdWikilinkPattern.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			// sub[1] is HTML-escaped; unescape for DB lookup and use escaped version for display.
			escapedTitle := strings.TrimSpace(sub[1])
			display := escapedTitle
			if len(sub) > 2 && strings.TrimSpace(sub[2]) != "" {
				display = strings.TrimSpace(sub[2])
			}
			if resolver != nil {
				rawTitle := html.UnescapeString(escapedTitle)
				if href := resolver(rawTitle); href != "" {
					return `<a href="` + template.HTMLEscapeString(href) + `" class="docs-wikilink">` + display + `</a>`
				}
			}
			return `<span class="docs-wikilink docs-wikilink-unresolved" title="No matching record found">` + display + `</span>`
		})

		switch {
		case strings.HasPrefix(trim, "- "):
			listItems = append(listItems, "<li>"+strings.TrimPrefix(esc, "- ")+"</li>")
		case strings.HasPrefix(trim, "### "):
			flushList()
			out = append(out, "<h3>"+strings.TrimPrefix(esc, "### ")+"</h3>")
		case strings.HasPrefix(trim, "## "):
			flushList()
			out = append(out, "<h2>"+strings.TrimPrefix(esc, "## ")+"</h2>")
		case strings.HasPrefix(trim, "# "):
			flushList()
			out = append(out, "<h1>"+strings.TrimPrefix(esc, "# ")+"</h1>")
		default:
			flushList()
			out = append(out, "<p>"+esc+"</p>")
		}
	}
	flushList()
	return template.HTML(strings.Join(out, "\n"))
}

func sanitizeURLForHTML(raw string) (string, bool) {
	href := strings.TrimSpace(raw)
	lower := strings.ToLower(href)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:") {
		return template.HTMLEscapeString(href), true
	}
	if strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
		return template.HTMLEscapeString(href), false
	}
	if strings.HasPrefix(href, "#") {
		return template.HTMLEscapeString(href), false
	}
	if !strings.HasPrefix(href, "//") && !strings.Contains(lower, ":") {
		return template.HTMLEscapeString(href), false
	}
	return "#", false
}
