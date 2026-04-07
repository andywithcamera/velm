-- Migration kept for compatibility with existing migration order.
-- Core `work_item` and `asset_item` schema is defined in 013.
DO $$
BEGIN
	RAISE NOTICE '015_work_item_core: no-op (core schema now managed in migration 013)';
END
$$;
