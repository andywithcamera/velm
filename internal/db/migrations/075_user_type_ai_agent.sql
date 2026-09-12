-- User type on _user (issue #110, decision B).
--
-- Every user gets an explicit type: human, ai_agent, api, or automation.
-- Type is stored on the record (visible in the user list UI), replacing
-- inference-from-token-rows. This supersedes the 070 comment that said
-- "the token table is the only place agent-ness lives": agent-ness is now
-- declared on the user; the token table remains purely authN.
--
-- AI agents additionally bind a connection + default model. These fields
-- appear in the form only when user_type = ai_agent.

ALTER TABLE _user
	ADD COLUMN IF NOT EXISTS user_type TEXT NOT NULL DEFAULT 'human',
	ADD COLUMN IF NOT EXISTS ai_connection_id UUID,
	ADD COLUMN IF NOT EXISTS ai_model_id UUID;

ALTER TABLE _user
	DROP CONSTRAINT IF EXISTS chk_user_user_type;
ALTER TABLE _user
	ADD CONSTRAINT chk_user_user_type
	CHECK (user_type IN ('human', 'ai_agent', 'api', 'automation'));
CREATE INDEX IF NOT EXISTS idx_user_user_type ON _user(user_type);

-- Seed existing API-token users as ai_agent (the only non-humans so far).
UPDATE _user
SET user_type = 'ai_agent'
WHERE user_type = 'human'
  AND EXISTS (SELECT 1 FROM _agent_api_token t WHERE t.user_id = _user._id::text);

-- System app definition: add the columns, form fields, and list columns.
UPDATE _app
SET
	definition_yaml = regexp_replace(
		regexp_replace(
			regexp_replace(
				regexp_replace(
					regexp_replace(
						definition_yaml,
						'(- name: password_hash\s+label: Password Hash\s+data_type: long_text\s+is_nullable: true)',
						E'\\1\n      - name: user_type\n        label: User Type\n        data_type: choice\n        is_nullable: false\n        default_value: human\n        choices:\n          - value: human\n            label: Human\n          - value: ai_agent\n            label: AI Agent\n          - value: api\n            label: API\n          - value: automation\n            label: Automation\n      - name: ai_connection_id\n        label: AI Connection\n        data_type: reference\n        is_nullable: true\n        reference_table: _ai_connection\n      - name: ai_model_id\n        label: Default AI Model\n        data_type: reference\n        is_nullable: true\n        reference_table: _ai_model',
						'g'
					),
					'(- name: password_hash\n)',
					E'\\1\n          - user_type\n          - ai_connection_id\n          - ai_model_id',
					'g'
				),
				'(columns:\n          - name\n          - email\n          - _updated_at)',
				E'\\1\n          - user_type',
				'g'
			),
			'(- name: ai_connection_id\n        label: AI Connection\n        data_type: reference\n        is_nullable: true\n        reference_table: _ai_connection\n)',
			E'\\1\n        is_visible_when:\n          field: user_type\n          equals: ai_agent',
			'g'
		),
		'(- name: ai_model_id\n        label: Default AI Model\n        data_type: reference\n        is_nullable: true\n        reference_table: _ai_model\n)',
		E'\\1\n        is_visible_when:\n          field: user_type\n          equals: ai_agent',
		'g'
	),
	published_definition_yaml = definition_yaml,
	definition_version    = definition_version    + 1,
	published_version     = published_version     + 1,
	_updated_at           = NOW()
WHERE name = 'system';
