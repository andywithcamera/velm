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
	// RecordID is the record the comment was written on. In a task chain the
	// thread spans stages, so this is what tells a reader which stage a remark
	// belongs to.
	RecordID string
	// RecordLabel is that record's human reference (a task's number), when the
	// record has one.
	RecordLabel string
}

// commentThreadTable is the one table whose comments span a chain rather than
// a single record. Everything else keeps a per-record thread.
const commentThreadTable = TaskTableName

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

	tableName = strings.TrimSpace(tableName)
	recordID = strings.TrimSpace(recordID)

	// A task's comments belong to its chain, so the query walks to the root and
	// reads the whole conversation. Non-task tables keep the per-record query:
	// widening those would merge unrelated threads.
	if tableName == commentThreadTable {
		entries, err := listChainCommentEntries(ctx, recordID, limit)
		if err != nil {
			return nil, err
		}
		return entries, nil
	}

	rows, err := Pool.Query(ctx, `
		SELECT
			c._created_at,
			COALESCE(c.user_id, ''),
			COALESCE(u.name, ''),
			COALESCE(u.email, ''),
			COALESCE(c.body, ''),
			COALESCE(c.record_id, ''),
			''
		FROM _record_comment c
		LEFT JOIN _user u ON u._id::text = c.user_id
		WHERE c.table_name = $1
		  AND c.record_id = $2
		ORDER BY c._created_at DESC
		LIMIT $3
	`, tableName, recordID, limit)
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
			&entry.RecordID,
			&entry.RecordLabel,
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

// listChainCommentEntries reads every comment on the chain that recordID
// belongs to. Comments written before 093 carry a thread_id from the backfill;
// the record_id clause is belt and braces so a thread_id that somehow stayed
// NULL cannot hide a stage's own comments.
func listChainCommentEntries(ctx context.Context, recordID string, limit int) ([]RecordCommentEntry, error) {
	rows, err := Pool.Query(ctx, `
		WITH RECURSIVE up AS (
			SELECT _id AS task_id, parent_task_id, 0 AS depth
			FROM base_task
			WHERE _id::text = $1
			UNION ALL
			SELECT t._id, t.parent_task_id, up.depth + 1
			FROM base_task t
			JOIN up ON t._id = up.parent_task_id
			WHERE up.depth < 100
		), chain_root AS (
			SELECT task_id FROM up ORDER BY depth DESC LIMIT 1
		)
		SELECT
			c._created_at,
			COALESCE(c.user_id, ''),
			COALESCE(u.name, ''),
			COALESCE(u.email, ''),
			COALESCE(c.body, ''),
			COALESCE(c.record_id, ''),
			COALESCE(t.number, '')
		FROM _record_comment c
		LEFT JOIN _user u ON u._id::text = c.user_id
		LEFT JOIN base_task t ON t._id::text = c.record_id
		WHERE c.table_name = 'base_task'
		  AND (
			c.thread_id = (SELECT task_id FROM chain_root)
			OR c.record_id = $1
		  )
		ORDER BY c._created_at DESC
		LIMIT $2
	`, recordID, limit)
	if err != nil {
		return nil, fmt.Errorf("list chain comments: %w", err)
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
			&entry.RecordID,
			&entry.RecordLabel,
		); err != nil {
			return nil, fmt.Errorf("scan chain comment: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chain comments: %w", err)
	}
	return entries, nil
}

// taskChainRoot returns the ancestor of taskID that has no parent. A task with
// no parent is its own root.
func taskChainRoot(ctx context.Context, taskID string) (string, error) {
	var root string
	err := Pool.QueryRow(ctx, `
		WITH RECURSIVE up AS (
			SELECT _id AS task_id, parent_task_id, 0 AS depth
			FROM base_task
			WHERE _id::text = $1
			UNION ALL
			SELECT t._id, t.parent_task_id, up.depth + 1
			FROM base_task t
			JOIN up ON t._id = up.parent_task_id
			WHERE up.depth < 100
		)
		SELECT task_id::text FROM up ORDER BY depth DESC LIMIT 1
	`, strings.TrimSpace(taskID)).Scan(&root)
	if err != nil {
		return "", fmt.Errorf("resolve task chain root: %w", err)
	}
	return root, nil
}

func AddRecordComment(ctx context.Context, tableName, recordID, userID, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("comment body is required")
	}
	if len(body) > 4000 {
		return fmt.Errorf("comment body exceeds 4000 characters")
	}

	tableName = strings.TrimSpace(tableName)
	recordID = strings.TrimSpace(recordID)

	// A comment on a task joins that chain's thread. Resolving the root here
	// rather than in a trigger keeps the walk in Go where the depth cap and the
	// failure are visible. A record that is not a task keeps thread_id NULL,
	// which is its own thread.
	var threadID any
	if tableName == commentThreadTable {
		if root, err := taskChainRoot(ctx, recordID); err == nil && root != "" {
			threadID = root
		}
	}

	_, err := Pool.Exec(ctx, `
		INSERT INTO _record_comment (user_id, table_name, record_id, body, thread_id)
		VALUES ($1, $2, $3, $4, $5)
	`,
		strings.TrimSpace(userID),
		tableName,
		recordID,
		body,
		threadID,
	)
	if err != nil {
		return fmt.Errorf("add record comment: %w", err)
	}
	return nil
}
