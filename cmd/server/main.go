package main

import (
	"log"
	"velm/internal/db"
	"velm/internal/push"

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
	// Web push misconfiguration fails silently at the only moment it matters:
	// the phone that should have buzzed. Say it out loud at boot instead, so a
	// 403 from the push service has a cause already in the logs.
	if push.Enabled() {
		if err := push.KeypairError(); err != nil {
			log.Printf("warn: web push is enabled but unusable: %v", err)
		}
	}
	startServer()
}
