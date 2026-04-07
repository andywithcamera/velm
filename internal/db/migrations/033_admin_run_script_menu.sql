DO $$
BEGIN
	IF to_regclass('_menu') IS NOT NULL THEN
		INSERT INTO _menu (title, href, "order")
		SELECT 'Run Script', '/admin/run-script', 230
		WHERE NOT EXISTS (SELECT 1 FROM _menu WHERE href = '/admin/run-script');

		UPDATE _menu
		SET title = 'Run Script',
			"order" = 230
		WHERE href = '/admin/run-script';
	END IF;
END
$$;
