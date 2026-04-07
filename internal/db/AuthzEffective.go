package db

const effectiveUserRolesCTE = `
WITH RECURSIVE seed_roles AS (
	SELECT ur.user_id, ur.role_id
	FROM _user_role ur
	WHERE ur.user_id = $1
	  AND (($3 = '' AND ur.app_id = '') OR ($3 <> '' AND (ur.app_id = '' OR ur.app_id = $3)))
	UNION
	SELECT gm.user_id, gr.role_id
	FROM _group_membership gm
	JOIN _group_role gr ON gr.group_id = gm.group_id
	WHERE gm.user_id = $1
	  AND (($3 = '' AND gr.app_id = '') OR ($3 <> '' AND (gr.app_id = '' OR gr.app_id = $3)))
),
effective_roles AS (
	SELECT sr.user_id, sr.role_id
	FROM seed_roles sr
	UNION
	SELECT er.user_id, ri.inherits_role_id
	FROM effective_roles er
	JOIN _role_inheritance ri ON ri.role_id = er.role_id
)
`

const effectiveUserRolesByAppCTE = `
WITH RECURSIVE seed_roles AS (
	SELECT ur.user_id, ur.role_id
	FROM _user_role ur
	WHERE ur.user_id = $1
	  AND (($2 = '' AND ur.app_id = '') OR ($2 <> '' AND (ur.app_id = '' OR ur.app_id = $2)))
	UNION
	SELECT gm.user_id, gr.role_id
	FROM _group_membership gm
	JOIN _group_role gr ON gr.group_id = gm.group_id
	WHERE gm.user_id = $1
	  AND (($2 = '' AND gr.app_id = '') OR ($2 <> '' AND (gr.app_id = '' OR gr.app_id = $2)))
),
effective_roles AS (
	SELECT sr.user_id, sr.role_id
	FROM seed_roles sr
	UNION
	SELECT er.user_id, ri.inherits_role_id
	FROM effective_roles er
	JOIN _role_inheritance ri ON ri.role_id = er.role_id
)
`

const effectiveGlobalAdminUsersCTE = `
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
`

const impactedUserIDsForRoleCTE = `
WITH RECURSIVE impacted_roles AS (
	SELECT $1::uuid AS role_id
	UNION
	SELECT ri.role_id
	FROM _role_inheritance ri
	JOIN impacted_roles ir ON ri.inherits_role_id = ir.role_id
),
impacted_users AS (
	SELECT DISTINCT ur.user_id
	FROM _user_role ur
	JOIN impacted_roles ir ON ir.role_id = ur.role_id
	UNION
	SELECT DISTINCT gm.user_id
	FROM _group_membership gm
	JOIN _group_role gr ON gr.group_id = gm.group_id
	JOIN impacted_roles ir ON ir.role_id = gr.role_id
)
`
