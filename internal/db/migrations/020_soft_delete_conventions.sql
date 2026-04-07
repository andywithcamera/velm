ALTER TABLE IF EXISTS work_item
	ADD COLUMN IF NOT EXISTS _deleted_at TIMESTAMPTZ,
	ADD COLUMN IF NOT EXISTS _deleted_by UUID;

ALTER TABLE IF EXISTS asset_item
	ADD COLUMN IF NOT EXISTS _deleted_at TIMESTAMPTZ,
	ADD COLUMN IF NOT EXISTS _deleted_by UUID;

DO $$
BEGIN
	IF to_regclass('work_item') IS NOT NULL THEN
		CREATE INDEX IF NOT EXISTS idx_work_item_deleted_at ON work_item(_deleted_at);
	END IF;
	IF to_regclass('asset_item') IS NOT NULL THEN
		CREATE INDEX IF NOT EXISTS idx_asset_item_deleted_at ON asset_item(_deleted_at);
	END IF;
END
$$;
