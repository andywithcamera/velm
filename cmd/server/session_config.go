package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"velm/internal/auth"
)

const (
	sessionKeyLength           = 32
	defaultSessionMaxAgeSecond = 3600
	defaultSessionKeyFile      = ".session-keys.json"
)

type persistedSessionKeys struct {
	AuthKey       string `json:"auth_key"`
	EncryptionKey string `json:"encryption_key"`
}

func allowEphemeralSessionKeys() bool {
	if value := strings.TrimSpace(os.Getenv("SESSION_ALLOW_EPHEMERAL_KEYS")); value != "" {
		allowed, err := strconv.ParseBool(value)
		return err == nil && allowed
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) {
	case "", "dev", "development", "local", "test":
		return true
	default:
		return false
	}
}

func loadSessionKeyFromEnv(envVar string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(envVar))
	if raw == "" {
		if path := loadSessionKeyFilePathFromEnv(); path != "" {
			key, err := loadSessionKeyFromFile(path, envVar)
			if err != nil {
				return nil, err
			}
			log.Printf("info: %s is not set; using persisted key from %s", envVar, path)
			return key, nil
		}

		if !allowEphemeralSessionKeys() {
			appEnv := strings.TrimSpace(os.Getenv("APP_ENV"))
			if appEnv == "" {
				appEnv = "production"
			}
			return nil, fmt.Errorf("%s is required when APP_ENV=%q", envVar, appEnv)
		}

		log.Printf("warning: %s is not set; using ephemeral key", envVar)
		return auth.GenerateRandomKey(sessionKeyLength), nil
	}

	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be base64: %w", envVar, err)
	}
	if len(key) != sessionKeyLength {
		return nil, fmt.Errorf("%s must decode to exactly %d bytes", envVar, sessionKeyLength)
	}
	return key, nil
}

func loadSessionKeyFilePathFromEnv() string {
	if value := strings.TrimSpace(os.Getenv("SESSION_KEY_FILE")); value != "" {
		return value
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) {
	case "", "dev", "development", "local":
		return defaultSessionKeyFile
	default:
		return ""
	}
}

func loadSessionKeyFromFile(path, envVar string) ([]byte, error) {
	keys, err := loadOrCreatePersistedSessionKeys(path)
	if err != nil {
		return nil, err
	}

	var raw string
	switch envVar {
	case "SESSION_AUTH_KEY":
		raw = keys.AuthKey
	case "SESSION_ENCRYPTION_KEY":
		raw = keys.EncryptionKey
	default:
		return nil, fmt.Errorf("unsupported session key env var %q", envVar)
	}

	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s in %s must be base64: %w", envVar, path, err)
	}
	if len(key) != sessionKeyLength {
		return nil, fmt.Errorf("%s in %s must decode to exactly %d bytes", envVar, path, sessionKeyLength)
	}
	return key, nil
}

func loadOrCreatePersistedSessionKeys(path string) (persistedSessionKeys, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return persistedSessionKeys{}, fmt.Errorf("session key file path is required")
	}

	content, err := os.ReadFile(path)
	if err == nil {
		var keys persistedSessionKeys
		if err := json.Unmarshal(content, &keys); err != nil {
			return persistedSessionKeys{}, fmt.Errorf("parse session key file %s: %w", path, err)
		}
		return keys, nil
	}
	if !os.IsNotExist(err) {
		return persistedSessionKeys{}, fmt.Errorf("read session key file %s: %w", path, err)
	}

	keys := persistedSessionKeys{
		AuthKey:       base64.StdEncoding.EncodeToString(auth.GenerateRandomKey(sessionKeyLength)),
		EncryptionKey: base64.StdEncoding.EncodeToString(auth.GenerateRandomKey(sessionKeyLength)),
	}
	content, err = json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return persistedSessionKeys{}, fmt.Errorf("marshal session key file %s: %w", path, err)
	}
	content = append(content, '\n')

	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return persistedSessionKeys{}, fmt.Errorf("create session key file directory %s: %w", dir, err)
		}
	}

	if err := os.WriteFile(path, content, 0o600); err != nil {
		return persistedSessionKeys{}, fmt.Errorf("write session key file %s: %w", path, err)
	}
	return keys, nil
}

func loadSessionMaxAgeSecondsFromEnv() (int, error) {
	return loadPositiveIntEnv("SESSION_MAX_AGE_SECONDS", defaultSessionMaxAgeSecond)
}

func loadSessionCookieSecureFromEnv() (bool, error) {
	tlsEnabled, err := isTLSEnabledByConfig()
	if err != nil {
		return false, err
	}
	return loadBoolEnv("SESSION_COOKIE_SECURE", tlsEnabled || isProductionEnv())
}

func loadPositiveIntEnv(envVar string, defaultValue int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(envVar))
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive integer: %w", envVar, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", envVar)
	}
	return value, nil
}

func loadBoolEnv(envVar string, defaultValue bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(envVar))
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", envVar, err)
	}
	return value, nil
}
