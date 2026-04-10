package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type RequestMetricRecord struct {
	RequestID      string
	StartedAt      time.Time
	FinishedAt     time.Time
	RequestSource  string
	Method         string
	Path           string
	QueryString    string
	Status         int
	UserID         string
	UserEmail      string
	UserRole       string
	IP             string
	UserAgent      string
	Referer        string
	ContentType    string
	ServerDuration time.Duration
	DBDuration     time.Duration
	DBQueryCount   int
	DBSlowest      time.Duration
	DBSlowestQuery string
}

type RequestMetricClientUpdate struct {
	RequestID          string
	Method             string
	Path               string
	RequestSource      string
	ClientEventType    string
	ClientNavType      string
	ClientTotalMS      int
	ClientNetworkMS    int
	ClientTTFBMS       int
	ClientTransferMS   int
	ClientProcessingMS int
	ClientRenderMS     int
	ClientDOMContentMS int
	ClientLoadEventMS  int
	ClientSettleMS     int
	ClientPayload      map[string]any
}

func UpsertRequestMetricServer(ctx context.Context, metric RequestMetricRecord) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}

	const query = `
INSERT INTO _request_metric (
	request_id,
	started_at,
	finished_at,
	request_source,
	method,
	path,
	query_string,
	status,
	user_id,
	user_email,
	user_role,
	ip,
	user_agent,
	referer,
	content_type,
	server_duration_ms,
	db_duration_ms,
	db_query_count,
	db_slowest_query_ms,
	db_slowest_query,
	_updated_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, NOW()
)
ON CONFLICT (request_id) DO UPDATE SET
	started_at = EXCLUDED.started_at,
	finished_at = EXCLUDED.finished_at,
	request_source = EXCLUDED.request_source,
	method = EXCLUDED.method,
	path = EXCLUDED.path,
	query_string = EXCLUDED.query_string,
	status = EXCLUDED.status,
	user_id = NULLIF(EXCLUDED.user_id, ''),
	user_email = NULLIF(EXCLUDED.user_email, ''),
	user_role = NULLIF(EXCLUDED.user_role, ''),
	ip = NULLIF(EXCLUDED.ip, ''),
	user_agent = NULLIF(EXCLUDED.user_agent, ''),
	referer = NULLIF(EXCLUDED.referer, ''),
	content_type = NULLIF(EXCLUDED.content_type, ''),
	server_duration_ms = EXCLUDED.server_duration_ms,
	db_duration_ms = EXCLUDED.db_duration_ms,
	db_query_count = EXCLUDED.db_query_count,
	db_slowest_query_ms = EXCLUDED.db_slowest_query_ms,
	db_slowest_query = NULLIF(EXCLUDED.db_slowest_query, ''),
	_updated_at = NOW()
`

	_, err := Pool.Exec(
		ctx,
		query,
		strings.TrimSpace(metric.RequestID),
		metric.StartedAt.UTC(),
		metric.FinishedAt.UTC(),
		normalizeRequestMetricSource(metric.RequestSource),
		strings.TrimSpace(metric.Method),
		strings.TrimSpace(metric.Path),
		strings.TrimSpace(metric.QueryString),
		metric.Status,
		strings.TrimSpace(metric.UserID),
		strings.TrimSpace(metric.UserEmail),
		strings.TrimSpace(metric.UserRole),
		strings.TrimSpace(metric.IP),
		strings.TrimSpace(metric.UserAgent),
		strings.TrimSpace(metric.Referer),
		strings.TrimSpace(metric.ContentType),
		durationToMS(metric.ServerDuration),
		durationToMS(metric.DBDuration),
		maxInt(metric.DBQueryCount, 0),
		durationToMS(metric.DBSlowest),
		strings.TrimSpace(metric.DBSlowestQuery),
	)
	if err != nil {
		return fmt.Errorf("upsert request metric server row: %w", err)
	}
	if metric.RequestID != "" || metric.Path != "" {
		emitDerivedRequestMetricObservability(metric)
	}
	return nil
}

func UpsertRequestMetricClient(ctx context.Context, metric RequestMetricClientUpdate) error {
	if Pool == nil {
		return fmt.Errorf("database pool is not initialized")
	}

	payload, err := json.Marshal(metric.ClientPayload)
	if err != nil {
		return fmt.Errorf("marshal request metric client payload: %w", err)
	}

	const query = `
INSERT INTO _request_metric (
	request_id,
	started_at,
	request_source,
	method,
	path,
	status,
	client_event_type,
	client_nav_type,
	client_total_ms,
	client_network_ms,
	client_ttfb_ms,
	client_transfer_ms,
	client_processing_ms,
	client_render_ms,
	client_dom_content_loaded_ms,
	client_load_event_ms,
	client_settle_ms,
	client_payload,
	_updated_at
) VALUES (
	$1, NOW(), $2, $3, $4, 0, $5, $6, NULLIF($7, -1), NULLIF($8, -1), NULLIF($9, -1), NULLIF($10, -1),
	NULLIF($11, -1), NULLIF($12, -1), NULLIF($13, -1), NULLIF($14, -1), NULLIF($15, -1), $16::jsonb, NOW()
)
ON CONFLICT (request_id) DO UPDATE SET
	client_event_type = NULLIF(EXCLUDED.client_event_type, ''),
	client_nav_type = NULLIF(EXCLUDED.client_nav_type, ''),
	client_total_ms = COALESCE(EXCLUDED.client_total_ms, _request_metric.client_total_ms),
	client_network_ms = COALESCE(EXCLUDED.client_network_ms, _request_metric.client_network_ms),
	client_ttfb_ms = COALESCE(EXCLUDED.client_ttfb_ms, _request_metric.client_ttfb_ms),
	client_transfer_ms = COALESCE(EXCLUDED.client_transfer_ms, _request_metric.client_transfer_ms),
	client_processing_ms = COALESCE(EXCLUDED.client_processing_ms, _request_metric.client_processing_ms),
	client_render_ms = COALESCE(EXCLUDED.client_render_ms, _request_metric.client_render_ms),
	client_dom_content_loaded_ms = COALESCE(EXCLUDED.client_dom_content_loaded_ms, _request_metric.client_dom_content_loaded_ms),
	client_load_event_ms = COALESCE(EXCLUDED.client_load_event_ms, _request_metric.client_load_event_ms),
	client_settle_ms = COALESCE(EXCLUDED.client_settle_ms, _request_metric.client_settle_ms),
	client_payload = COALESCE(_request_metric.client_payload, '{}'::jsonb) || EXCLUDED.client_payload,
	_updated_at = NOW(),
	method = CASE WHEN COALESCE(_request_metric.method, '') = '' THEN EXCLUDED.method ELSE _request_metric.method END,
	path = CASE WHEN COALESCE(_request_metric.path, '') = '' THEN EXCLUDED.path ELSE _request_metric.path END,
	request_source = CASE WHEN COALESCE(_request_metric.request_source, '') IN ('', 'client') THEN EXCLUDED.request_source ELSE _request_metric.request_source END
`

	_, err = Pool.Exec(
		ctx,
		query,
		strings.TrimSpace(metric.RequestID),
		normalizeRequestMetricSource(metric.RequestSource),
		strings.TrimSpace(metric.Method),
		strings.TrimSpace(metric.Path),
		strings.TrimSpace(metric.ClientEventType),
		strings.TrimSpace(metric.ClientNavType),
		nullableMetricInt(metric.ClientTotalMS),
		nullableMetricInt(metric.ClientNetworkMS),
		nullableMetricInt(metric.ClientTTFBMS),
		nullableMetricInt(metric.ClientTransferMS),
		nullableMetricInt(metric.ClientProcessingMS),
		nullableMetricInt(metric.ClientRenderMS),
		nullableMetricInt(metric.ClientDOMContentMS),
		nullableMetricInt(metric.ClientLoadEventMS),
		nullableMetricInt(metric.ClientSettleMS),
		string(payload),
	)
	if err != nil {
		return fmt.Errorf("upsert request metric client row: %w", err)
	}
	return nil
}

func durationToMS(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int(duration / time.Millisecond)
}

func nullableMetricInt(value int) int {
	if value < 0 {
		return -1
	}
	return value
}

func normalizeRequestMetricSource(source string) string {
	source = strings.TrimSpace(strings.ToLower(source))
	if source == "" {
		return "unknown"
	}
	return source
}

func maxInt(value, floor int) int {
	if value < floor {
		return floor
	}
	return value
}
