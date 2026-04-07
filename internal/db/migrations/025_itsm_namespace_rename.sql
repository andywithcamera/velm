DO $$
DECLARE
	rec RECORD;
BEGIN
	CREATE TEMP TABLE tmp_itsm_rename_map (
		old_name TEXT PRIMARY KEY,
		new_name TEXT NOT NULL
	) ON COMMIT DROP;

	INSERT INTO tmp_itsm_rename_map (old_name, new_name)
	VALUES
		('incident', 'itsm_incident'),
		('problem', 'itsm_problem'),
		('change_request', 'itsm_change_request'),
		('service_request', 'itsm_service_request'),
		('cmdb_ci', 'itsm_cmdb_ci'),
		('cmdb_ci_relationship', 'itsm_cmdb_ci_relationship'),
		('incident_problem_link', 'itsm_incident_problem_link'),
		('change_ci_impact', 'itsm_change_ci_impact');

	FOR rec IN
		SELECT old_name, new_name
		FROM tmp_itsm_rename_map
	LOOP
		IF to_regclass(rec.old_name) IS NOT NULL AND to_regclass(rec.new_name) IS NULL THEN
			EXECUTE format('ALTER TABLE %I RENAME TO %I', rec.old_name, rec.new_name);
		END IF;
	END LOOP;

	IF to_regclass('_table') IS NOT NULL THEN
		UPDATE _table t
		SET name = m.new_name
		FROM tmp_itsm_rename_map m
		WHERE t.name = m.old_name;
	END IF;

	IF to_regclass('_menu') IS NOT NULL THEN
		UPDATE _menu mnu
		SET href = regexp_replace(mnu.href, '^/t/' || m.old_name || '$', '/t/' || m.new_name)
		FROM tmp_itsm_rename_map m
		WHERE mnu.href = '/t/' || m.old_name;
	END IF;

	IF to_regclass('_saved_view') IS NOT NULL THEN
		UPDATE _saved_view sv
		SET table_name = m.new_name
		FROM tmp_itsm_rename_map m
		WHERE sv.table_name = m.old_name;
	END IF;

	IF to_regclass('_property') IS NOT NULL THEN
		UPDATE _property p
		SET value = regexp_replace(p.value, '^table:' || m.old_name || '$', 'table:' || m.new_name)
		FROM tmp_itsm_rename_map m
		WHERE p.key = 'root_route_target'
		  AND p.value = 'table:' || m.old_name;
	END IF;

	IF to_regclass('_page') IS NOT NULL THEN
		UPDATE _page pg
		SET content = replace(pg.content, '/t/' || m.old_name, '/t/' || m.new_name)
		FROM tmp_itsm_rename_map m
		WHERE pg.content LIKE '%' || '/t/' || m.old_name || '%';
	END IF;

	IF to_regclass('_page_version') IS NOT NULL THEN
		UPDATE _page_version pg
		SET content = replace(pg.content, '/t/' || m.old_name, '/t/' || m.new_name)
		FROM tmp_itsm_rename_map m
		WHERE pg.content LIKE '%' || '/t/' || m.old_name || '%';
	END IF;
END
$$;
