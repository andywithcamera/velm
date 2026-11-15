package main

import (
	"context"
	"encoding/json"
	"strings"

	"velm/internal/db"
)

// stylePreferenceKey stores the active style pack in the per-user
// _user_preference table, alongside the colour theme and the ambient overlay.
//
// The two axes are deliberately separate: a theme sets colour, a style sets
// geometry and typography (radius, edge weight and style, font families). That
// split is what keeps the option count at themes + styles instead of
// themes x styles, and it is enforced by the token block in 10-core.css — no
// style pack may declare a colour, and no theme may declare a radius.
const stylePreferenceKey = "style"

// validStyles is the set of style pack identifiers. Each name must have a
// matching html[data-style="..."] block in 60-style-packs.css;
// TestEveryStyleHasAPack guards the pair.
var validStyles = map[string]bool{
	"modern":   true,
	"terminal": true,
	"eightbit": true,
	"soft":     true,
}

const defaultStyle = "modern"

// resolveUserStyle reads the persisted style pack for the user, returning the
// raw value (with quotes stripped) or "" when absent.
func resolveUserStyle(ctx context.Context, userID string) string {
	raw, err := db.GetUserPreference(ctx, userID, themePreferenceNamespace, stylePreferenceKey)
	if err != nil || raw == nil {
		return ""
	}
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	return trimmed
}

// normalizeStyle returns a known style identifier, falling back to the default.
func normalizeStyle(style string) string {
	if style != "" && validStyles[style] {
		return style
	}
	return defaultStyle
}

// activeUserStyle returns the normalized style a request should render with.
func activeUserStyle(ctx context.Context, userID string) string {
	if userID == "" {
		return defaultStyle
	}
	return normalizeStyle(resolveUserStyle(ctx, userID))
}

// packStylePreference serializes a style value into JSONB for storage.
func packStylePreference(style string) ([]byte, error) {
	return json.Marshal(normalizeStyle(style))
}
