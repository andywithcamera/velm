package db

import (
	"context"
	"encoding/json"
)

func LogSecurityEvent(
	ctx context.Context,
	eventType string,
	severity string,
	userID string,
	requestPath string,
	requestMethod string,
	ip string,
	userAgent string,
	detail map[string]any,
) error {
	if Pool == nil {
		return nil
	}

	if detail == nil {
		detail = map[string]any{}
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		return err
	}

	_, err = Pool.Exec(ctx, `
		INSERT INTO _security_log (event_type, severity, user_id, request_path, request_method, ip, user_agent, detail)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8::jsonb)
	`,
		eventType,
		severity,
		userID,
		requestPath,
		requestMethod,
		ip,
		userAgent,
		string(payload),
	)
	return err
}
