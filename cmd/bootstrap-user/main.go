package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"velm/internal/db"
	"strings"
)

func main() {
	email := flag.String("email", "", "Email address of the user to create or update")
	name := flag.String("name", "", "Display name for the user (defaults from the email local-part)")
	password := flag.String("password", "", "Password for the user")
	passwordStdin := flag.Bool("password-stdin", false, "Read the password from stdin")
	admin := flag.Bool("admin", true, "Grant the global admin role")
	flag.Parse()

	if strings.TrimSpace(*email) == "" {
		log.Fatal("missing required -email")
	}

	resolvedPassword, err := resolveBootstrapPassword(*password, *passwordStdin)
	if err != nil {
		log.Fatal(err)
	}

	if err := db.ConnectToDB(); err != nil {
		log.Fatal("connect db:", err)
	}
	defer db.CloseDB()

	if err := db.RunMigrations(context.Background()); err != nil {
		log.Fatal("run migrations:", err)
	}

	result, err := db.BootstrapUser(context.Background(), db.BootstrapUserInput{
		Email:      *email,
		Name:       *name,
		Password:   resolvedPassword,
		GrantAdmin: *admin,
	})
	if err != nil {
		log.Fatal("bootstrap user:", err)
	}

	action := "updated"
	if result.Created {
		action = "created"
	}
	roleNote := "without admin role"
	if result.GrantedAdmin {
		roleNote = "with admin role"
	}
	log.Printf("bootstrap user complete: %s %s (%s)", action, result.Email, roleNote)
}

func resolveBootstrapPassword(password string, passwordStdin bool) (string, error) {
	if passwordStdin {
		if password != "" {
			return "", fmt.Errorf("use either -password or -password-stdin, not both")
		}
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		password = strings.TrimRight(string(raw), "\r\n")
	}

	if password == "" {
		return "", fmt.Errorf("missing required password: provide -password or -password-stdin")
	}
	return password, nil
}
