package main

import (
	"context"
	"flag"
	"log"
	"velm/internal/db"
)

func main() {
	email := flag.String("email", "", "Email address of the user to grant global admin")
	flag.Parse()

	if *email == "" {
		log.Fatal("missing required -email")
	}

	if err := db.ConnectToDB(); err != nil {
		log.Fatal("connect db:", err)
	}
	defer db.CloseDB()

	if err := db.RunMigrations(context.Background()); err != nil {
		log.Fatal("run migrations:", err)
	}

	if err := db.BootstrapAdminByEmail(context.Background(), *email); err != nil {
		log.Fatal("bootstrap admin:", err)
	}

	log.Printf("bootstrap admin complete for %s", *email)
}
