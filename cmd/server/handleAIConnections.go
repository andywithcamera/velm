package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/security"
)

var aiKeyFingerprintRe = regexp.MustCompile(`^(.{0,6})`)

// handleAIConnections is the admin CRUD + model-sync API for AI connections.
// Admin-only (PermissionAdmin) for P1 per the 2026-09-11 decision; the UI
// will live under the admin area.
func handleAIConnections(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch {
	case r.Method == http.MethodGet && r.PathValue("id") == "":
		handleListAIConnections(w, r)
	case r.Method == http.MethodPost && r.PathValue("id") == "":
		handleCreateAIConnection(w, r)
	case r.Method == http.MethodGet:
		handleGetAIConnection(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/models/sync"):
		handleSyncAIConnectionModels(w, r)
	case r.Method == http.MethodPatch:
		handleUpdateAIConnection(w, r)
	case r.Method == http.MethodDelete:
		handleDeleteAIConnection(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func handleListAIConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := db.ListAIConnections(r.Context())
	if err != nil {
		http.Error(w, "Failed to list connections", http.StatusInternalServerError)
		return
	}
	providers, err := db.ListAIProviders(r.Context())
	if err != nil {
		http.Error(w, "Failed to list providers", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": conns, "providers": providers})
}

type aiConnectionInput struct {
	ProviderID string `json:"provider_id"`
	Label      string `json:"label"`
	APIKey     string `json:"api_key"`
}

var errInvalidInput = errors.New("invalid input")

func parseAIConnectionInput(r *http.Request) (*aiConnectionInput, error) {
	var in aiConnectionInput
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10)).Decode(&in); err != nil {
		return nil, errInvalidInput
	}
	if in.ProviderID == "" || in.APIKey == "" {
		return nil, errInvalidInput
	}
	return &in, nil
}

func fingerprintKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:6] + "…" + key[len(key)-4:]
}

func handleCreateAIConnection(w http.ResponseWriter, r *http.Request) {
	in, err := parseAIConnectionInput(r)
	if err != nil {
		http.Error(w, "provider_id and api_key are required", http.StatusBadRequest)
		return
	}
	cipher, err := security.NewMFASecretCipherFromEnv()
	if err != nil {
		http.Error(w, "Secret cipher unavailable", http.StatusInternalServerError)
		return
	}
	enc, err := cipher.Encrypt(in.APIKey)
	if err != nil {
		http.Error(w, "Failed to secure key", http.StatusInternalServerError)
		return
	}
	conn, err := db.CreateAIConnection(r.Context(), in.ProviderID, in.Label, enc, fingerprintKey(in.APIKey))
	if err != nil {
		http.Error(w, "Failed to create connection", http.StatusInternalServerError)
		return
	}
	// Per the #93 decision: models populate from a live provider call, so run
	// the sync as part of creation. Report sync failure but keep the connection.
	synced, syncErr := syncConnectionModels(r, conn.ID)
	if syncErr != nil {
		writeJSON(w, http.StatusCreated, map[string]any{"connection": conn, "sync_error": syncErr.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"connection": conn, "models": synced})
}

func handleGetAIConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := db.GetAIConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, conn)
}

func handleUpdateAIConnection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label  *string `json:"label"`
		APIKey string  `json:"api_key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10)).Decode(&in); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}
	var encKey, fingerprint *string
	if in.APIKey != "" {
		cipher, err := security.NewMFASecretCipherFromEnv()
		if err != nil {
			http.Error(w, "Secret cipher unavailable", http.StatusInternalServerError)
			return
		}
		enc, err := cipher.Encrypt(in.APIKey)
		if err != nil {
			http.Error(w, "Failed to secure key", http.StatusInternalServerError)
			return
		}
		encKey, fingerprint = &enc, fp(fingerprintKey(in.APIKey))
	}
	if err := db.UpdateAIConnection(r.Context(), r.PathValue("id"), in.Label, encKey, fingerprint); err != nil {
		http.Error(w, "Failed to update connection", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func fp(s string) *string { return &s }

func handleDeleteAIConnection(w http.ResponseWriter, r *http.Request) {
	if err := db.DeleteAIConnection(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, "Failed to delete connection", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// syncConnectionModels decrypts the stored key, calls the provider's
// list-models endpoint, and rewrites the connection's _ai_model rows.
func syncConnectionModels(r *http.Request, id string) ([]db.AIModel, error) {
	conn, err := db.GetAIConnection(r.Context(), id)
	if err != nil {
		return nil, err
	}
	provider, err := db.GetAIProvider(r.Context(), conn.ProviderID)
	if err != nil {
		return nil, err
	}
	enc, err := db.GetAIConnectionKey(r.Context(), id)
	if err != nil {
		return nil, err
	}
	cipher, err := security.NewMFASecretCipherFromEnv()
	if err != nil {
		return nil, err
	}
	plaintext, err := cipher.Decrypt(enc)
	if err != nil {
		return nil, err
	}
	models, err := security.NewProviderClient().ListModels(provider.BaseURL, provider.DefaultListModelsPath, plaintext, provider.ProviderMetadata)
	if err != nil {
		return nil, err
	}
	if err := db.MarkAIConnectionVerified(r.Context(), id); err != nil {
		return nil, err
	}
	rows := make([]db.AIModel, 0, len(models))
	for _, m := range models {
		rows = append(rows, db.AIModel{ConnectionID: id, ModelID: m.ModelID, DisplayName: m.DisplayName, ModelMetadata: m.Metadata, IsActive: true})
	}
	if err := db.SyncAIModels(r.Context(), id, rows); err != nil {
		return nil, err
	}
	return db.ListAIModelsByConnection(r.Context(), id)
}

func handleSyncAIConnectionModels(w http.ResponseWriter, r *http.Request) {
	synced, err := syncConnectionModels(r, r.PathValue("id"))
	if err != nil {
		http.Error(w, "Sync failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": synced})
}
