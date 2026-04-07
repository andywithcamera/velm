package main

import (
	"context"
	"log"
	"velm/internal/db"
)

func main() {
	if err := db.ConnectToDB(); err != nil {
		log.Fatal(err)
	}
	defer db.CloseDB()

	if err := db.RunMigrations(context.Background()); err != nil {
		log.Fatal(err)
	}

	log.Println("migrations applied successfully")
}
