-- 103: per-attempt records for the `retry` control block (Process Designer,
-- Plan slice 5 / Requirements AC-75, AC-86).
--
-- A retry block re-runs its scope as a unit. Every attempt needs its own row
-- so the run says how many attempts it took, how long each lasted and why the
-- failed ones failed: `attempts_used` is this row count, never a counter kept
-- in variables that a restart could lose.
--
-- block_task_id is the CONTROL run row of the retry block itself; deleting
-- that row (a test cleaning up, or a future purge) takes its attempts with it.
CREATE TABLE IF NOT EXISTS base_task_block_attempt (
	block_task_id UUID NOT NULL REFERENCES base_task(_id) ON DELETE CASCADE,
	attempt_no INTEGER NOT NULL CHECK (attempt_no >= 1),
	started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	duration_ms INTEGER,
	error TEXT,
	PRIMARY KEY (block_task_id, attempt_no)
);

CREATE INDEX IF NOT EXISTS idx_base_task_block_attempt_block
	ON base_task_block_attempt (block_task_id, attempt_no);
