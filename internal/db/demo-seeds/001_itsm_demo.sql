CREATE EXTENSION IF NOT EXISTS pgcrypto;

INSERT INTO _item (asset_type, number, name, description, status, criticality, owner, lifecycle_state)
SELECT
	'api_service',
	'AST-000001',
	'Payments API',
	'Primary payments processing API service.',
	'active',
	'high',
	'platform-team@company.local',
	'production'
WHERE to_regclass('_item') IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM _item WHERE number = 'AST-000001');

INSERT INTO _item (asset_type, number, name, description, status, criticality, owner, lifecycle_state)
SELECT
	'database',
	'AST-000002',
	'Primary PostgreSQL',
	'Core transactional database cluster.',
	'active',
	'critical',
	'dba-team@company.local',
	'production'
WHERE to_regclass('_item') IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM _item WHERE number = 'AST-000002');
