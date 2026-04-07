package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"velm/internal/utils"

	"github.com/jackc/pgx/v5"
)

const (
	defaultUserNotificationLimit = 8
	maxUserNotificationLimit     = 50
	maxUserNotificationTitleLen  = 180
	maxUserNotificationBodyLen   = 2000
	maxUserNotificationHrefLen   = 2048
)

var allowedUserNotificationLevels = []string{"info", "success", "warning", "error"}

type UserNotification struct {
	ID        string
	UserID    string
	Title     string
	Body      string
	Href      string
	Level     string
	ReadAt    time.Time
	IsRead    bool
	CreatedAt time.Time
}

type UserNotificationCreateInput struct {
	UserID    string
	Title     string
	Body      string
	Href      string
	Level     string
	CreatedBy string
}

type ScriptNotificationRequest struct {
	UserIDs []string
	Title   string
	Body    string
	Href    string
	Level   string
}

func CreateUserNotification(ctx context.Context, input UserNotificationCreateInput) (UserNotification, error) {
	return createUserNotificationWithQuerier(ctx, Pool, input)
}

func ListUserNotifications(ctx context.Context, userID string, limit int) ([]UserNotification, error) {
	return listUserNotificationsWithQuerier(ctx, Pool, userID, limit, false)
}

func ListUnreadUserNotifications(ctx context.Context, userID string, limit int) ([]UserNotification, error) {
	return listUserNotificationsWithQuerier(ctx, Pool, userID, limit, true)
}

func CountUnreadUserNotifications(ctx context.Context, userID string) (int, error) {
	return countUnreadUserNotificationsWithQuerier(ctx, Pool, userID)
}

func MarkAllUserNotificationsRead(ctx context.Context, userID string) (int64, error) {
	return markAllUserNotificationsReadWithQuerier(ctx, Pool, userID)
}

func ReadUserNotification(ctx context.Context, userID, notificationID string) (UserNotification, bool, error) {
	return readUserNotificationWithQuerier(ctx, Pool, userID, notificationID)
}

func NormalizeScriptNotificationRequest(raw map[string]any, fallbackUserID string) (ScriptNotificationRequest, error) {
	if raw == nil {
		return ScriptNotificationRequest{}, fmt.Errorf("notification must be an object")
	}

	userIDs := make([]string, 0, 4)
	if rawUserID, ok := raw["userID"]; ok {
		if userID := normalizeScriptNotificationText(rawUserID); userID != "" {
			userIDs = append(userIDs, userID)
		}
	}
	if rawUserIDs, ok := raw["userIDs"]; ok {
		items, ok := normalizeScriptExportValue(rawUserIDs).([]any)
		if !ok {
			return ScriptNotificationRequest{}, fmt.Errorf("notification.userIDs must be an array")
		}
		for _, item := range items {
			if userID := strings.TrimSpace(fmt.Sprint(item)); userID != "" {
				userIDs = append(userIDs, userID)
			}
		}
	}
	if len(userIDs) == 0 {
		if fallback := strings.TrimSpace(fallbackUserID); fallback != "" {
			userIDs = append(userIDs, fallback)
		}
	}
	if len(userIDs) == 0 {
		return ScriptNotificationRequest{}, fmt.Errorf("notification userID is required")
	}

	uniqueUserIDs := make([]string, 0, len(userIDs))
	seenUserIDs := make(map[string]bool, len(userIDs))
	for _, userID := range userIDs {
		if seenUserIDs[userID] {
			continue
		}
		seenUserIDs[userID] = true
		uniqueUserIDs = append(uniqueUserIDs, userID)
	}

	return ScriptNotificationRequest{
		UserIDs: uniqueUserIDs,
		Title:   normalizeScriptNotificationText(raw["title"]),
		Body:    normalizeScriptNotificationText(raw["body"]),
		Href:    normalizeScriptNotificationText(raw["href"]),
		Level:   normalizeScriptNotificationText(raw["level"]),
	}, nil
}

func normalizeScriptNotificationText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func createUserNotificationWithQuerier(ctx context.Context, querier scriptQuerier, input UserNotificationCreateInput) (UserNotification, error) {
	if querier == nil {
		return UserNotification{}, fmt.Errorf("database is not initialized")
	}

	normalized, err := normalizeUserNotificationInput(input)
	if err != nil {
		return UserNotification{}, err
	}

	var item UserNotification
	var readAt time.Time
	query := `
		INSERT INTO _user_notification (
			user_id,
			title,
			body,
			href,
			level,
			_created_by,
			_updated_by
		)
		VALUES (
			$1::uuid,
			$2,
			$3,
			$4,
			$5,
			NULLIF($6, '')::uuid,
			NULLIF($6, '')::uuid
		)
		RETURNING _id::text, user_id::text, title, COALESCE(body, ''), COALESCE(href, ''), level, COALESCE(read_at, '0001-01-01T00:00:00Z'::timestamptz), _created_at
	`
	if err := querier.QueryRow(
		ctx,
		query,
		normalized.UserID,
		normalized.Title,
		normalized.Body,
		normalized.Href,
		normalized.Level,
		normalized.CreatedBy,
	).Scan(
		&item.ID,
		&item.UserID,
		&item.Title,
		&item.Body,
		&item.Href,
		&item.Level,
		&readAt,
		&item.CreatedAt,
	); err != nil {
		return UserNotification{}, err
	}
	item.ReadAt = readAt
	item.IsRead = !readAt.IsZero()
	return item, nil
}

func createUserNotificationsWithQuerier(ctx context.Context, querier scriptQuerier, inputs []UserNotificationCreateInput) ([]UserNotification, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	items := make([]UserNotification, 0, len(inputs))
	for _, input := range inputs {
		item, err := createUserNotificationWithQuerier(ctx, querier, input)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func listUserNotificationsWithQuerier(ctx context.Context, querier scriptQuerier, userID string, limit int, unreadOnly bool) ([]UserNotification, error) {
	if querier == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	if !utils.IsValidUUID(strings.TrimSpace(userID)) {
		return nil, fmt.Errorf("invalid user id")
	}
	if limit <= 0 {
		limit = defaultUserNotificationLimit
	}
	if limit > maxUserNotificationLimit {
		limit = maxUserNotificationLimit
	}

	query := `
		SELECT
			_id::text,
			user_id::text,
			title,
			COALESCE(body, ''),
			COALESCE(href, ''),
			COALESCE(level, 'info'),
			COALESCE(read_at, '0001-01-01T00:00:00Z'::timestamptz),
			_created_at
		FROM _user_notification
		WHERE user_id = $1::uuid
		  AND _deleted_at IS NULL
	`
	if unreadOnly {
		query += `
		  AND read_at IS NULL
		ORDER BY _created_at DESC
		`
	} else {
		query += `
		ORDER BY (read_at IS NULL) DESC, _created_at DESC
		`
	}
	query += `
		LIMIT $2
	`

	rows, err := querier.Query(ctx, query, strings.TrimSpace(userID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]UserNotification, 0, limit)
	for rows.Next() {
		var item UserNotification
		var readAt time.Time
		if err := rows.Scan(
			&item.ID,
			&item.UserID,
			&item.Title,
			&item.Body,
			&item.Href,
			&item.Level,
			&readAt,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		item.ReadAt = readAt
		item.IsRead = !readAt.IsZero()
		items = append(items, item)
	}
	return items, rows.Err()
}

func countUnreadUserNotificationsWithQuerier(ctx context.Context, querier scriptQuerier, userID string) (int, error) {
	if querier == nil {
		return 0, fmt.Errorf("database is not initialized")
	}
	if !utils.IsValidUUID(strings.TrimSpace(userID)) {
		return 0, fmt.Errorf("invalid user id")
	}

	var count int
	err := querier.QueryRow(ctx, `
		SELECT COUNT(1)
		FROM _user_notification
		WHERE user_id = $1::uuid
		  AND read_at IS NULL
		  AND _deleted_at IS NULL
	`, strings.TrimSpace(userID)).Scan(&count)
	return count, err
}

func markAllUserNotificationsReadWithQuerier(ctx context.Context, querier scriptQuerier, userID string) (int64, error) {
	if querier == nil {
		return 0, fmt.Errorf("database is not initialized")
	}
	userID = strings.TrimSpace(userID)
	if !utils.IsValidUUID(userID) {
		return 0, fmt.Errorf("invalid user id")
	}

	now := time.Now().UTC()
	tag, err := querier.Exec(ctx, `
		UPDATE _user_notification
		SET
			read_at = $2::timestamptz,
			_updated_at = $2::timestamptz,
			_updated_by = $1::uuid
		WHERE user_id = $1::uuid
		  AND read_at IS NULL
		  AND _deleted_at IS NULL
	`, userID, now.Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func readUserNotificationWithQuerier(ctx context.Context, querier scriptQuerier, userID, notificationID string) (UserNotification, bool, error) {
	if querier == nil {
		return UserNotification{}, false, fmt.Errorf("database is not initialized")
	}
	userID = strings.TrimSpace(userID)
	notificationID = strings.TrimSpace(notificationID)
	if !utils.IsValidUUID(userID) {
		return UserNotification{}, false, fmt.Errorf("invalid user id")
	}
	if !utils.IsValidUUID(notificationID) {
		return UserNotification{}, false, fmt.Errorf("invalid notification id")
	}

	now := time.Now().UTC()
	var (
		item   UserNotification
		readAt time.Time
	)
	err := querier.QueryRow(ctx, `
		UPDATE _user_notification
		SET
			read_at = COALESCE(read_at, $3::timestamptz),
			_updated_at = $3::timestamptz,
			_updated_by = $1::uuid
		WHERE user_id = $1::uuid
		  AND _id = $2::uuid
		  AND _deleted_at IS NULL
		RETURNING
			_id::text,
			user_id::text,
			title,
			COALESCE(body, ''),
			COALESCE(href, ''),
			COALESCE(level, 'info'),
			COALESCE(read_at, '0001-01-01T00:00:00Z'::timestamptz),
			_created_at
	`, userID, notificationID, now.Format(time.RFC3339Nano)).Scan(
		&item.ID,
		&item.UserID,
		&item.Title,
		&item.Body,
		&item.Href,
		&item.Level,
		&readAt,
		&item.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserNotification{}, false, nil
		}
		return UserNotification{}, false, err
	}
	item.ReadAt = readAt
	item.IsRead = !readAt.IsZero()
	return item, true, nil
}

func normalizeUserNotificationInput(input UserNotificationCreateInput) (UserNotificationCreateInput, error) {
	normalized := UserNotificationCreateInput{
		UserID:    strings.TrimSpace(input.UserID),
		Title:     strings.TrimSpace(input.Title),
		Body:      strings.TrimSpace(input.Body),
		Href:      strings.TrimSpace(input.Href),
		Level:     strings.ToLower(strings.TrimSpace(input.Level)),
		CreatedBy: strings.TrimSpace(input.CreatedBy),
	}

	if !utils.IsValidUUID(normalized.UserID) {
		return UserNotificationCreateInput{}, fmt.Errorf("notification user_id is invalid")
	}
	if normalized.Title == "" {
		return UserNotificationCreateInput{}, fmt.Errorf("notification title is required")
	}
	if len(normalized.Title) > maxUserNotificationTitleLen {
		return UserNotificationCreateInput{}, fmt.Errorf("notification title exceeds %d characters", maxUserNotificationTitleLen)
	}
	if len(normalized.Body) > maxUserNotificationBodyLen {
		return UserNotificationCreateInput{}, fmt.Errorf("notification body exceeds %d characters", maxUserNotificationBodyLen)
	}
	if len(normalized.Href) > maxUserNotificationHrefLen {
		return UserNotificationCreateInput{}, fmt.Errorf("notification href exceeds %d characters", maxUserNotificationHrefLen)
	}
	href, err := normalizeUserNotificationHref(normalized.Href)
	if err != nil {
		return UserNotificationCreateInput{}, err
	}
	normalized.Href = href
	if normalized.Level == "" {
		normalized.Level = "info"
	}
	if !slices.Contains(allowedUserNotificationLevels, normalized.Level) {
		return UserNotificationCreateInput{}, fmt.Errorf("notification level must be one of %s", strings.Join(allowedUserNotificationLevels, ", "))
	}
	if normalized.CreatedBy != "" && !utils.IsValidUUID(normalized.CreatedBy) {
		return UserNotificationCreateInput{}, fmt.Errorf("notification created_by is invalid")
	}
	return normalized, nil
}

func normalizeUserNotificationHref(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return raw, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("notification href is invalid")
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "https", "mailto":
		return raw, nil
	default:
		return "", fmt.Errorf("notification href must be a relative path or http(s)/mailto URL")
	}
}
