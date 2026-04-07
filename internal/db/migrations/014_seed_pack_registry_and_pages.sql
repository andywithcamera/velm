CREATE TABLE IF NOT EXISTS _seed_pack_release (
	pack_name TEXT NOT NULL,
	version TEXT NOT NULL,
	checksum TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (pack_name, version)
);

INSERT INTO _seed_pack_release (pack_name, version, checksum, description)
VALUES
	('core_metadata', '2026.03.13.2', 'm011+m012', 'System metadata, labels, defaults, validation, and index flags'),
	('core_objects', '2026.03.13.2', 'm013', 'Core work and asset tables plus metadata bootstrap'),
	('core_pages', '2026.03.13.2', 'm014', 'Seeded core pages and menu entries')
ON CONFLICT (pack_name, version)
DO UPDATE SET
	checksum = EXCLUDED.checksum,
	description = EXCLUDED.description,
	applied_at = NOW();

DO $$
BEGIN
	IF to_regclass('_page') IS NOT NULL THEN
		INSERT INTO _page (name, slug, content)
		SELECT p.name, p.slug, p.content
		FROM (
			VALUES
				('Work Overview', 'work-overview', 'Core work management page. Use work items for tasks, tickets, and operational execution.'),
				('Asset Overview', 'asset-overview', 'Core asset management page. Track systems, services, devices, and configuration items.')
		) AS p(name, slug, content)
		WHERE NOT EXISTS (SELECT 1 FROM _page x WHERE x.slug = p.slug);

		UPDATE _page pg
		SET
			name = p.name,
			content = p.content
		FROM (
			VALUES
				('Work Overview', 'work-overview', 'Core work management page. Use work items for tasks, tickets, and operational execution.'),
				('Asset Overview', 'asset-overview', 'Core asset management page. Track systems, services, devices, and configuration items.')
		) AS p(name, slug, content)
		WHERE pg.slug = p.slug;
	END IF;

	IF to_regclass('_menu') IS NOT NULL THEN
		INSERT INTO _menu (title, href, "order")
		SELECT m.title, m.href, m.sort_order
		FROM (
			VALUES
				('Work Page', '/p/work-overview', 210),
				('Asset Page', '/p/asset-overview', 220)
		) AS m(title, href, sort_order)
		WHERE NOT EXISTS (SELECT 1 FROM _menu x WHERE x.href = m.href);

		UPDATE _menu mm
		SET
			title = m.title,
			"order" = m.sort_order
		FROM (
			VALUES
				('Work Page', '/p/work-overview', 210),
				('Asset Page', '/p/asset-overview', 220)
		) AS m(title, href, sort_order)
		WHERE mm.href = m.href;
	END IF;
END
$$;
