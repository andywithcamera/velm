package main

import (
	"context"
	"flag"
	"log"
	"strings"
	"velm/internal/auth"
	"velm/internal/db"
)

func main() {
	userID := flag.String("user", "", "Platform user_id (_id::text) to bind the token to (an agent's real identity)")
	label := flag.String("label", "", "Human label for the token (e.g. 'github-webhook')")
	scopes := flag.String("scopes", "", "Pipe-separated scopes, e.g. base_task:write|platform:read. Empty = all permissions the identity has.")
	flag.Parse()

	if strings.TrimSpace(*userID) == "" {
		log.Fatal("missing required -user")
	}

	if err := db.ConnectToDB(); err != nil {
		log.Fatal("connect db:", err)
	}
	defer db.CloseDB()

	if err := db.RunMigrations(context.Background()); err != nil {
		log.Fatal("run migrations:", err)
	}

	raw, err := auth.NewAgentToken()
	if err != nil {
		log.Fatal("generate token:", err)
	}

	store := db.NewAgentTokenStore()
	if err := auth.IssueAgentToken(context.Background(), store, *userID, *label, raw, split(*scopes)); err != nil {
		log.Fatal("issue token:", err)
	}

	log.Printf("token issued (user=%s label=%q scopes=%q)", *userID, *label, *scopes)
	log.Printf("SHOW THE RAW TOKEN ONCE ONLY:\n%s", raw)
}

func split(scopes string) []string {
	scopes = strings.TrimSpace(scopes)
	if scopes == "" {
		return nil
	}
	parts := strings.Split(scopes, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
