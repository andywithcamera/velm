package main

import (
	"context"
	"log"

	"velm/internal/db"
)

// Reports public service methods and enabled endpoints that declare no roles.
// The runtime denies these; the report is the list to fix before or after
// deploying the fail-closed authorisation change.
func main() {
	if err := db.ConnectToDB(); err != nil {
		log.Fatal(err)
	}
	defer db.CloseDB()

	findings, err := db.ListUngatedAppEntryPoints(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	if len(findings) == 0 {
		log.Println("no ungated app entry points")
		return
	}
	for _, finding := range findings {
		log.Printf("%s", finding)
	}
	log.Printf("%d ungated entry point(s); give each one explicit roles, using [\"%s\"] for any signed-in user", len(findings), db.AuthenticatedRole)
}
