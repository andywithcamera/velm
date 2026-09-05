package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// Agent tokens (issue #68): agents are users without a body. AuthN differs
// (scoped bearer token vs session cookie), authZ is identical — both resolve to
// the same request context read by UserIDFromRequest / RoleFromContext.

// TokenStore is the storage boundary for agent tokens. Implemented by
// internal/db so the auth package stays DB-agnostic and unit-testable.
type TokenStore interface {
	CreateAgentToken(ctx context.Context, userID, tokenHash, label, scopes string) error
	LookupAgentToken(ctx context.Context, tokenHash string) (AgentToken, error)
	TouchAgentToken(ctx context.Context, tokenID string) error
}

// AgentToken is the stored (hashed) representation of an issued token.
type AgentToken struct {
	ID      string
	UserID  string
	Label   string
	Scopes  []string
	Revoked bool
	Expires bool
}

// AgentScope is the in-context representation of an authenticated agent.
type AgentScope struct {
	UserID string
	Scopes []string
}

var (
	ErrNoAgentToken = errors.New("no agent token matches")
	ErrAgentRevoked = errors.New("agent token is revoked")
	ErrAgentExpired = errors.New("agent token has expired")
	ErrAgentInvalid = errors.New("invalid agent token")
)

const agentTokenPrefix = "velm_"
const agentTokenBytes = 32 // -> 64 hex chars

// NewAgentToken generates a raw secret. The returned value is the ONLY time the
// raw token exists; only its SHA-256 hash is persisted. Marker "velm_" makes
// leaked tokens greppable / classifiable as synthetic not user-chosen.
func NewAgentToken() (string, error) {
	buf := make([]byte, agentTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return agentTokenPrefix + hex.EncodeToString(buf), nil
}

// HashAgentToken derives the storage key from a raw secret.
func HashAgentToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ParseBearer extracts the raw token from an Authorization header value.
// Returns "" when the header is absent or not a Bearer credential.
func ParseBearer(authzHeader string) string {
	const prefix = "Bearer "
	if len(authzHeader) > len(prefix) && strings.EqualFold(authzHeader[:len(prefix)], prefix) {
		return strings.TrimSpace(authzHeader[len(prefix):])
	}
	return ""
}

// IsBearerRequest reports whether the request presents a Bearer credential.
// Used by CSRF and rate-opacity layers to skip browser-only protections for
// machine/agent clients (a scoped bearer token is the machine-audited
// anti-forgery, so no cookie CSRF is required).
func IsBearerRequest(r *http.Request) bool {
	return r != nil && ParseBearer(r.Header.Get("Authorization")) != ""
}

// IssueAgentToken creates a token bound to a real user identity and returns the
// raw secret (exactly once). Pass the already-hashed hash to keep raw handling
// in the caller that surfaces it to the operator.
func IssueAgentToken(ctx context.Context, store TokenStore, userID, label, raw string, scopes []string) error {
	if userID == "" || raw == "" {
		return ErrAgentInvalid
	}
	return store.CreateAgentToken(ctx, userID, HashAgentToken(raw), label, strings.Join(scopes, "|"))
}

// ResolveAgentToken authenticates a raw bearer token to an identity + scopes.
// It rejects revoked or expired tokens and refuses empty/unknown hashes.
func ResolveAgentToken(ctx context.Context, store TokenStore, raw string) (AgentScope, error) {
	if raw == "" {
		return AgentScope{}, ErrNoAgentToken
	}
	hash := HashAgentToken(raw)
	tok, err := store.LookupAgentToken(ctx, hash)
	if err != nil {
		return AgentScope{}, err
	}
	if tok.Revoked {
		return AgentScope{}, ErrAgentRevoked
	}
	if tok.Expires {
		return AgentScope{}, ErrAgentExpired
	}
	if err := store.TouchAgentToken(ctx, tok.ID); err != nil {
		return AgentScope{}, err
	}
	return AgentScope{UserID: tok.UserID, Scopes: tok.Scopes}, nil
}
