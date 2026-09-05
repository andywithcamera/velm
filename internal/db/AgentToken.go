package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"velm/internal/auth"
)

// AgentTokenStore persists agent API tokens. It implements auth.TokenStore so
// the auth package stays DB-agnostic. user_id here is the platform canonical
// identity key (_id::text, same join key as _user_role) — issue #68: agents are
// plain _user rows, token-bound identity is what marks API authN.

var _ auth.TokenStore = (*AgentTokenStore)(nil)

type AgentTokenStore struct{}

func NewAgentTokenStore() *AgentTokenStore { return &AgentTokenStore{} }

func (s *AgentTokenStore) CreateAgentToken(ctx context.Context, userID, tokenHash, label, scopes string) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}
	_, err := Pool.Exec(ctx, `
		INSERT INTO _agent_api_token (user_id, token_hash, label, scopes, is_revoked)
		VALUES ($1, $2, $3, $4, FALSE)
	`, userID, tokenHash, label, scopes)
	return err
}

func (s *AgentTokenStore) LookupAgentToken(ctx context.Context, tokenHash string) (auth.AgentToken, error) {
	if Pool == nil {
		return auth.AgentToken{}, fmt.Errorf("database pool is not initialized")
	}
	if tokenHash == "" {
		return auth.AgentToken{}, auth.ErrNoAgentToken
	}

	var tok auth.AgentToken
	var scopes string
	var revoked bool
	var expiresAt *time.Time
	row := Pool.QueryRow(ctx, `
		SELECT _id::text, user_id, label, scopes, is_revoked, expires_at
		FROM _agent_api_token
		WHERE token_hash = $1
	`, tokenHash)
	if err := row.Scan(&tok.ID, &tok.UserID, &tok.Label, &scopes, &revoked, &expiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.AgentToken{}, auth.ErrNoAgentToken
		}
		return auth.AgentToken{}, err
	}

	tok.Revoked = revoked
	tok.Scopes = splitScopes(scopes)
	if expiresAt != nil {
		tok.Expires = expiresAt.Before(time.Now())
	}
	return tok, nil
}

func (s *AgentTokenStore) TouchAgentToken(ctx context.Context, tokenID string) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}
	_, err := Pool.Exec(ctx,
		`UPDATE _agent_api_token SET last_used_at = NOW() WHERE _id::text = $1`, tokenID)
	return err
}

func splitScopes(scopes string) []string {
	scopes = strings.TrimSpace(scopes)
	if scopes == "" {
		return nil
	}
	parts := strings.Split(scopes, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// LookupUserIdentity implements auth.IdentityResolver: agents are plain _user
// rows, so this is the identical user lookup the human session path uses.
func (s *AgentTokenStore) LookupUserIdentity(ctx context.Context, userID string) (email, name string, err error) {
	if Pool == nil {
		return "", "", fmt.Errorf("database pool is not initialized")
	}
	err = Pool.QueryRow(ctx,
		`SELECT email, name FROM _user WHERE _id::text = $1 LIMIT 1`,
		userID,
	).Scan(&email, &name)
	if err != nil {
		return "", "", err
	}
	return email, name, nil
}
