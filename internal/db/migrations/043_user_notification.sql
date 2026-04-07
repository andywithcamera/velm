CREATE TABLE IF NOT EXISTS _user_notification (
	_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	user_id UUID NOT NULL REFERENCES _user(_id) ON DELETE CASCADE,
	title TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	href TEXT NOT NULL DEFAULT '',
	level TEXT NOT NULL DEFAULT 'info',
	read_at TIMESTAMPTZ,
	_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	_deleted_at TIMESTAMPTZ,
	_created_by UUID REFERENCES _user(_id) ON DELETE SET NULL,
	_updated_by UUID REFERENCES _user(_id) ON DELETE SET NULL,
	_deleted_by UUID REFERENCES _user(_id) ON DELETE SET NULL,
	CONSTRAINT chk__user_notification_level CHECK (level IN ('info', 'success', 'warning', 'error'))
);

CREATE INDEX IF NOT EXISTS idx_user_notification_user_created
	ON _user_notification(user_id, _created_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_notification_user_unread
	ON _user_notification(user_id, read_at, _created_at DESC)
	WHERE _deleted_at IS NULL;
