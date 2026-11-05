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
--
-- Every pattern below is anchored to explicit "\n" sequences and to the exact
-- indentation of the block it targets. An earlier revision used "\s+", which
-- matches newlines too: it swallowed the password_hash block and injected the
-- ref-column visibility rules on top of the following "- name:" line, leaving
-- a draft definition that no longer parsed ("mapping values are not allowed in
-- this context"). The draft is read by MigrateLegacyScriptDefinitions at
-- startup, so the corruption only surfaced on a database that ran the whole
-- migration set from empty.
-- Both columns get the rewritten definition. A plain
-- "published_definition_yaml = definition_yaml" does NOT do that in Postgres:
-- every SET expression reads the pre-UPDATE row, so published would keep the
-- old text and the app would keep serving a definition without the new fields.
UPDATE _app
SET
	definition_yaml          = fixed.definition_yaml,
	published_definition_yaml = fixed.definition_yaml,
	definition_version       = definition_version + 1,
	published_version        = published_version  + 1,
	_updated_at              = NOW()
FROM (
	SELECT _id, regexp_replace(
		regexp_replace(
			regexp_replace(
				regexp_replace(
					regexp_replace(
						definition_yaml,
						E'(      - name: password_hash\n        label: Password Hash\n        data_type: long_text\n        is_nullable: true\n)',
						E'\\1      - name: user_type\n        label: User Type\n        data_type: choice\n        is_nullable: false\n        default_value: human\n        choices:\n          - value: human\n            label: Human\n          - value: ai_agent\n            label: AI Agent\n          - value: api\n            label: API\n          - value: automation\n            label: Automation\n      - name: ai_connection_id\n        label: AI Connection\n        data_type: reference\n        is_nullable: true\n        reference_table: _ai_connection\n      - name: ai_model_id\n        label: Default AI Model\n        data_type: reference\n        is_nullable: true\n        reference_table: _ai_model\n',
						'g'
					),
					E'(          - name: password_hash\n)',
					E'\\1          - user_type\n          - ai_connection_id\n          - ai_model_id\n',
					'g'
				),
				E'(        columns:\n          - name\n          - email\n          - _updated_at\n)',
				E'\\1          - user_type\n',
				'g'
			),
			E'(      - name: ai_connection_id\n        label: AI Connection\n        data_type: reference\n        is_nullable: true\n        reference_table: _ai_connection\n)',
			E'\\1        is_visible_when:\n          field: user_type\n          equals: ai_agent\n',
			'g'
		),
		E'(      - name: ai_model_id\n        label: Default AI Model\n        data_type: reference\n        is_nullable: true\n        reference_table: _ai_model\n)',
		E'\\1        is_visible_when:\n          field: user_type\n          equals: ai_agent\n',
		'g'
	) AS definition_yaml
	FROM _app
	WHERE name = 'system'
) AS fixed
WHERE _app._id = fixed._id;
