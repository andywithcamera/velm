package main

import (
	"html/template"
	"regexp"
	"strings"
)

var (
	mdLinkPattern   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	mdBoldPattern   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalicPattern = regexp.MustCompile(`\*([^*]+)\*`)
	mdCodePattern   = regexp.MustCompile("`([^`]+)`")
)

func renderMarkdownToSafeHTML(raw string) template.HTML {
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
