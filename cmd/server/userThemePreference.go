package main

import (
	"context"
	"encoding/json"
	"strings"

	"velm/internal/db"
)

// themePreferenceNamespace / themePreferenceKey are where the active theme is
// stored in the per-user _user_preference table (R4: server-side persistence,
// no localStorage).
const (
	themePreferenceNamespace = "ui"
	themePreferenceKey       = "theme"
)

// validThemes is the set of theme identifiers the registry accepts. Unknown or
// blank values fall back to the default ("light").
var validThemes = map[string]bool{
	"light":       true,
	"dark":        true,
	"nord":        true,
	"tokyo-night": true,
	"catppuccin":  true,
}

const defaultTheme = "light"

// resolveUserTheme reads the persisted theme for the user, returning the raw
// value (with quotes stripped) or "" when absent/unknown.
func resolveUserTheme(ctx context.Context, userID string) string {
	raw, err := db.GetUserPreference(ctx, userID, themePreferenceNamespace, themePreferenceKey)
	if err != nil || raw == nil {
		return ""
	}
	// Stored as a JSON string value, e.g. "tokyo-night".
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	return trimmed
}

// normalizeTheme returns a known theme identifier, falling back to the default.
func normalizeTheme(theme string) string {
	if theme != "" && validThemes[theme] {
		return theme
	}
	return defaultTheme
}

// activeUserTheme returns the normalized theme a request should render with.
func activeUserTheme(ctx context.Context, userID string) string {
	if userID == "" {
		return defaultTheme
	}
	return normalizeTheme(resolveUserTheme(ctx, userID))
}

// packThemePreference serializes a theme value into JSONB for storage.
func packThemePreference(theme string) ([]byte, error) {
	return json.Marshal(normalizeTheme(theme))
}
