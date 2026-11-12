-- Chain-wide comment threads.
--
-- A task chain is one conversation: the requester asks, a drafter drafts, a
-- reviewer reviews, a publisher publishes. Until now _record_comment was keyed
-- (table_name, record_id), so each stage had its own thread and the reviewer at
-- stage four could not see what was said at stage one. A rejection with no
-- reason attached is the visible symptom.
--
-- thread_id names the conversation. For a task it is the root of its chain
-- (walk parent_task_id to the ancestor with no parent), so every stage of one
-- run shares a thread. For any other table the thread is the record itself and
-- thread_id is left NULL — comments there already behave correctly and this
-- migration must not change them.
--
-- Existing rows are backfilled by the recursive walk below, so a chain already
-- in flight gains the shared thread rather than starting empty.

ALTER TABLE _record_comment
	ADD COLUMN IF NOT EXISTS thread_id UUID;

CREATE INDEX IF NOT EXISTS idx_record_comment_thread
	ON _record_comment(thread_id, _created_at DESC);

-- Backfill: walk each task up to its chain root. Depth-capped so a cycle
-- (which the engine should never create, but a hand-edited row could) fails
-- to a stop rather than spinning.
WITH RECURSIVE chain AS (
	SELECT _id AS task_id, _id AS root_id, 0 AS depth
	FROM base_task
	WHERE parent_task_id IS NULL
	UNION ALL
	SELECT t._id, c.root_id, c.depth + 1
	FROM base_task t
	JOIN chain c ON t.parent_task_id = c.task_id
	WHERE c.depth < 100
)
UPDATE _record_comment rc
SET thread_id = chain.root_id
FROM chain
WHERE rc.table_name = 'base_task'
  AND rc.record_id = chain.task_id::text
  AND rc.thread_id IS NULL;

COMMENT ON COLUMN _record_comment.thread_id IS
	'Conversation identity. For base_task, the chain root, so every stage of one run shares a thread. NULL for other tables (the record is its own thread).';
