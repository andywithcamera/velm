ALTER TABLE _app
	DROP CONSTRAINT IF EXISTS chk_app_namespace;

ALTER TABLE _app
	ADD CONSTRAINT chk_app_namespace CHECK (
		namespace = '' OR namespace ~ '^[a-z][a-z0-9_]{1,62}$'
	);

INSERT INTO _app (
	name,
	namespace,
	label,
	description,
	status,
	definition_yaml,
	published_definition_yaml,
	definition_version,
	published_version
)
VALUES (
	'system',
	'',
	'System',
	'Core platform tables and views.',
	'active',
	trim($yaml$
name: system
namespace: ""
label: System
description: Core platform tables and views.
tables:
  - name: _user
    label_singular: User
    label_plural: Users
    description: Platform user accounts.
    display_field: name
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _created_by
        label: Created By
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_by
        label: Updated By
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: email
        label: Email
        data_type: email
        is_nullable: false
      - name: password_hash
        label: Password Hash
        data_type: long_text
        is_nullable: true
    forms:
      - name: default
        label: Default
        fields:
          - name
          - email
          - password_hash
    lists:
      - name: default
        label: Default
        columns:
          - name
          - email
          - _updated_at
  - name: _group
    label_singular: Group
    label_plural: Groups
    description: User groups used for role assignment.
    display_field: name
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: is_system
        label: Is System
        data_type: boolean
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - name
          - description
          - is_system
    lists:
      - name: default
        label: Default
        columns:
          - name
          - description
          - is_system
  - name: _group_membership
    label_singular: Group Membership
    label_plural: Group Memberships
    description: User-to-group assignments.
    display_field: user_id
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: group_id
        label: Group
        data_type: reference
        is_nullable: false
        reference_table: _group
      - name: user_id
        label: User
        data_type: reference
        is_nullable: false
        reference_table: _user
    forms:
      - name: default
        label: Default
        fields:
          - group_id
          - user_id
    lists:
      - name: default
        label: Default
        columns:
          - group_id
          - user_id
          - _created_at
  - name: _group_role
    label_singular: Role Assignment
    label_plural: Role Assignments
    description: Group-to-role assignments.
    display_field: group_id
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: group_id
        label: Group
        data_type: reference
        is_nullable: false
        reference_table: _group
      - name: role_id
        label: Role
        data_type: reference
        is_nullable: false
        reference_table: _role
      - name: app_id
        label: App
        data_type: text
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - group_id
          - role_id
          - app_id
    lists:
      - name: default
        label: Default
        columns:
          - group_id
          - role_id
          - app_id
  - name: _role
    label_singular: Role
    label_plural: Roles
    description: Authorization roles.
    display_field: name
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: is_system
        label: Is System
        data_type: boolean
        is_nullable: false
      - name: priority
        label: Priority
        data_type: integer
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - name
          - description
          - is_system
          - priority
    lists:
      - name: default
        label: Default
        columns:
          - name
          - description
          - priority
          - is_system
  - name: _role_inheritance
    label_singular: Role Inheritance
    label_plural: Role Inheritance
    description: Role-to-role inheritance links.
    display_field: role_id
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: role_id
        label: Role
        data_type: reference
        is_nullable: false
        reference_table: _role
      - name: inherits_role_id
        label: Inherits Role
        data_type: reference
        is_nullable: false
        reference_table: _role
    forms:
      - name: default
        label: Default
        fields:
          - role_id
          - inherits_role_id
    lists:
      - name: default
        label: Default
        columns:
          - role_id
          - inherits_role_id
  - name: _permission
    label_singular: Permission
    label_plural: Permissions
    description: Authorization permissions.
    display_field: description
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: resource
        label: Resource
        data_type: text
        is_nullable: false
      - name: action
        label: Action
        data_type: text
        is_nullable: false
      - name: scope
        label: Scope
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
    forms:
      - name: default
        label: Default
        fields:
          - resource
          - action
          - scope
          - description
    lists:
      - name: default
        label: Default
        columns:
          - resource
          - action
          - scope
          - description
  - name: _role_permission
    label_singular: Role Permission
    label_plural: Role Permissions
    description: Role-to-permission assignments.
    display_field: role_id
    columns:
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: role_id
        label: Role
        data_type: reference
        is_nullable: false
        reference_table: _role
      - name: permission_id
        label: Permission
        data_type: reference
        is_nullable: false
        reference_table: _permission
    forms:
      - name: default
        label: Default
        fields:
          - role_id
          - permission_id
    lists:
      - name: default
        label: Default
        columns:
          - role_id
          - permission_id
          - _created_at
  - name: _user_role
    label_singular: User Role
    label_plural: User Roles
    description: Direct user-to-role assignments.
    display_field: user_id
    columns:
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: user_id
        label: User
        data_type: reference
        is_nullable: false
        reference_table: _user
      - name: role_id
        label: Role
        data_type: reference
        is_nullable: false
        reference_table: _role
      - name: app_id
        label: App
        data_type: text
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - user_id
          - role_id
          - app_id
    lists:
      - name: default
        label: Default
        columns:
          - user_id
          - role_id
          - app_id
  - name: _property
    label_singular: Property
    label_plural: Properties
    description: System runtime properties.
    display_field: key
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _created_by
        label: Created By
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_by
        label: Updated By
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: key
        label: Key
        data_type: text
        is_nullable: true
      - name: value
        label: Value
        data_type: long_text
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - key
          - value
    lists:
      - name: default
        label: Default
        columns:
          - key
          - value
          - _updated_at
  - name: _user_preference
    label_singular: Preference
    label_plural: Preferences
    description: Per-user interface preferences.
    display_field: pref_key
    columns:
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: user_id
        label: User
        data_type: reference
        is_nullable: false
        reference_table: _user
      - name: namespace
        label: Namespace
        data_type: text
        is_nullable: false
      - name: pref_key
        label: Preference Key
        data_type: text
        is_nullable: false
      - name: pref_value
        label: Preference Value
        data_type: jsonb
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - user_id
          - namespace
          - pref_key
          - pref_value
    lists:
      - name: default
        label: Default
        columns:
          - user_id
          - namespace
          - pref_key
          - _updated_at
$yaml$),
	trim($yaml$
name: system
namespace: ""
label: System
description: Core platform tables and views.
tables:
  - name: _user
    label_singular: User
    label_plural: Users
    description: Platform user accounts.
    display_field: name
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _created_by
        label: Created By
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_by
        label: Updated By
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: email
        label: Email
        data_type: email
        is_nullable: false
      - name: password_hash
        label: Password Hash
        data_type: long_text
        is_nullable: true
    forms:
      - name: default
        label: Default
        fields:
          - name
          - email
          - password_hash
    lists:
      - name: default
        label: Default
        columns:
          - name
          - email
          - _updated_at
  - name: _group
    label_singular: Group
    label_plural: Groups
    description: User groups used for role assignment.
    display_field: name
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: is_system
        label: Is System
        data_type: boolean
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - name
          - description
          - is_system
    lists:
      - name: default
        label: Default
        columns:
          - name
          - description
          - is_system
  - name: _group_membership
    label_singular: Group Membership
    label_plural: Group Memberships
    description: User-to-group assignments.
    display_field: user_id
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: group_id
        label: Group
        data_type: reference
        is_nullable: false
        reference_table: _group
      - name: user_id
        label: User
        data_type: reference
        is_nullable: false
        reference_table: _user
    forms:
      - name: default
        label: Default
        fields:
          - group_id
          - user_id
    lists:
      - name: default
        label: Default
        columns:
          - group_id
          - user_id
          - _created_at
  - name: _group_role
    label_singular: Role Assignment
    label_plural: Role Assignments
    description: Group-to-role assignments.
    display_field: group_id
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: group_id
        label: Group
        data_type: reference
        is_nullable: false
        reference_table: _group
      - name: role_id
        label: Role
        data_type: reference
        is_nullable: false
        reference_table: _role
      - name: app_id
        label: App
        data_type: text
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - group_id
          - role_id
          - app_id
    lists:
      - name: default
        label: Default
        columns:
          - group_id
          - role_id
          - app_id
  - name: _role
    label_singular: Role
    label_plural: Roles
    description: Authorization roles.
    display_field: name
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
      - name: is_system
        label: Is System
        data_type: boolean
        is_nullable: false
      - name: priority
        label: Priority
        data_type: integer
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - name
          - description
          - is_system
          - priority
    lists:
      - name: default
        label: Default
        columns:
          - name
          - description
          - priority
          - is_system
  - name: _role_inheritance
    label_singular: Role Inheritance
    label_plural: Role Inheritance
    description: Role-to-role inheritance links.
    display_field: role_id
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: role_id
        label: Role
        data_type: reference
        is_nullable: false
        reference_table: _role
      - name: inherits_role_id
        label: Inherits Role
        data_type: reference
        is_nullable: false
        reference_table: _role
    forms:
      - name: default
        label: Default
        fields:
          - role_id
          - inherits_role_id
    lists:
      - name: default
        label: Default
        columns:
          - role_id
          - inherits_role_id
  - name: _permission
    label_singular: Permission
    label_plural: Permissions
    description: Authorization permissions.
    display_field: description
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: resource
        label: Resource
        data_type: text
        is_nullable: false
      - name: action
        label: Action
        data_type: text
        is_nullable: false
      - name: scope
        label: Scope
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: long_text
        is_nullable: true
    forms:
      - name: default
        label: Default
        fields:
          - resource
          - action
          - scope
          - description
    lists:
      - name: default
        label: Default
        columns:
          - resource
          - action
          - scope
          - description
  - name: _role_permission
    label_singular: Role Permission
    label_plural: Role Permissions
    description: Role-to-permission assignments.
    display_field: role_id
    columns:
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: role_id
        label: Role
        data_type: reference
        is_nullable: false
        reference_table: _role
      - name: permission_id
        label: Permission
        data_type: reference
        is_nullable: false
        reference_table: _permission
    forms:
      - name: default
        label: Default
        fields:
          - role_id
          - permission_id
    lists:
      - name: default
        label: Default
        columns:
          - role_id
          - permission_id
          - _created_at
  - name: _user_role
    label_singular: User Role
    label_plural: User Roles
    description: Direct user-to-role assignments.
    display_field: user_id
    columns:
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: user_id
        label: User
        data_type: reference
        is_nullable: false
        reference_table: _user
      - name: role_id
        label: Role
        data_type: reference
        is_nullable: false
        reference_table: _role
      - name: app_id
        label: App
        data_type: text
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - user_id
          - role_id
          - app_id
    lists:
      - name: default
        label: Default
        columns:
          - user_id
          - role_id
          - app_id
  - name: _property
    label_singular: Property
    label_plural: Properties
    description: System runtime properties.
    display_field: key
    columns:
      - name: _id
        label: ID
        data_type: uuid
        is_nullable: false
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _created_by
        label: Created By
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_by
        label: Updated By
        data_type: reference
        is_nullable: true
        reference_table: _user
      - name: key
        label: Key
        data_type: text
        is_nullable: true
      - name: value
        label: Value
        data_type: long_text
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - key
          - value
    lists:
      - name: default
        label: Default
        columns:
          - key
          - value
          - _updated_at
  - name: _user_preference
    label_singular: Preference
    label_plural: Preferences
    description: Per-user interface preferences.
    display_field: pref_key
    columns:
      - name: _created_at
        label: Created At
        data_type: timestamptz
        is_nullable: false
      - name: _updated_at
        label: Updated At
        data_type: timestamptz
        is_nullable: false
      - name: user_id
        label: User
        data_type: reference
        is_nullable: false
        reference_table: _user
      - name: namespace
        label: Namespace
        data_type: text
        is_nullable: false
      - name: pref_key
        label: Preference Key
        data_type: text
        is_nullable: false
      - name: pref_value
        label: Preference Value
        data_type: jsonb
        is_nullable: false
    forms:
      - name: default
        label: Default
        fields:
          - user_id
          - namespace
          - pref_key
          - pref_value
    lists:
      - name: default
        label: Default
        columns:
          - user_id
          - namespace
          - pref_key
          - _updated_at
$yaml$),
	1,
	1
)
ON CONFLICT (name) DO UPDATE
SET namespace = EXCLUDED.namespace,
	label = EXCLUDED.label,
	description = EXCLUDED.description,
	status = EXCLUDED.status,
	definition_yaml = EXCLUDED.definition_yaml,
	published_definition_yaml = EXCLUDED.published_definition_yaml,
	definition_version = GREATEST(_app.definition_version, 1),
	published_version = GREATEST(_app.published_version, 1),
	_updated_at = NOW();
