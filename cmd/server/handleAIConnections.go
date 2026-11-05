package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/security"
)

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

// errConnectionDestinationChanged means the provider endpoint moved after the
// key was entered. The key is withheld until an admin re-enters it, which is
// the act that authorises the new destination.
var errConnectionDestinationChanged = errors.New("connection destination changed")

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
	provider, err := db.GetAIProvider(r.Context(), in.ProviderID)
	if err != nil {
		http.Error(w, "Unknown provider", http.StatusBadRequest)
		return
	}
	// Refuse the destination before encrypting or storing anything: an admin
	// who points base_url somewhere unacceptable finds out here, and no key is
	// ever written alongside an endpoint that cannot be used.
	if err := security.ValidateProviderDestination(r.Context(), provider.BaseURL); err != nil {
		http.Error(w, syncFailureMessage(err), http.StatusBadRequest)
		return
	}
	cipher, err := security.NewSecretCipherFromEnv()
	if err != nil {
		http.Error(w, "Secret cipher unavailable", http.StatusInternalServerError)
		return
	}
	enc, err := cipher.Encrypt(in.APIKey)
	if err != nil {
		http.Error(w, "Failed to secure key", http.StatusInternalServerError)
		return
	}
	conn, err := db.CreateAIConnection(r.Context(), in.ProviderID, in.Label, enc, fingerprintKey(in.APIKey), provider.BaseURL)
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
	var encKey, fingerprint, pinned *string
	if in.APIKey != "" {
		// Entering a key is what authorises a destination. Read the provider's
		// endpoint now, validate it, and pin it alongside the new key: without
		// this an admin could move base_url and then re-key, and the old key
		// would quietly follow the provider to a new host.
		conn, err := db.GetAIConnection(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		provider, err := db.GetAIProvider(r.Context(), conn.ProviderID)
		if err != nil {
			http.Error(w, "Unknown provider", http.StatusBadRequest)
			return
		}
		if err := security.ValidateProviderDestination(r.Context(), provider.BaseURL); err != nil {
			http.Error(w, syncFailureMessage(err), http.StatusBadRequest)
			return
		}
		cipher, err := security.NewSecretCipherFromEnv()
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
		pinned = fp(provider.BaseURL)
	}
	if err := db.UpdateAIConnection(r.Context(), r.PathValue("id"), in.Label, encKey, fingerprint, pinned); err != nil {
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

// syncFailureMessage renders a sync failure for an admin without turning it
// into a network probe. A refused destination is worth naming -- it is the
// admin's own configuration and the fix is on their side. Anything that
// reached the network reports only that it failed: dial timeouts, TLS
// refusals, DNS failures and reset-by-peer are otherwise distinguishable, and
// that difference is what makes the endpoint a usable port scanner.
func syncFailureMessage(err error) string {
	switch {
	case errors.Is(err, security.ErrProviderDestination):
		return "provider URL refused: " + err.Error()
	case errors.Is(err, errConnectionDestinationChanged):
		return "the provider endpoint changed since this key was entered; re-enter the key to authorise the new destination"
	case errors.Is(err, security.ErrProviderRequest):
		return "could not reach the provider; check the base URL and network"
	default:
		return "sync failed"
	}
}

// resolveSyncDestination decides where a connection's key may be sent. The key
// travels only to the destination it was entered for: a provider row edited
// after the key was stored must not move an existing credential to a different
// host, because that edit is precisely how one admin would redirect another
// admin's key to a server they control. Re-entering the key is the act that
// authorises a new destination, and it re-pins in the same write.
func resolveSyncDestination(conn *db.AIConnection, provider *db.AIProvider) (string, error) {
	if conn == nil || provider == nil {
		return "", errConnectionDestinationChanged
	}
	if conn.PinnedBaseURL == "" || conn.PinnedBaseURL != provider.BaseURL {
		return "", errConnectionDestinationChanged
	}
	return conn.PinnedBaseURL, nil
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
	destination, err := resolveSyncDestination(conn, provider)
	if err != nil {
		return nil, err
	}
	enc, err := db.GetAIConnectionKey(r.Context(), id)
	if err != nil {
		return nil, err
	}
	cipher, err := security.NewSecretCipherFromEnv()
	if err != nil {
		return nil, err
	}
	plaintext, err := cipher.Decrypt(enc)
	if err != nil {
		return nil, err
	}
	models, err := security.NewProviderClient().ListModelsContext(r.Context(), destination, provider.DefaultListModelsPath, plaintext, provider.ProviderMetadata)
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
		http.Error(w, "Sync failed: "+syncFailureMessage(err), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": synced})
}
