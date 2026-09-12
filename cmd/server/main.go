package main

import (
	"log"
	"velm/internal/db"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	if err := initSessionStore(); err != nil {
		log.Fatalf("fatal: failed to initialize session store: %v", redactSecrets(err.Error()))
	}
	initTemplates()
	if err := initDatabase(); err != nil {
		log.Fatalf("fatal: failed to initialize database: %v", redactSecrets(err.Error()))
	}
	defer db.CloseDB()
	startServer()
}
