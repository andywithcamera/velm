-- Make "there is always at least one global admin" a property of the database
-- rather than a check in the callers that happen to remember it (issue #199 R2).
--
-- Admin authority arrives by two edges — a direct _user_role row and a
-- _group_membership -> _group_role path — is widened by _role_inheritance, and
-- is defined by holding the platform/admin/global permission. All five tables
-- are guarded, because dropping the permission off a role strands admins just
-- as effectively as deleting the membership that grants it.
-- The existing Go guard covers only RevokeUserRole and RevokeRolePermission.
-- Everything else that can empty the last admin wrote straight to the tables and
-- was never checked: removing yourself from the Administrators group, deleting
-- the group_role row, or deleting the group all go through the generic record
-- editor, which has no idea what an admin is. After #200 makes groups the
-- offered path that is the ordinary path, not an exotic one.
--
-- DEFERRABLE INITIALLY DEFERRED is load-bearing. A trigger that fired per row
-- would reject the legitimate "move admin from A to B" transaction at the
-- moment A is deleted, when B is not yet inserted. Deferred to COMMIT, the
-- transaction is judged on its net effect, which is the thing that matters.
--
-- Deleting every user is allowed: a database with no users has no admin to
-- strand and locking an operator out of a teardown helps nobody.

CREATE OR REPLACE FUNCTION _assert_global_admin_remains() RETURNS TRIGGER AS $$
DECLARE
    admin_count INTEGER;
    user_count INTEGER;
BEGIN
    WITH RECURSIVE seed_roles AS (
        SELECT ur.user_id, ur.role_id
        FROM _user_role ur
        WHERE ur.app_id = ''
        UNION
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

-- One trigger per edge. Each is DEFERRABLE INITIALLY DEFERRED, so the check runs
-- once at COMMIT regardless of how many rows the statement touched.
DROP TRIGGER IF EXISTS _user_role_last_admin_guard ON _user_role;
CREATE CONSTRAINT TRIGGER _user_role_last_admin_guard
    AFTER DELETE OR UPDATE OF user_id, role_id, app_id ON _user_role
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION _assert_global_admin_remains();

DROP TRIGGER IF EXISTS _group_membership_last_admin_guard ON _group_membership;
CREATE CONSTRAINT TRIGGER _group_membership_last_admin_guard
    AFTER DELETE OR UPDATE OF user_id, group_id ON _group_membership
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION _assert_global_admin_remains();

DROP TRIGGER IF EXISTS _group_role_last_admin_guard ON _group_role;
CREATE CONSTRAINT TRIGGER _group_role_last_admin_guard
    AFTER DELETE OR UPDATE OF group_id, role_id, app_id ON _group_role
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION _assert_global_admin_remains();

DROP TRIGGER IF EXISTS _role_permission_last_admin_guard ON _role_permission;
CREATE CONSTRAINT TRIGGER _role_permission_last_admin_guard
    AFTER DELETE OR UPDATE OF role_id, permission_id ON _role_permission
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION _assert_global_admin_remains();

DROP TRIGGER IF EXISTS _role_inheritance_last_admin_guard ON _role_inheritance;
CREATE CONSTRAINT TRIGGER _role_inheritance_last_admin_guard
    AFTER DELETE OR UPDATE OF role_id, inherits_role_id ON _role_inheritance
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION _assert_global_admin_remains();
