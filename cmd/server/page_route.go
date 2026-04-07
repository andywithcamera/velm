package main

import (
	"strings"

	"velm/internal/db"
)

func normalizePageRouteSlug(input string) string {
	slug := strings.TrimSpace(strings.ToLower(input))
	switch {
	case db.IsQualifiedPageSlug(slug):
		return slug
	case pageSlugPattern.MatchString(slug):
		return db.QualifiedPageSlug("", slug)
	default:
		return ""
	}
}
