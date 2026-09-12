package db

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// SecretCipherBridge lets cmd/server inject the package-level secret cipher
// without db importing security (which would create an import cycle:
// security already imports db for audit middleware).
type SecretCipherBridge struct {
	EncryptFingerprint func(plaintext string) (enc string, fingerprint string, err error)
	Decrypt            func(enc string) (plaintext string, err error)
}

var secretCipherBridge *SecretCipherBridge

// SetSecretCipherBridge registers the bridge. Called once from cmd/server
// during startup.
func SetSecretCipherBridge(bridge *SecretCipherBridge) {
	secretCipherBridge = bridge
}

// applyBuiltinRecordTransforms consumes write-only virtual form fields before
// the record is persisted. Virtual columns (see builtinVirtualColumns) have no
// backing DB column, so their values must be translated into real columns here.
// Runs before filterSubmittedColumns so the virtual key is stripped from the
// write set and the encrypted form is added in its place.
func applyBuiltinRecordTransforms(tableName string, formData map[string]string, nullColumns map[string]bool) {
	switch strings.ToLower(strings.TrimSpace(tableName)) {
	case "_ai_connection":
		plaintext := strings.TrimSpace(formData["api_key"])
		delete(formData, "api_key")
		delete(nullColumns, "api_key")
		if plaintext == "" {
			return
		}
		if secretCipherBridge == nil || secretCipherBridge.EncryptFingerprint == nil {
			log.Printf("ai connection save: secret cipher bridge not registered, key not updated")
			return
		}
		enc, fingerprint, err := secretCipherBridge.EncryptFingerprint(plaintext)
		if err != nil {
			log.Printf("ai connection save: failed to encrypt key, key not updated: %v", err)
			return
		}
		formData["api_key_enc"] = enc
		formData["api_key_fingerprint"] = fingerprint
	}
}

// syncConnectionAfterSave re-syncs models for an AI connection after a generic
// form save. Best-effort: a provider failure must not fail the save.
func syncConnectionAfterSave(ctx context.Context, connectionID string) error {
	conn, err := GetAIConnection(ctx, connectionID)
	if err != nil || conn == nil {
		return err
	}
	provider, err := GetAIProvider(ctx, conn.ProviderID)
	if err != nil {
		return err
	}
	enc, err := GetAIConnectionKey(ctx, connectionID)
	if err != nil {
		return err
	}
	if secretCipherBridge == nil || secretCipherBridge.Decrypt == nil {
		return fmt.Errorf("secret cipher bridge not registered")
	}
	plaintext, err := secretCipherBridge.Decrypt(enc)
	if err != nil {
		return fmt.Errorf("decrypt key: %w", err)
	}
	rows, err := ListProviderModelsForSync(connectionID, provider.BaseURL, provider.DefaultListModelsPath, plaintext, provider.ProviderMetadata)
	if err != nil {
		return fmt.Errorf("list models: %w", err)
	}
	if err := SyncAIModels(ctx, connectionID, rows); err != nil {
		return err
	}
	if MarkVerifiedAfterSync != nil {
		return MarkVerifiedAfterSync(ctx, connectionID)
	}
	return nil
}

// dbPackage is a package-level function-variable indirection so the model-list
// fetch can live in cmd/server (which already imports security) without db
// importing security.
var (
	// ListProviderModelsForSync is injected by cmd/server; it wraps the
	// security.ProviderClient without db importing security.
	ListProviderModelsForSync func(connectionID, baseURL, listPath, plaintextKey string, meta map[string]any) ([]AIModel, error)
	// MarkVerifiedAfterSync flags a connection as verified post-sync.
	MarkVerifiedAfterSync func(ctx context.Context, id string) error
)
