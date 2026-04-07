package db

import "strings"

var preferredDisplayFieldNames = []string{
	"name",
	"title",
	"number",
	"email",
	"label",
	"key",
	"slug",
}

func NormalizeDisplayFieldName(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func InferDisplayFieldName(names []string) string {
	available := make(map[string]bool, len(names))
	for _, name := range names {
		normalized := NormalizeDisplayFieldName(name)
		if normalized == "" {
			continue
		}
		available[normalized] = true
	}

	for _, candidate := range preferredDisplayFieldNames {
		if available[candidate] {
			return candidate
		}
	}
	return ""
}
