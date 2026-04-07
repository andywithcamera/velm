package db

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type AuditLogFilter struct {
	UserID string
	Method string
	Path   string
	Status int
	From   time.Time
	To     time.Time
	Limit  int
}

type AuditLogEntry struct {
	CreatedAt time.Time
	UserID    string
	UserEmail string
	UserRole  string
	Method    string
	Path      string
	Status    int
	IP        string
	UserAgent string
}

func ListAuditLogs(ctx context.Context, filter AuditLogFilter) ([]AuditLogEntry, error) {
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 200
	}

	query := `
		SELECT _created_at, COALESCE(user_id, ''), COALESCE(user_email, ''), COALESCE(user_role, ''),
		       method, path, status, COALESCE(ip, ''), COALESCE(user_agent, '')
		FROM _audit_log
		WHERE 1=1
	`
	args := make([]any, 0, 8)
	argIndex := 1

	if filter.UserID != "" {
		query += fmt.Sprintf(" AND user_id = $%d", argIndex)
		args = append(args, filter.UserID)
		argIndex++
	}
	if filter.Method != "" {
		query += fmt.Sprintf(" AND method = $%d", argIndex)
		args = append(args, strings.ToUpper(filter.Method))
		argIndex++
	}
	if filter.Path != "" {
		query += fmt.Sprintf(" AND path ILIKE $%d", argIndex)
		args = append(args, "%"+filter.Path+"%")
		argIndex++
	}
	if filter.Status > 0 {
		query += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, filter.Status)
		argIndex++
	}
	if !filter.From.IsZero() {
		query += fmt.Sprintf(" AND _created_at >= $%d", argIndex)
		args = append(args, filter.From)
		argIndex++
	}
	if !filter.To.IsZero() {
		query += fmt.Sprintf(" AND _created_at <= $%d", argIndex)
		args = append(args, filter.To)
		argIndex++
	}

	query += fmt.Sprintf(" ORDER BY _created_at DESC LIMIT $%d", argIndex)
	args = append(args, filter.Limit)

	rows, err := Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditLogEntry
	for rows.Next() {
		var entry AuditLogEntry
		if err := rows.Scan(
			&entry.CreatedAt,
			&entry.UserID,
			&entry.UserEmail,
			&entry.UserRole,
			&entry.Method,
			&entry.Path,
			&entry.Status,
			&entry.IP,
			&entry.UserAgent,
		); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
