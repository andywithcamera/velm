package main

import (
	"context"

	"velm/internal/db"
	"velm/internal/security"
)

// Register the cipher bridge and model-list bridge so db can translate the
// virtual api_key form field into encrypted storage and re-sync provider
// models after a generic-form save, without db importing security.
func init() {
	db.SetSecretCipherBridge(&db.SecretCipherBridge{
		EncryptFingerprint: func(plaintext string) (string, string, error) {
			cipher, err := security.NewMFASecretCipherFromEnv()
			if err != nil {
				return "", "", err
			}
			enc, err := cipher.Encrypt(plaintext)
			if err != nil {
				return "", "", err
			}
			return enc, fingerprintKey(plaintext), nil
		},
		Decrypt: func(enc string) (string, error) {
			cipher, err := security.NewMFASecretCipherFromEnv()
			if err != nil {
				return "", err
			}
			return cipher.Decrypt(enc)
		},
	})

	db.ListProviderModelsForSync = func(connectionID, baseURL, listPath, plaintextKey string, meta map[string]any) ([]db.AIModel, error) {
		models, err := security.NewProviderClient().ListModels(baseURL, listPath, plaintextKey, meta)
		if err != nil {
			return nil, err
		}
		rows := make([]db.AIModel, 0, len(models))
		for _, m := range models {
			rows = append(rows, db.AIModel{
				ConnectionID:  connectionID,
				ModelID:       m.ModelID,
				DisplayName:   m.DisplayName,
				ModelMetadata: m.Metadata,
				IsActive:      true,
			})
		}
		return rows, nil
	}

	db.MarkVerifiedAfterSync = func(ctx context.Context, id string) error {
		return db.MarkAIConnectionVerified(ctx, id)
	}
}
