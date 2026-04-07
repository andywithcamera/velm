CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE _role ADD COLUMN IF NOT EXISTS _id_uuid UUID;
UPDATE _role SET _id_uuid = gen_random_uuid() WHERE _id_uuid IS NULL;
ALTER TABLE _role ALTER COLUMN _id_uuid SET NOT NULL;
ALTER TABLE _role ALTER COLUMN _id_uuid SET DEFAULT gen_random_uuid();

ALTER TABLE _permission ADD COLUMN IF NOT EXISTS _id_uuid UUID;
UPDATE _permission SET _id_uuid = gen_random_uuid() WHERE _id_uuid IS NULL;
ALTER TABLE _permission ALTER COLUMN _id_uuid SET NOT NULL;
ALTER TABLE _permission ALTER COLUMN _id_uuid SET DEFAULT gen_random_uuid();

ALTER TABLE _group ADD COLUMN IF NOT EXISTS _id_uuid UUID;
UPDATE _group SET _id_uuid = gen_random_uuid() WHERE _id_uuid IS NULL;
ALTER TABLE _group ALTER COLUMN _id_uuid SET NOT NULL;
ALTER TABLE _group ALTER COLUMN _id_uuid SET DEFAULT gen_random_uuid();

ALTER TABLE _group_membership ADD COLUMN IF NOT EXISTS _id_uuid UUID;
UPDATE _group_membership SET _id_uuid = gen_random_uuid() WHERE _id_uuid IS NULL;
ALTER TABLE _group_membership ALTER COLUMN _id_uuid SET NOT NULL;
ALTER TABLE _group_membership ALTER COLUMN _id_uuid SET DEFAULT gen_random_uuid();

ALTER TABLE _group_role ADD COLUMN IF NOT EXISTS _id_uuid UUID;
UPDATE _group_role SET _id_uuid = gen_random_uuid() WHERE _id_uuid IS NULL;
ALTER TABLE _group_role ALTER COLUMN _id_uuid SET NOT NULL;
ALTER TABLE _group_role ALTER COLUMN _id_uuid SET DEFAULT gen_random_uuid();

ALTER TABLE _role_inheritance ADD COLUMN IF NOT EXISTS _id_uuid UUID;
UPDATE _role_inheritance SET _id_uuid = gen_random_uuid() WHERE _id_uuid IS NULL;
ALTER TABLE _role_inheritance ALTER COLUMN _id_uuid SET NOT NULL;
ALTER TABLE _role_inheritance ALTER COLUMN _id_uuid SET DEFAULT gen_random_uuid();

ALTER TABLE _group_membership ADD COLUMN IF NOT EXISTS group_id_uuid UUID;
UPDATE _group_membership gm
SET group_id_uuid = g._id_uuid
FROM _group g
WHERE gm.group_id = g._id
  AND gm.group_id_uuid IS NULL;
ALTER TABLE _group_membership ALTER COLUMN group_id_uuid SET NOT NULL;

ALTER TABLE _group_role ADD COLUMN IF NOT EXISTS group_id_uuid UUID;
UPDATE _group_role gr
SET group_id_uuid = g._id_uuid
FROM _group g
WHERE gr.group_id = g._id
  AND gr.group_id_uuid IS NULL;
ALTER TABLE _group_role ALTER COLUMN group_id_uuid SET NOT NULL;

ALTER TABLE _group_role ADD COLUMN IF NOT EXISTS role_id_uuid UUID;
UPDATE _group_role gr
SET role_id_uuid = r._id_uuid
FROM _role r
WHERE gr.role_id = r._id
  AND gr.role_id_uuid IS NULL;
ALTER TABLE _group_role ALTER COLUMN role_id_uuid SET NOT NULL;

ALTER TABLE _role_inheritance ADD COLUMN IF NOT EXISTS role_id_uuid UUID;
UPDATE _role_inheritance ri
SET role_id_uuid = r._id_uuid
FROM _role r
WHERE ri.role_id = r._id
  AND ri.role_id_uuid IS NULL;
ALTER TABLE _role_inheritance ALTER COLUMN role_id_uuid SET NOT NULL;

ALTER TABLE _role_inheritance ADD COLUMN IF NOT EXISTS inherits_role_id_uuid UUID;
UPDATE _role_inheritance ri
SET inherits_role_id_uuid = r._id_uuid
FROM _role r
WHERE ri.inherits_role_id = r._id
  AND ri.inherits_role_id_uuid IS NULL;
ALTER TABLE _role_inheritance ALTER COLUMN inherits_role_id_uuid SET NOT NULL;

ALTER TABLE _role_permission ADD COLUMN IF NOT EXISTS role_id_uuid UUID;
UPDATE _role_permission rp
SET role_id_uuid = r._id_uuid
FROM _role r
WHERE rp.role_id = r._id
  AND rp.role_id_uuid IS NULL;
ALTER TABLE _role_permission ALTER COLUMN role_id_uuid SET NOT NULL;

ALTER TABLE _role_permission ADD COLUMN IF NOT EXISTS permission_id_uuid UUID;
UPDATE _role_permission rp
SET permission_id_uuid = p._id_uuid
FROM _permission p
WHERE rp.permission_id = p._id
  AND rp.permission_id_uuid IS NULL;
ALTER TABLE _role_permission ALTER COLUMN permission_id_uuid SET NOT NULL;

ALTER TABLE _user_role ADD COLUMN IF NOT EXISTS role_id_uuid UUID;
UPDATE _user_role ur
SET role_id_uuid = r._id_uuid
FROM _role r
WHERE ur.role_id = r._id
  AND ur.role_id_uuid IS NULL;
ALTER TABLE _user_role ALTER COLUMN role_id_uuid SET NOT NULL;

ALTER TABLE _role_permission DROP CONSTRAINT IF EXISTS _role_permission_role_id_fkey;
ALTER TABLE _role_permission DROP CONSTRAINT IF EXISTS _role_permission_permission_id_fkey;
ALTER TABLE _role_permission DROP CONSTRAINT IF EXISTS _role_permission_pkey;

ALTER TABLE _user_role DROP CONSTRAINT IF EXISTS _user_role_role_id_fkey;
ALTER TABLE _user_role DROP CONSTRAINT IF EXISTS _user_role_pkey;

ALTER TABLE _group_membership DROP CONSTRAINT IF EXISTS _group_membership_group_id_fkey;
ALTER TABLE _group_membership DROP CONSTRAINT IF EXISTS _group_membership_group_id_user_id_key;
ALTER TABLE _group_membership DROP CONSTRAINT IF EXISTS _group_membership_pkey;

ALTER TABLE _group_role DROP CONSTRAINT IF EXISTS _group_role_group_id_fkey;
ALTER TABLE _group_role DROP CONSTRAINT IF EXISTS _group_role_role_id_fkey;
ALTER TABLE _group_role DROP CONSTRAINT IF EXISTS _group_role_group_id_role_id_app_id_key;
ALTER TABLE _group_role DROP CONSTRAINT IF EXISTS _group_role_pkey;

ALTER TABLE _role_inheritance DROP CONSTRAINT IF EXISTS _role_inheritance_role_id_fkey;
ALTER TABLE _role_inheritance DROP CONSTRAINT IF EXISTS _role_inheritance_inherits_role_id_fkey;
ALTER TABLE _role_inheritance DROP CONSTRAINT IF EXISTS _role_inheritance_role_id_inherits_role_id_key;
ALTER TABLE _role_inheritance DROP CONSTRAINT IF EXISTS chk_role_inheritance_not_self;
ALTER TABLE _role_inheritance DROP CONSTRAINT IF EXISTS _role_inheritance_pkey;

ALTER TABLE _group DROP CONSTRAINT IF EXISTS _group_pkey;
ALTER TABLE _role DROP CONSTRAINT IF EXISTS _role_pkey;
ALTER TABLE _permission DROP CONSTRAINT IF EXISTS _permission_pkey;

DROP INDEX IF EXISTS idx_group_membership_group_id;
DROP INDEX IF EXISTS idx_group_role_group_id;
DROP INDEX IF EXISTS idx_group_role_role_id;
DROP INDEX IF EXISTS idx_role_inheritance_role_id;
DROP INDEX IF EXISTS idx_role_inheritance_inherits_role_id;

ALTER TABLE _group DROP COLUMN _id;
ALTER TABLE _group RENAME COLUMN _id_uuid TO _id;
ALTER TABLE _group ADD CONSTRAINT _group_pkey PRIMARY KEY (_id);

ALTER TABLE _role DROP COLUMN _id;
ALTER TABLE _role RENAME COLUMN _id_uuid TO _id;
ALTER TABLE _role ADD CONSTRAINT _role_pkey PRIMARY KEY (_id);

ALTER TABLE _permission DROP COLUMN _id;
ALTER TABLE _permission RENAME COLUMN _id_uuid TO _id;
ALTER TABLE _permission ADD CONSTRAINT _permission_pkey PRIMARY KEY (_id);

ALTER TABLE _group_membership DROP COLUMN _id;
ALTER TABLE _group_membership RENAME COLUMN _id_uuid TO _id;
ALTER TABLE _group_membership DROP COLUMN group_id;
ALTER TABLE _group_membership RENAME COLUMN group_id_uuid TO group_id;
ALTER TABLE _group_membership ADD CONSTRAINT _group_membership_pkey PRIMARY KEY (_id);
ALTER TABLE _group_membership ADD CONSTRAINT _group_membership_group_id_user_id_key UNIQUE (group_id, user_id);
ALTER TABLE _group_membership ADD CONSTRAINT _group_membership_group_id_fkey FOREIGN KEY (group_id) REFERENCES _group(_id) ON DELETE CASCADE;

ALTER TABLE _group_role DROP COLUMN _id;
ALTER TABLE _group_role RENAME COLUMN _id_uuid TO _id;
ALTER TABLE _group_role DROP COLUMN group_id;
ALTER TABLE _group_role RENAME COLUMN group_id_uuid TO group_id;
ALTER TABLE _group_role DROP COLUMN role_id;
ALTER TABLE _group_role RENAME COLUMN role_id_uuid TO role_id;
ALTER TABLE _group_role ADD CONSTRAINT _group_role_pkey PRIMARY KEY (_id);
ALTER TABLE _group_role ADD CONSTRAINT _group_role_group_id_role_id_app_id_key UNIQUE (group_id, role_id, app_id);
ALTER TABLE _group_role ADD CONSTRAINT _group_role_group_id_fkey FOREIGN KEY (group_id) REFERENCES _group(_id) ON DELETE CASCADE;
ALTER TABLE _group_role ADD CONSTRAINT _group_role_role_id_fkey FOREIGN KEY (role_id) REFERENCES _role(_id) ON DELETE CASCADE;

ALTER TABLE _role_inheritance DROP COLUMN _id;
ALTER TABLE _role_inheritance RENAME COLUMN _id_uuid TO _id;
ALTER TABLE _role_inheritance DROP COLUMN role_id;
ALTER TABLE _role_inheritance RENAME COLUMN role_id_uuid TO role_id;
ALTER TABLE _role_inheritance DROP COLUMN inherits_role_id;
ALTER TABLE _role_inheritance RENAME COLUMN inherits_role_id_uuid TO inherits_role_id;
ALTER TABLE _role_inheritance ADD CONSTRAINT _role_inheritance_pkey PRIMARY KEY (_id);
ALTER TABLE _role_inheritance ADD CONSTRAINT chk_role_inheritance_not_self CHECK (role_id <> inherits_role_id);
ALTER TABLE _role_inheritance ADD CONSTRAINT _role_inheritance_role_id_inherits_role_id_key UNIQUE (role_id, inherits_role_id);
ALTER TABLE _role_inheritance ADD CONSTRAINT _role_inheritance_role_id_fkey FOREIGN KEY (role_id) REFERENCES _role(_id) ON DELETE CASCADE;
ALTER TABLE _role_inheritance ADD CONSTRAINT _role_inheritance_inherits_role_id_fkey FOREIGN KEY (inherits_role_id) REFERENCES _role(_id) ON DELETE CASCADE;

ALTER TABLE _role_permission DROP COLUMN role_id;
ALTER TABLE _role_permission RENAME COLUMN role_id_uuid TO role_id;
ALTER TABLE _role_permission DROP COLUMN permission_id;
ALTER TABLE _role_permission RENAME COLUMN permission_id_uuid TO permission_id;
ALTER TABLE _role_permission ADD CONSTRAINT _role_permission_pkey PRIMARY KEY (role_id, permission_id);
ALTER TABLE _role_permission ADD CONSTRAINT _role_permission_role_id_fkey FOREIGN KEY (role_id) REFERENCES _role(_id) ON DELETE CASCADE;
ALTER TABLE _role_permission ADD CONSTRAINT _role_permission_permission_id_fkey FOREIGN KEY (permission_id) REFERENCES _permission(_id) ON DELETE CASCADE;

ALTER TABLE _user_role DROP COLUMN role_id;
ALTER TABLE _user_role RENAME COLUMN role_id_uuid TO role_id;
ALTER TABLE _user_role ADD CONSTRAINT _user_role_pkey PRIMARY KEY (user_id, role_id, app_id);
ALTER TABLE _user_role ADD CONSTRAINT _user_role_role_id_fkey FOREIGN KEY (role_id) REFERENCES _role(_id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_group_membership_group_id ON _group_membership(group_id);
CREATE INDEX IF NOT EXISTS idx_group_membership_user_id ON _group_membership(user_id);
CREATE INDEX IF NOT EXISTS idx_group_role_group_id ON _group_role(group_id);
CREATE INDEX IF NOT EXISTS idx_group_role_role_id ON _group_role(role_id);
CREATE INDEX IF NOT EXISTS idx_group_role_app_id ON _group_role(app_id);
CREATE INDEX IF NOT EXISTS idx_role_inheritance_role_id ON _role_inheritance(role_id);
CREATE INDEX IF NOT EXISTS idx_role_inheritance_inherits_role_id ON _role_inheritance(inherits_role_id);
