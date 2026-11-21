package main

import (
	"context"
	"strings"

	"velm/internal/db"
)

// loadMenuFromDB builds the main menu. Set 2026-11-20: four fixed doors, not
// one entry per entity table — Catalogue (order work), Task Studio (author the
// processes), Entities (the data model behind both) and Docs (the knowledge
// library). Everything that used to be a top-level item is reachable from
// those, so the menu no longer grows with the schema.
func loadMenuFromDB() error {
	ctx := context.Background()
	menuItems = nil

	menuItems = append(menuItems,
		MenuItem{Title: "Catalogue", Href: "/task-catalogue", Order: 10},
		MenuItem{Title: "Task Studio", Href: "/task-definitions", Order: 20},
	)

	// Entities is the base_entity table view — the same /t/<table> page every
	// entity gets, not the schema builder. Gate on the table existing so a
	// minimal instance still gets the two doors above.
	if _, ok, err := db.GetPhysicalTable(ctx, "base_entity"); err == nil && ok {
		menuItems = append(menuItems, MenuItem{Title: "Entities", Href: "/t/base_entity", Order: 30})
	}

	// Docs keeps its old gate: only when the library table is installed.
	if _, ok, err := db.GetPhysicalTable(ctx, "_docs_library"); err == nil && ok {
		menuItems = append(menuItems, MenuItem{Title: "Docs", Href: "/docs", Order: 40})
	}

	return nil
}

func humanizeMenuName(input string) string {
	input = strings.TrimSpace(strings.TrimPrefix(input, "_"))
	if input == "" {
		return ""
	}
	parts := strings.Split(input, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}
