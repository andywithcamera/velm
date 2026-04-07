package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type RecordFieldDiff struct {
	Old string `json:"old"`
	New string `json:"new"`
}

type RecordChangeEntry struct {
	CreatedAt time.Time
	UserID    string
	UserName  string
	UserEmail string
	Operation string
	FieldDiff map[string]RecordFieldDiff
}

type RecordCommentEntry struct {
	CreatedAt time.Time
	UserID    string
	UserName  string
	UserEmail string
	Body      string
}

func ListRecordChangeEntries(ctx context.Context, tableName, recordID string, limit int) ([]RecordChangeEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 80
	}

	rows, err := Pool.Query(ctx, `
		SELECT
			a._created_at,
			COALESCE(a.user_id, ''),
			COALESCE(u.name, ''),
			COALESCE(u.email, ''),
			COALESCE(a.operation, ''),
			COALESCE(a.field_diff::text, '{}')
		FROM _audit_data_change a
		LEFT JOIN _user u ON u._id::text = a.user_id
		WHERE a.table_name = $1
		  AND a.record_id = $2
		ORDER BY a._created_at DESC
		LIMIT $3
	`, strings.TrimSpace(tableName), strings.TrimSpace(recordID), limit)
	if err != nil {
		return nil, fmt.Errorf("list record change entries: %w", err)
	}
	defer rows.Close()

	entries := make([]RecordChangeEntry, 0, limit)
	for rows.Next() {
		var entry RecordChangeEntry
		var diffRaw string
		if err := rows.Scan(
			&entry.CreatedAt,
			&entry.UserID,
			&entry.UserName,
			&entry.UserEmail,
			&entry.Operation,
			&diffRaw,
		); err != nil {
			return nil, fmt.Errorf("scan record change entry: %w", err)
		}
		if err := json.Unmarshal([]byte(diffRaw), &entry.FieldDiff); err != nil {
			entry.FieldDiff = map[string]RecordFieldDiff{}
		}
		if entry.FieldDiff == nil {
			entry.FieldDiff = map[string]RecordFieldDiff{}
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate record change entries: %w", err)
	}
	return entries, nil
}

func ListRecordCommentEntries(ctx context.Context, tableName, recordID string, limit int) ([]RecordCommentEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 80
	}

	rows, err := Pool.Query(ctx, `
		SELECT
			c._created_at,
			COALESCE(c.user_id, ''),
			COALESCE(u.name, ''),
			COALESCE(u.email, ''),
			COALESCE(c.body, '')
		FROM _record_comment c
		LEFT JOIN _user u ON u._id::text = c.user_id
		WHERE c.table_name = $1
		  AND c.record_id = $2
		ORDER BY c._created_at DESC
		LIMIT $3
	`, strings.TrimSpace(tableName), strings.TrimSpace(recordID), limit)
	if err != nil {
		return nil, fmt.Errorf("list record comments: %w", err)
	}
	defer rows.Close()

	entries := make([]RecordCommentEntry, 0, limit)
	for rows.Next() {
		var entry RecordCommentEntry
		if err := rows.Scan(
			&entry.CreatedAt,
			&entry.UserID,
			&entry.UserName,
			&entry.UserEmail,
			&entry.Body,
		); err != nil {
			return nil, fmt.Errorf("scan record comment: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate record comments: %w", err)
	}
	return entries, nil
}

func AddRecordComment(ctx context.Context, tableName, recordID, userID, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("comment body is required")
	}
	if len(body) > 4000 {
		return fmt.Errorf("comment body exceeds 4000 characters")
	}

	_, err := Pool.Exec(ctx, `
		INSERT INTO _record_comment (user_id, table_name, record_id, body)
		VALUES ($1, $2, $3, $4)
	`,
		strings.TrimSpace(userID),
		strings.TrimSpace(tableName),
		strings.TrimSpace(recordID),
		body,
	)
	if err != nil {
		return fmt.Errorf("add record comment: %w", err)
	}
	return nil
}
