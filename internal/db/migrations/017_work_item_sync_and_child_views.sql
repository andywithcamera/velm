-- Remove legacy ITSM hardcoded schema artifacts so business apps can own their own tables.
DO $$
BEGIN
	-- Triggers
	DROP TRIGGER IF EXISTS trg_incident_ensure_work_item ON incident;
	DROP TRIGGER IF EXISTS trg_problem_ensure_work_item ON problem;
	DROP TRIGGER IF EXISTS trg_change_request_ensure_work_item ON change_request;
	DROP TRIGGER IF EXISTS trg_service_request_ensure_work_item ON service_request;
	DROP TRIGGER IF EXISTS trg_incident_sync_work_item ON incident;
	DROP TRIGGER IF EXISTS trg_problem_sync_work_item ON problem;
	DROP TRIGGER IF EXISTS trg_change_request_sync_work_item ON change_request;
	DROP TRIGGER IF EXISTS trg_service_request_sync_work_item ON service_request;

	-- Views
	DROP VIEW IF EXISTS incident_with_work_item;
	DROP VIEW IF EXISTS problem_with_work_item;
	DROP VIEW IF EXISTS change_request_with_work_item;
	DROP VIEW IF EXISTS service_request_with_work_item;

	-- Functions
	DROP FUNCTION IF EXISTS ensure_work_item_for_incident();
	DROP FUNCTION IF EXISTS ensure_work_item_for_problem();
	DROP FUNCTION IF EXISTS ensure_work_item_for_change_request();
	DROP FUNCTION IF EXISTS ensure_work_item_for_service_request();
	DROP FUNCTION IF EXISTS sync_work_item_from_incident();
	DROP FUNCTION IF EXISTS sync_work_item_from_problem();
	DROP FUNCTION IF EXISTS sync_work_item_from_change_request();
	DROP FUNCTION IF EXISTS sync_work_item_from_service_request();

	-- ITSM-specific link and app tables.
	DROP TABLE IF EXISTS incident_problem_link CASCADE;
	DROP TABLE IF EXISTS change_ci_impact CASCADE;
	DROP TABLE IF EXISTS cmdb_ci_relationship CASCADE;
	DROP TABLE IF EXISTS incident CASCADE;
	DROP TABLE IF EXISTS problem CASCADE;
	DROP TABLE IF EXISTS change_request CASCADE;
	DROP TABLE IF EXISTS service_request CASCADE;
	DROP TABLE IF EXISTS knowledge_article CASCADE;
	DROP TABLE IF EXISTS cmdb_ci CASCADE;

	-- Remove stale metadata/menu/page artifacts.
	IF to_regclass('_column') IS NOT NULL AND to_regclass('_table') IS NOT NULL THEN
		DELETE FROM _column
		WHERE table_id IN (
			SELECT _id
			FROM _table
			WHERE name IN (
				'cmdb_ci',
				'incident',
				'problem',
				'change_request',
				'service_request',
				'knowledge_article',
				'cmdb_ci_relationship',
				'incident_problem_link',
				'change_ci_impact'
			)
		);
	END IF;

	IF to_regclass('_table') IS NOT NULL THEN
		DELETE FROM _table
		WHERE name IN (
			'cmdb_ci',
			'incident',
			'problem',
			'change_request',
			'service_request',
			'knowledge_article',
			'cmdb_ci_relationship',
			'incident_problem_link',
			'change_ci_impact'
		);
	END IF;

	IF to_regclass('_menu') IS NOT NULL THEN
		DELETE FROM _menu
		WHERE href IN (
			'/t/cmdb_ci',
			'/t/incident',
			'/t/problem',
			'/t/change_request',
			'/t/service_request',
			'/t/knowledge_article',
			'/t/cmdb_ci_relationship',
			'/t/incident_problem_link',
			'/t/change_ci_impact',
			'/p/cmdb',
			'/p/incident-ops',
			'/p/problem-mgmt',
			'/p/change-control',
			'/p/request-fulfilment',
			'/p/knowledge-hub'
		);
	END IF;

	IF to_regclass('_page') IS NOT NULL THEN
		DELETE FROM _page
		WHERE slug IN ('cmdb', 'incident-ops', 'problem-mgmt', 'change-control', 'request-fulfilment', 'knowledge-hub');
	END IF;
END
$$;
