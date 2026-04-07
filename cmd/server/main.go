package main

import (
	"log"
	"velm/internal/db"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	if err := initSessionStore(); err != nil {
		log.Fatal(err)
	}
	initTemplates()
	if err := initDatabase(); err != nil {
		log.Fatal(err)
	}
	defer db.CloseDB()
	startServer()
}
