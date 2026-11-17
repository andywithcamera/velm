-- 100: the round-robin cursor (Process Designer, Plan T5 / Requirements AC-48).
--
-- A per-group cursor, in its own table rather than a column on _group. _group
-- is a system-app table reconciled by BaseAppSync (042), so anything stored on
-- it can be overwritten by a sync; a cursor is runtime state and must not be.
-- One row per group, created on first use, advanced inside the spawn
-- transaction (AC-61, AC-62): two concurrent spawns serialise on this row's
-- FOR UPDATE lock, so they cannot both pick the same member, and a spawn that
-- rolls back leaves the cursor where it was.
--
-- group_id is UUID because _group._id is UUID (041 converted the authz tables
-- from BIGSERIAL). AC-48's BIGINT is the pre-041 shape; a BIGINT column could
-- not carry the foreign key it asks for.

CREATE TABLE IF NOT EXISTS base_assignment_cursor (
	group_id UUID PRIMARY KEY REFERENCES _group(_id) ON DELETE CASCADE,
	next_index BIGINT NOT NULL DEFAULT 0,
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
