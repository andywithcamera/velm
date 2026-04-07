package main

import (
	"context"
	"sort"
	"strings"

	"velm/internal/db"
)

// loadMenuFromDB preserves the existing bootstrap hook, but the menu is now
// derived from active app definitions and known platform routes instead of _menu.
func loadMenuFromDB() error {
	ctx := context.Background()
	menuItems = nil

	tables, err := db.ListBuilderTables(ctx)
	if err != nil {
		return err
	}

	nextOrder := 10
	seen := map[string]bool{}
	appendItem := func(title, href string) {
		title = strings.TrimSpace(title)
		href = strings.TrimSpace(href)
		if title == "" || href == "" || seen[href] {
			return
		}
		seen[href] = true
		menuItems = append(menuItems, MenuItem{
			Title: title,
			Href:  href,
			Order: nextOrder,
		})
		nextOrder += 10
	}

	for _, table := range tables {
		title := strings.TrimSpace(table.LabelPlural)
		if title == "" {
			title = strings.TrimSpace(table.LabelSingular)
		}
		if title == "" {
			title = humanizeMenuName(table.Name)
		}
		appendItem(title, "/t/"+table.Name)
	}

	if _, ok, err := db.GetPhysicalTable(ctx, "_docs_library"); err == nil && ok {
		appendItem("Docs", "/docs")
	}

	sort.Slice(menuItems, func(i, j int) bool {
		if menuItems[i].Order == menuItems[j].Order {
			return menuItems[i].Title < menuItems[j].Title
		}
		return menuItems[i].Order < menuItems[j].Order
	})

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
