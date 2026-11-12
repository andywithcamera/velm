-- Give every roleless user a role (issue #196).
--
-- 002_authz_core.sql backfilled admin onto the users that existed when it ran,
-- and 083 moved grants onto groups, but nothing has ever assigned a role to a
-- user created since. Such a user signs in successfully and then every page
-- redirects to /forbidden, because zero roles means every permission check
-- fails, including the read-only ones.
--
-- The fix in Go gives new users a membership of the "All users" system group,
-- which carries the viewer role. This migration creates that group and puts
-- the users who are already stuck in it, so an existing inert account starts
-- working on deploy instead of waiting for an admin to find it by hand.
--
-- Scoped to users with no global role of any kind: a user who already holds a
-- grant keeps exactly the roles they had.

INSERT INTO _group (name, description, is_system)
VALUES (
    'All users',
    'Every user created through the UI joins this group so the account can sign in and read. Add roles here to grant them to all such users.',
    TRUE
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO _group_role (group_id, role_id, app_id)
SELECT g._id, r._id, ''
FROM _group g
JOIN _role r ON r.name = 'viewer'
WHERE g.name = 'All users'
ON CONFLICT (group_id, role_id, app_id) DO NOTHING;

INSERT INTO _group_membership (user_id, group_id)
SELECT u._id::text, g._id
FROM _user u
JOIN _group g ON g.name = 'All users'
WHERE NOT EXISTS (
    SELECT 1
    FROM _group_membership gm
    JOIN _group_role gr ON gr.group_id = gm.group_id
    WHERE gm.user_id = u._id::text
      AND gr.app_id = ''
)
ON CONFLICT (user_id, group_id) DO NOTHING;
