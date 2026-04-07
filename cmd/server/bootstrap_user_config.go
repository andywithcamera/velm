package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"velm/internal/db"
)

type startupBootstrapUserConfig struct {
	Enabled    bool
	Email      string
	Name       string
	Password   string
	GrantAdmin bool
}

func bootstrapInitialUserIfConfigured(ctx context.Context) error {
	config, err := loadStartupBootstrapUserConfigFromEnv()
	if err != nil {
		return err
	}
	if !config.Enabled {
		hasUsers, err := startupUsersExist(ctx)
		if err != nil {
			return err
		}
		if isProductionEnv() && !hasUsers {
			return fmt.Errorf("no users exist and bootstrap user is not configured; set BOOTSTRAP_USER_EMAIL and BOOTSTRAP_USER_PASSWORD or BOOTSTRAP_USER_PASSWORD_FILE before first production startup")
		}
		if !hasUsers {
			log.Printf("info: bootstrap user is not configured; create the first user with BOOTSTRAP_USER_EMAIL and BOOTSTRAP_USER_PASSWORD or by running the bootstrap-user helper")
		}
		return nil
	}

	result, bootstrapped, err := db.BootstrapFirstUser(ctx, db.BootstrapUserInput{
		Email:      config.Email,
		Name:       config.Name,
		Password:   config.Password,
		GrantAdmin: config.GrantAdmin,
	})
	if err != nil {
		return err
	}
	if !bootstrapped {
		log.Printf("info: bootstrap user config detected; skipping automatic bootstrap because users already exist")
		return nil
	}

	roleNote := "without admin role"
	if result.GrantedAdmin {
		roleNote = "with admin role"
	}
	log.Printf("info: bootstrap user complete on first startup: created %s (%s)", result.Email, roleNote)
	return nil
}

func loadStartupBootstrapUserConfigFromEnv() (startupBootstrapUserConfig, error) {
	email := strings.TrimSpace(os.Getenv("BOOTSTRAP_USER_EMAIL"))
	name := strings.TrimSpace(os.Getenv("BOOTSTRAP_USER_NAME"))
	password := os.Getenv("BOOTSTRAP_USER_PASSWORD")
	passwordFile := strings.TrimSpace(os.Getenv("BOOTSTRAP_USER_PASSWORD_FILE"))

	enabled := email != "" || name != "" || password != "" || passwordFile != ""
	if !enabled {
		return startupBootstrapUserConfig{}, nil
	}
	if email == "" {
		return startupBootstrapUserConfig{}, fmt.Errorf("BOOTSTRAP_USER_EMAIL is required when bootstrap user config is enabled")
	}
	if password != "" && passwordFile != "" {
		return startupBootstrapUserConfig{}, fmt.Errorf("use either BOOTSTRAP_USER_PASSWORD or BOOTSTRAP_USER_PASSWORD_FILE, not both")
	}
	if passwordFile != "" {
		resolvedPassword, err := loadBootstrapUserPasswordFromFile(passwordFile)
		if err != nil {
			return startupBootstrapUserConfig{}, err
		}
		password = resolvedPassword
	}
	if password == "" {
		return startupBootstrapUserConfig{}, fmt.Errorf("BOOTSTRAP_USER_PASSWORD or BOOTSTRAP_USER_PASSWORD_FILE is required when BOOTSTRAP_USER_EMAIL is set")
	}

	grantAdmin, err := loadBoolEnv("BOOTSTRAP_USER_ADMIN", true)
	if err != nil {
		return startupBootstrapUserConfig{}, err
	}

	return startupBootstrapUserConfig{
		Enabled:    true,
		Email:      email,
		Name:       name,
		Password:   password,
		GrantAdmin: grantAdmin,
	}, nil
}

func startupUsersExist(ctx context.Context) (bool, error) {
	var exists bool
	if err := db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM _user LIMIT 1)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check for existing users: %w", err)
	}
	return exists, nil
}

func loadBootstrapUserPasswordFromFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read BOOTSTRAP_USER_PASSWORD_FILE %s: %w", path, err)
	}
	password := strings.TrimRight(string(content), "\r\n")
	if password == "" {
		return "", fmt.Errorf("BOOTSTRAP_USER_PASSWORD_FILE %s is empty", path)
	}
	return password, nil
}
