package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowEphemeralSessionKeysDefaultsToDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("SESSION_ALLOW_EPHEMERAL_KEYS", "")

	if !allowEphemeralSessionKeys() {
		t.Fatal("expected ephemeral session keys to be allowed by default in development")
	}
}

func TestAllowEphemeralSessionKeysRejectsProductionByDefault(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SESSION_ALLOW_EPHEMERAL_KEYS", "")

	if allowEphemeralSessionKeys() {
		t.Fatal("expected ephemeral session keys to be disabled in production")
	}
}

func TestLoadSessionKeyFromEnvRequiresValueInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SESSION_ALLOW_EPHEMERAL_KEYS", "")
	t.Setenv("SESSION_AUTH_KEY", "")

	_, err := loadSessionKeyFromEnv("SESSION_AUTH_KEY")
	if err == nil {
		t.Fatal("expected an error when SESSION_AUTH_KEY is missing in production")
	}
	if !strings.Contains(err.Error(), "SESSION_AUTH_KEY is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadSessionKeyFromEnvAcceptsValidBase64Value(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SESSION_ALLOW_EPHEMERAL_KEYS", "")
	t.Setenv("SESSION_AUTH_KEY", base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")))

	key, err := loadSessionKeyFromEnv("SESSION_AUTH_KEY")
	if err != nil {
		t.Fatalf("loadSessionKeyFromEnv returned error: %v", err)
	}
	if len(key) != sessionKeyLength {
		t.Fatalf("key length = %d, want %d", len(key), sessionKeyLength)
	}
}

func TestLoadSessionKeyFromEnvPersistsGeneratedKeysInDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("SESSION_ALLOW_EPHEMERAL_KEYS", "")
	t.Setenv("SESSION_AUTH_KEY", "")
	t.Setenv("SESSION_ENCRYPTION_KEY", "")
	keyFile := filepath.Join(t.TempDir(), "session-keys.json")
	t.Setenv("SESSION_KEY_FILE", keyFile)

	authKey1, err := loadSessionKeyFromEnv("SESSION_AUTH_KEY")
	if err != nil {
		t.Fatalf("loadSessionKeyFromEnv auth returned error: %v", err)
	}
	authKey2, err := loadSessionKeyFromEnv("SESSION_AUTH_KEY")
	if err != nil {
		t.Fatalf("loadSessionKeyFromEnv auth second call returned error: %v", err)
	}
	if string(authKey1) != string(authKey2) {
		t.Fatal("expected persisted auth key to be stable across calls")
	}

	encryptionKey1, err := loadSessionKeyFromEnv("SESSION_ENCRYPTION_KEY")
	if err != nil {
		t.Fatalf("loadSessionKeyFromEnv encryption returned error: %v", err)
	}
	encryptionKey2, err := loadSessionKeyFromEnv("SESSION_ENCRYPTION_KEY")
	if err != nil {
		t.Fatalf("loadSessionKeyFromEnv encryption second call returned error: %v", err)
	}
	if string(encryptionKey1) != string(encryptionKey2) {
		t.Fatal("expected persisted encryption key to be stable across calls")
	}

	if _, err := os.Stat(keyFile); err != nil {
		t.Fatalf("expected persisted session key file to exist: %v", err)
	}
}

func TestLoadSessionKeyFilePathFromEnvDefaultsToDevFile(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("SESSION_KEY_FILE", "")

	if got := loadSessionKeyFilePathFromEnv(); got != defaultSessionKeyFile {
		t.Fatalf("loadSessionKeyFilePathFromEnv() = %q, want %q", got, defaultSessionKeyFile)
	}
}

func TestLoadSessionKeyFilePathFromEnvSkipsFileInTest(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("SESSION_KEY_FILE", "")

	if got := loadSessionKeyFilePathFromEnv(); got != "" {
		t.Fatalf("loadSessionKeyFilePathFromEnv() = %q, want empty", got)
	}
}

func TestLoadSessionMaxAgeSecondsFromEnvDefaultsAndValidates(t *testing.T) {
	t.Setenv("SESSION_MAX_AGE_SECONDS", "")

	value, err := loadSessionMaxAgeSecondsFromEnv()
	if err != nil {
		t.Fatalf("loadSessionMaxAgeSecondsFromEnv returned error: %v", err)
	}
	if value != defaultSessionMaxAgeSecond {
		t.Fatalf("default session max age = %d, want %d", value, defaultSessionMaxAgeSecond)
	}

	t.Setenv("SESSION_MAX_AGE_SECONDS", "7200")
	value, err = loadSessionMaxAgeSecondsFromEnv()
	if err != nil {
		t.Fatalf("loadSessionMaxAgeSecondsFromEnv returned error: %v", err)
	}
	if value != 7200 {
		t.Fatalf("session max age = %d, want 7200", value)
	}

	t.Setenv("SESSION_MAX_AGE_SECONDS", "0")
	if _, err := loadSessionMaxAgeSecondsFromEnv(); err == nil {
		t.Fatal("expected validation error for SESSION_MAX_AGE_SECONDS=0")
	}
}

func TestLoadSessionCookieSecureFromEnvValidatesBoolean(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("TLS_CERT_FILE", "")
	t.Setenv("TLS_KEY_FILE", "")
	t.Setenv("SESSION_COOKIE_SECURE", "true")
	enabled, err := loadSessionCookieSecureFromEnv()
	if err != nil {
		t.Fatalf("loadSessionCookieSecureFromEnv returned error: %v", err)
	}
	if !enabled {
		t.Fatal("expected secure cookie mode to be enabled")
	}

	t.Setenv("SESSION_COOKIE_SECURE", "not-a-bool")
	if _, err := loadSessionCookieSecureFromEnv(); err == nil {
		t.Fatal("expected validation error for invalid SESSION_COOKIE_SECURE")
	}
}
