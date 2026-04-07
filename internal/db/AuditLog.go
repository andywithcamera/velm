package db

import (
	"context"
	"errors"
)

type AuditEvent struct {
	UserID    string
	UserEmail string
	UserRole  string
	Method    string
	Path      string
	Status    int
	IP        string
	UserAgent string
}

func EnsureAuditLogTable() error {
	if Pool == nil {
		return errors.New("database pool is not initialized")
	}

	const query = `
CREATE TABLE IF NOT EXISTS _audit_log (
	_id BIGSERIAL PRIMARY KEY,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	user_id TEXT,
	user_email TEXT,
	user_role TEXT,
	method TEXT NOT NULL,
	path TEXT NOT NULL,
	status INTEGER NOT NULL,
	ip TEXT,
	user_agent TEXT
)`
	_, err := Pool.Exec(context.Background(), query)
	return err
}

func WriteAuditLog(ctx context.Context, event AuditEvent) error {
	if Pool == nil {
		return errors.New("database pool is not initialized")
	}

	const query = `
INSERT INTO _audit_log (
	user_id, user_email, user_role, method, path, status, ip, user_agent
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := Pool.Exec(
		ctx,
		query,
		event.UserID,
		event.UserEmail,
		event.UserRole,
		event.Method,
		event.Path,
		event.Status,
		event.IP,
		event.UserAgent,
	)
	return err
}
