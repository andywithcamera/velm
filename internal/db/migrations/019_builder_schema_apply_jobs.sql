CREATE TABLE IF NOT EXISTS _builder_schema_job (
	_id BIGSERIAL PRIMARY KEY,
	table_name TEXT NOT NULL,
	dry_run BOOLEAN NOT NULL DEFAULT TRUE,
	status TEXT NOT NULL DEFAULT 'planned',
	created_by TEXT,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	executed_at TIMESTAMPTZ,
	error_text TEXT,
	CONSTRAINT chk_builder_schema_job_status CHECK (status IN ('planned', 'applied', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_builder_schema_job_table_time
	ON _builder_schema_job(table_name, created_at DESC);

CREATE TABLE IF NOT EXISTS _builder_schema_job_step (
	_id BIGSERIAL PRIMARY KEY,
	job_id BIGINT NOT NULL REFERENCES _builder_schema_job(_id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	action TEXT NOT NULL,
	statement_sql TEXT NOT NULL,
	rollback_sql TEXT,
	status TEXT NOT NULL DEFAULT 'planned',
	error_text TEXT,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT chk_builder_schema_job_step_status CHECK (status IN ('planned', 'applied', 'failed', 'skipped')),
	UNIQUE(job_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_builder_schema_job_step_lookup
	ON _builder_schema_job_step(job_id, seq);
