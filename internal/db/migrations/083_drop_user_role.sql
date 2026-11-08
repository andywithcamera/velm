-- Fold direct user-role grants into groups, then remove the table (issue #199 R4).
--
-- After R3 group membership is the only path the UI offers, but the table still
-- exists and existing instances still hold rows: velm.dev does. Dropping it
-- without a backfill would silently demote every user who holds a direct grant,
-- so this migration converts each (role, app) pair that has direct grants into a
-- group carrying that role, adds the grant holders as members, and only then
-- drops the table. Effective roles are unchanged by construction: the effective
-- CTE unions the two edges, and this swaps one edge for the other.
--
-- One group per (role, app) rather than per user: a per-user group would create
-- as many groups as there are grants and clutter the admin page, while a shared
-- group is the unit the UI now works in. Named so the origin is obvious and so
-- the group can be renamed or deleted like any other.

INSERT INTO _group (name, description, is_system)
SELECT DISTINCT
    'Migrated: ' || r.name || CASE WHEN ur.app_id = '' THEN '' ELSE ' (' || ur.app_id || ')' END,
    'Created by migration 083 to carry direct user-role grants that predate group-only roles. Safe to rename.',
    FALSE
FROM _user_role ur
JOIN _role r ON r._id = ur.role_id
ON CONFLICT (name) DO NOTHING;

INSERT INTO _group_role (group_id, role_id, app_id)
SELECT DISTINCT
    g._id,
    ur.role_id,
    ur.app_id
FROM _user_role ur
JOIN _role r ON r._id = ur.role_id
JOIN _group g ON g.name = 'Migrated: ' || r.name || CASE WHEN ur.app_id = '' THEN '' ELSE ' (' || ur.app_id || ')' END
ON CONFLICT (group_id, role_id, app_id) DO NOTHING;

INSERT INTO _group_membership (group_id, user_id)
SELECT DISTINCT
    g._id,
    ur.user_id
FROM _user_role ur
JOIN _role r ON r._id = ur.role_id
JOIN _group g ON g.name = 'Migrated: ' || r.name || CASE WHEN ur.app_id = '' THEN '' ELSE ' (' || ur.app_id || ')' END
ON CONFLICT (group_id, user_id) DO NOTHING;

-- The 082 guard reads _user_role. Redefine it against group membership only
-- before the table goes, or every later delete would fail on a missing relation.
CREATE OR REPLACE FUNCTION _assert_global_admin_remains() RETURNS TRIGGER AS $$
DECLARE
    admin_count INTEGER;
    user_count INTEGER;
BEGIN
    WITH RECURSIVE seed_roles AS (
        SELECT gm.user_id, gr.role_id
        FROM _group_membership gm
        JOIN _group_role gr ON gr.group_id = gm.group_id
        WHERE gr.app_id = ''
    ),
    effective_roles AS (
        SELECT sr.user_id, sr.role_id
        FROM seed_roles sr
        UNION
        SELECT er.user_id, ri.inherits_role_id
        FROM effective_roles er
        JOIN _role_inheritance ri ON ri.role_id = er.role_id
    )
    SELECT COUNT(DISTINCT er.user_id)
    INTO admin_count
    FROM effective_roles er
    JOIN _role_permission rp ON rp.role_id = er.role_id
    JOIN _permission p ON p._id = rp.permission_id
    WHERE p.resource = 'platform'
      AND p.action = 'admin'
      AND p.scope = 'global';

    IF admin_count > 0 THEN
        RETURN NULL;
    END IF;

    SELECT COUNT(*) INTO user_count FROM _user;

    IF user_count = 0 THEN
        RETURN NULL;
    END IF;

    RAISE EXCEPTION 'cannot remove the last global admin'
        USING ERRCODE = 'VL001',
              HINT = 'Grant the admin role to another user or group before removing this one.';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS _user_role_last_admin_guard ON _user_role;
DROP TABLE IF EXISTS _user_role;
