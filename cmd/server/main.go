package main

import (
	"log"
	"velm/internal/db"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	if err := initSessionStore(); err != nil {
		log.Fatal("fatal: failed to initialize session store")
	}
	initTemplates()
	if err := initDatabase(); err != nil {
		log.Fatal("fatal: failed to initialize database")
	}
	defer db.CloseDB()
	startServer()
}
