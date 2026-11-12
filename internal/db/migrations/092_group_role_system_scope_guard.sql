-- Refuse app-scoped role grants on system groups (issue #216).
--
-- Administrators carried the admin role with a non-empty app_id on velm.dev.
-- Every global-admin query filters gr.app_id = '' (effectiveUserRolesCTE), so
-- the grant resolved to nothing and members of the group had no permissions at
-- all: sign-in succeeded, then every page redirected to /forbidden. The last-
-- admin guard did not notice because it also counts only global grants, so the
-- instance was one delete away from a lockout.
--
-- No insert in this codebase writes a non-empty app_id into _group_role
-- (EnsureAdministratorsGroupTx, ensureDefaultUserRoleGroupTx and 083 all pass
-- ''), so the row arrived by a direct write or predates those paths. A Go-side
-- check would only cover the write paths that remember it, so the invariant
-- belongs in the database: a system group is a global construct, and a scoped
-- grant on one can never mean anything in the global context. The generic
-- record editor already writes to these tables, which is exactly the path that
-- has no idea what an admin is.
--
-- Repair first: drop the app-scoped admin grants that the trigger would now
-- reject. Only admin grants are touched here; the trigger covers the rest going
-- forward, so operator intent on other roles is not silently deleted by an
-- upgrade.

DELETE FROM _group_role gr
USING _group g, _role r
WHERE gr.group_id = g._id
  AND gr.role_id = r._id
  AND g.is_system
  AND r.name = 'admin'
  AND gr.app_id <> '';

CREATE OR REPLACE FUNCTION _assert_system_group_grant_is_global() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.app_id <> '' AND EXISTS (
        SELECT 1 FROM _group WHERE _id = NEW.group_id AND is_system
    ) THEN
        RAISE EXCEPTION
            'app-scoped role grant is not allowed on a system group (group_id=%, app_id=%)',
            NEW.group_id, NEW.app_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS _group_role_system_scope_guard ON _group_role;
CREATE TRIGGER _group_role_system_scope_guard
    BEFORE INSERT OR UPDATE OF group_id, role_id, app_id ON _group_role
    FOR EACH ROW
    EXECUTE FUNCTION _assert_system_group_grant_is_global();
