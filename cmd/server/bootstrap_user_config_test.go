package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStartupBootstrapUserConfigFromEnvDefaultsToDisabled(t *testing.T) {
	t.Setenv("BOOTSTRAP_USER_EMAIL", "")
	t.Setenv("BOOTSTRAP_USER_NAME", "")
	t.Setenv("BOOTSTRAP_USER_PASSWORD", "")
	t.Setenv("BOOTSTRAP_USER_PASSWORD_FILE", "")
	t.Setenv("BOOTSTRAP_USER_ADMIN", "")

	config, err := loadStartupBootstrapUserConfigFromEnv()
	if err != nil {
		t.Fatalf("loadStartupBootstrapUserConfigFromEnv() error = %v", err)
	}
	if config.Enabled {
		t.Fatal("expected bootstrap config to be disabled when no environment variables are set")
	}
}

func TestLoadStartupBootstrapUserConfigFromEnvWithDirectPassword(t *testing.T) {
	t.Setenv("BOOTSTRAP_USER_EMAIL", " admin@example.com ")
	t.Setenv("BOOTSTRAP_USER_NAME", " Admin User ")
	t.Setenv("BOOTSTRAP_USER_PASSWORD", "secret123")
	t.Setenv("BOOTSTRAP_USER_PASSWORD_FILE", "")
	t.Setenv("BOOTSTRAP_USER_ADMIN", "")

	config, err := loadStartupBootstrapUserConfigFromEnv()
	if err != nil {
		t.Fatalf("loadStartupBootstrapUserConfigFromEnv() error = %v", err)
	}
	if !config.Enabled {
		t.Fatal("expected config to be enabled")
	}
	if config.Email != "admin@example.com" {
		t.Fatalf("config.Email = %q, want %q", config.Email, "admin@example.com")
	}
	if config.Name != "Admin User" {
		t.Fatalf("config.Name = %q, want %q", config.Name, "Admin User")
	}
	if config.Password != "secret123" {
		t.Fatalf("config.Password = %q, want %q", config.Password, "secret123")
	}
	if !config.GrantAdmin {
		t.Fatal("expected GrantAdmin to default to true")
	}
}

func TestLoadStartupBootstrapUserConfigFromEnvAllowsEmptyName(t *testing.T) {
	t.Setenv("BOOTSTRAP_USER_EMAIL", "admin@example.com")
	t.Setenv("BOOTSTRAP_USER_NAME", "")
	t.Setenv("BOOTSTRAP_USER_PASSWORD", "secret123")
	t.Setenv("BOOTSTRAP_USER_PASSWORD_FILE", "")
	t.Setenv("BOOTSTRAP_USER_ADMIN", "")

	config, err := loadStartupBootstrapUserConfigFromEnv()
	if err != nil {
		t.Fatalf("loadStartupBootstrapUserConfigFromEnv() error = %v", err)
	}
	if config.Name != "" {
		t.Fatalf("config.Name = %q, want empty", config.Name)
	}
}

func TestLoadStartupBootstrapUserConfigFromEnvWithPasswordFile(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "bootstrap-password.txt")
	if err := os.WriteFile(passwordFile, []byte("secret-from-file\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	t.Setenv("BOOTSTRAP_USER_EMAIL", "admin@example.com")
	t.Setenv("BOOTSTRAP_USER_NAME", "")
	t.Setenv("BOOTSTRAP_USER_PASSWORD", "")
	t.Setenv("BOOTSTRAP_USER_PASSWORD_FILE", passwordFile)
	t.Setenv("BOOTSTRAP_USER_ADMIN", "false")

	config, err := loadStartupBootstrapUserConfigFromEnv()
	if err != nil {
		t.Fatalf("loadStartupBootstrapUserConfigFromEnv() error = %v", err)
	}
	if config.Password != "secret-from-file" {
		t.Fatalf("config.Password = %q, want %q", config.Password, "secret-from-file")
	}
	if config.GrantAdmin {
		t.Fatal("expected GrantAdmin to be false")
	}
}

func TestLoadStartupBootstrapUserConfigFromEnvValidatesRequiredFields(t *testing.T) {
	t.Setenv("BOOTSTRAP_USER_EMAIL", "")
	t.Setenv("BOOTSTRAP_USER_NAME", "Admin")
	t.Setenv("BOOTSTRAP_USER_PASSWORD", "secret123")
	t.Setenv("BOOTSTRAP_USER_PASSWORD_FILE", "")
	t.Setenv("BOOTSTRAP_USER_ADMIN", "")

	if _, err := loadStartupBootstrapUserConfigFromEnv(); err == nil {
		t.Fatal("expected missing email to fail validation")
	}

	t.Setenv("BOOTSTRAP_USER_EMAIL", "admin@example.com")
	t.Setenv("BOOTSTRAP_USER_PASSWORD", "")
	if _, err := loadStartupBootstrapUserConfigFromEnv(); err == nil {
		t.Fatal("expected missing password to fail validation")
	}
}

func TestLoadStartupBootstrapUserConfigFromEnvRejectsPasswordConflicts(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "bootstrap-password.txt")
	if err := os.WriteFile(passwordFile, []byte("secret-from-file\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	t.Setenv("BOOTSTRAP_USER_EMAIL", "admin@example.com")
	t.Setenv("BOOTSTRAP_USER_NAME", "")
	t.Setenv("BOOTSTRAP_USER_PASSWORD", "secret123")
	t.Setenv("BOOTSTRAP_USER_PASSWORD_FILE", passwordFile)
	t.Setenv("BOOTSTRAP_USER_ADMIN", "")

	if _, err := loadStartupBootstrapUserConfigFromEnv(); err == nil {
		t.Fatal("expected password source conflict to fail validation")
	}
}

func TestLoadStartupBootstrapUserConfigFromEnvRejectsInvalidAdminValue(t *testing.T) {
	t.Setenv("BOOTSTRAP_USER_EMAIL", "admin@example.com")
	t.Setenv("BOOTSTRAP_USER_NAME", "")
	t.Setenv("BOOTSTRAP_USER_PASSWORD", "secret123")
	t.Setenv("BOOTSTRAP_USER_PASSWORD_FILE", "")
	t.Setenv("BOOTSTRAP_USER_ADMIN", "not-a-bool")

	if _, err := loadStartupBootstrapUserConfigFromEnv(); err == nil {
		t.Fatal("expected invalid BOOTSTRAP_USER_ADMIN to fail validation")
	}
}
