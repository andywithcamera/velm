package main

import (
	"context"
	"fmt"
	"velm/internal/db"
)

// loadPropertiesFromDB loads properties from the database.
func loadPropertiesFromDB() error {
	propertyItems = make(map[string]any)

	rows, err := db.Pool.Query(context.Background(), "SELECT key, value FROM _property")
	if err != nil {
		return fmt.Errorf("unable to query _properties: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("unable to scan property row: %v", err)
		}
		propertyItems[key] = value
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error reading property rows: %v", err)
	}

	return nil
}
