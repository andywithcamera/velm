package db

import (
	stdhtml "html"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var richTextVoidTags = map[string]bool{
	"br": true,
	"hr": true,
}

var richTextDropSubtreeTags = map[string]bool{
	"button":   true,
	"embed":    true,
	"form":     true,
	"iframe":   true,
	"input":    true,
	"link":     true,
	"meta":     true,
	"object":   true,
	"script":   true,
	"select":   true,
	"style":    true,
	"textarea": true,
}

func SanitizeRichTextHTML(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}

	nodes, err := xhtml.ParseFragment(strings.NewReader(trimmed), &xhtml.Node{
		Type:     xhtml.ElementNode,
		Data:     "div",
		DataAtom: atom.Div,
	})
	if err != nil {
		return stdhtml.EscapeString(trimmed)
	}

	var out strings.Builder
	for _, node := range nodes {
		writeSanitizedRichTextNode(&out, node, false)
	}
	return strings.TrimSpace(out.String())
}

func writeSanitizedRichTextNode(out *strings.Builder, node *xhtml.Node, preserveWhitespace bool) {
	if node == nil {
		return
	}

	switch node.Type {
	case xhtml.TextNode:
		text := node.Data
		if !preserveWhitespace && strings.TrimSpace(text) == "" {
			if strings.Contains(text, "\n") || strings.Contains(text, "\r") || strings.Contains(text, "\t") {
				text = " "
			}
		}
		out.WriteString(stdhtml.EscapeString(text))
	case xhtml.ElementNode:
		tag := canonicalRichTextTag(node.Data)
		if richTextDropSubtreeTags[tag] {
			return
		}
		if tag == "" {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				writeSanitizedRichTextNode(out, child, preserveWhitespace || strings.EqualFold(node.Data, "pre"))
			}
			return
		}

		out.WriteByte('<')
		out.WriteString(tag)
		if tag == "a" {
			if href := sanitizeRichTextURL(attrValue(node, "href")); href != "" {
				out.WriteString(` href="`)
				out.WriteString(href)
				out.WriteString(`" target="_blank" rel="noopener noreferrer"`)
			}
		}
		out.WriteByte('>')
		if richTextVoidTags[tag] {
			return
		}

		childPreserveWhitespace := preserveWhitespace || tag == "pre" || tag == "code"
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			writeSanitizedRichTextNode(out, child, childPreserveWhitespace)
		}
		out.WriteString("</")
		out.WriteString(tag)
		out.WriteByte('>')
	default:
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			writeSanitizedRichTextNode(out, child, preserveWhitespace)
		}
	}
}

func canonicalRichTextTag(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "a":
		return "a"
	case "b", "strong":
		return "strong"
	case "blockquote":
		return "blockquote"
	case "br":
		return "br"
	case "code":
		return "code"
	case "div":
		return "div"
	case "em", "i":
		return "em"
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return strings.ToLower(strings.TrimSpace(input))
	case "hr":
		return "hr"
	case "li":
		return "li"
	case "ol":
		return "ol"
	case "p":
		return "p"
	case "pre":
		return "pre"
	case "s":
		return "s"
	case "span":
		return "span"
	case "u":
		return "u"
	case "ul":
		return "ul"
	default:
		return ""
	}
}

func sanitizeRichTextURL(raw string) string {
	href := strings.TrimSpace(raw)
	lower := strings.ToLower(href)
	if strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "tel:") {
		return stdhtml.EscapeString(href)
	}
	return ""
}

func attrValue(node *xhtml.Node, name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, attr := range node.Attr {
		if strings.ToLower(strings.TrimSpace(attr.Key)) == name {
			return attr.Val
		}
	}
	return ""
}
