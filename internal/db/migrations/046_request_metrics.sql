CREATE TABLE IF NOT EXISTS _request_metric (
	_id BIGSERIAL PRIMARY KEY,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	request_id TEXT NOT NULL UNIQUE,
	started_at TIMESTAMPTZ NOT NULL,
	finished_at TIMESTAMPTZ,
	request_source TEXT NOT NULL DEFAULT 'unknown',
	method TEXT NOT NULL,
	path TEXT NOT NULL,
	query_string TEXT,
	status INTEGER NOT NULL DEFAULT 0,
	user_id TEXT,
	user_email TEXT,
	user_role TEXT,
	ip TEXT,
	user_agent TEXT,
	referer TEXT,
	content_type TEXT,
	server_duration_ms INTEGER NOT NULL DEFAULT 0,
	db_duration_ms INTEGER NOT NULL DEFAULT 0,
	db_query_count INTEGER NOT NULL DEFAULT 0,
	db_slowest_query_ms INTEGER NOT NULL DEFAULT 0,
	db_slowest_query TEXT,
	client_event_type TEXT,
	client_nav_type TEXT,
	client_total_ms INTEGER,
	client_network_ms INTEGER,
	client_ttfb_ms INTEGER,
	client_transfer_ms INTEGER,
	client_processing_ms INTEGER,
	client_render_ms INTEGER,
	client_dom_content_loaded_ms INTEGER,
	client_load_event_ms INTEGER,
	client_settle_ms INTEGER,
	client_payload JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_request_metric_created_at
	ON _request_metric(_created_at DESC);

CREATE INDEX IF NOT EXISTS idx_request_metric_path_created_at
	ON _request_metric(path, _created_at DESC);

CREATE INDEX IF NOT EXISTS idx_request_metric_source_created_at
	ON _request_metric(request_source, _created_at DESC);

CREATE INDEX IF NOT EXISTS idx_request_metric_status_created_at
	ON _request_metric(status, _created_at DESC);

CREATE INDEX IF NOT EXISTS idx_request_metric_user_created_at
	ON _request_metric(user_id, _created_at DESC);
