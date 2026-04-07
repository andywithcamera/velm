CREATE TABLE IF NOT EXISTS _bootstrap_admin (
	user_id TEXT PRIMARY KEY,
	email TEXT NOT NULL,
	bootstrapped_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DELETE FROM _user_role ur
USING _role r
WHERE ur.role_id = r._id
  AND r.name = 'admin'
  AND ur.app_id = ''
  AND NOT EXISTS (
    SELECT 1
    FROM _bootstrap_admin ba
    WHERE ba.user_id = ur.user_id
  );
