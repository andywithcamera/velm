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
	mdBareURLPattern  = regexp.MustCompile(`https?://[^\s<>"]+`)
	mdOrderedPattern  = regexp.MustCompile(`^[0-9]+\.\s+`)
	mdTagSplitPattern = regexp.MustCompile(`<[^>]+>`)
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
	listTag := "ul"
	flushList := func() {
		if len(listItems) == 0 {
			return
		}
		out = append(out, "<"+listTag+">"+strings.Join(listItems, "")+"</"+listTag+">")
		listItems = listItems[:0]
		listTag = "ul"
	}
	appendListItem := func(tag string, body string) {
		// A change of marker starts a new list: mixing bullet and numbered
		// items through one <ul> loses the numbering the author typed.
		if tag != listTag {
			flushList()
			listTag = tag
		}
		listItems = append(listItems, "<li>"+body+"</li>")
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
		esc = autolinkBareURLs(esc)

		switch {
		case strings.HasPrefix(trim, "- "):
			appendListItem("ul", strings.TrimPrefix(esc, "- "))
		case mdOrderedPattern.MatchString(trim):
			appendListItem("ol", mdOrderedPattern.ReplaceAllString(esc, ""))
		case strings.HasPrefix(trim, "> "):
			flushList()
			out = append(out, "<blockquote>"+strings.TrimPrefix(esc, "&gt; ")+"</blockquote>")
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
	// Safe by construction: every line is fully escaped with
	// template.HTMLEscapeString *before* any markup is applied, all later
	// substitutions only add a fixed tag around already-escaped text, and every
	// href goes through sanitizeURLForHTML (scheme allowlist) plus
	// HTMLEscapeString. Escaping first is what makes the injected tags safe;
	// applying them to raw input would not be.
	return template.HTML(strings.Join(out, "\n")) // #nosec G203 -- escaped before markup, see comment above
}

// autolinkBareURLs turns pasted URLs into links. A task brief is usually
// issue text, and issue text carries bare URLs ("GitHub: #216
// https://github.com/..."). CommonMark does not link those without an
// autolink extension, so without this the most useful thing in a brief was
// dead text — the exact complaint the rendered field was meant to fix.
//
// Only text outside existing tags is scanned: the tag boundary is a fixed
// injected tag, never user input, and a URL already inside href="..." must not
// be wrapped again.
func autolinkBareURLs(escaped string) string {
	if !strings.Contains(escaped, "http") {
		return escaped
	}
	parts := mdTagSplitPattern.Split(escaped, -1)
	tags := mdTagSplitPattern.FindAllString(escaped, -1)
	var builder strings.Builder
	builder.Grow(len(escaped) + 32)
	for i, part := range parts {
		builder.WriteString(linkifyText(part))
		if i < len(tags) {
			builder.WriteString(tags[i])
		}
	}
	return builder.String()
}

func linkifyText(text string) string {
	if !strings.Contains(text, "http") {
		return text
	}
	return mdBareURLPattern.ReplaceAllStringFunc(text, func(match string) string {
		url := strings.TrimRight(match, ".,;:!?'\"")
		// A URL at the end of a parenthesised clause commonly ends with ')'
		// that belongs to the sentence: balance it before trimming.
		for strings.Count(url, ")") > strings.Count(url, "(") && strings.HasSuffix(url, ")") {
			url = strings.TrimSuffix(url, ")")
		}
		if url == "" {
			return match
		}
		href, external := sanitizeURLForHTML(html.UnescapeString(url))
		if href == "#" {
			return match
		}
		suffix := match[len(url):]
		if external {
			return `<a href="` + href + `" target="_blank" rel="noopener noreferrer">` + url + `</a>` + suffix
		}
		return `<a href="` + href + `">` + url + `</a>` + suffix
	})
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
