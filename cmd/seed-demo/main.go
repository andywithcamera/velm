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
	if err := db.RunDemoSeeds(context.Background()); err != nil {
		log.Fatal(err)
	}

	log.Println("demo seeds applied successfully")
}
