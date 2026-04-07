package db

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

var simplePageSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,63}$`)

func isSimplePageSlug(input string) bool {
	return simplePageSlugPattern.MatchString(strings.TrimSpace(strings.ToLower(input)))
}

func QualifiedPageSlug(namespace, slug string) string {
	namespace = strings.TrimSpace(strings.ToLower(namespace))
	slug = strings.TrimSpace(strings.ToLower(slug))
	if !isSimplePageSlug(slug) {
		return ""
	}
	if namespace == "" {
		return "_" + slug
	}
	if !IsSafeIdentifier(namespace) {
		return ""
	}
	return namespace + "_" + slug
}

func ParseQualifiedPageSlug(input string) (namespace string, slug string, ok bool) {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return "", "", false
	}
	if strings.HasPrefix(input, "_") {
		slug = strings.TrimPrefix(input, "_")
		if !isSimplePageSlug(slug) {
			return "", "", false
		}
		return "", slug, true
	}

	separator := strings.LastIndex(input, "_")
	if separator <= 0 || separator >= len(input)-1 {
		return "", "", false
	}

	namespace = input[:separator]
	slug = input[separator+1:]
	if !IsSafeIdentifier(namespace) || !isSimplePageSlug(slug) {
		return "", "", false
	}
	return namespace, slug, true
}

func IsQualifiedPageSlug(input string) bool {
	_, _, ok := ParseQualifiedPageSlug(input)
	return ok
}

func FindRuntimePageBySlug(ctx context.Context, slug string) (RegisteredApp, AppDefinitionPage, bool, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return RegisteredApp{}, AppDefinitionPage{}, false, err
	}
	app, page, ok := findRuntimePageBySlugWithApps(apps, slug)
	return app, page, ok, nil
}

func findRuntimePageBySlugWithApps(apps []RegisteredApp, slug string) (RegisteredApp, AppDefinitionPage, bool) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	if !IsQualifiedPageSlug(slug) {
		return RegisteredApp{}, AppDefinitionPage{}, false
	}
	for _, app := range apps {
		definition := runtimeDefinitionForApp(app)
		if definition == nil {
			continue
		}
		for _, page := range definition.Pages {
			if QualifiedPageSlug(app.Namespace, page.Slug) != slug {
				continue
			}
			return app, page, true
		}
	}
	return RegisteredApp{}, AppDefinitionPage{}, false
}

func SyncPublishedAppPages(ctx context.Context) error {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return err
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin sync published app pages: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, app := range apps {
		if app.Definition == nil {
			continue
		}
		if err := syncPublishedAppPagesTx(ctx, tx, app, nil, app.Definition); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit sync published app pages: %w", err)
	}
	return nil
}

func syncPublishedAppPagesTx(ctx context.Context, tx pgx.Tx, app RegisteredApp, publishedDefinition, nextDefinition *AppDefinition) error {
	pageExists, err := relationExists(ctx, tx, "_page")
	if err != nil {
		return fmt.Errorf("check _page relation: %w", err)
	}
	if !pageExists {
		return nil
	}

	pageVersionExists, err := relationExists(ctx, tx, "_page_version")
	if err != nil {
		return fmt.Errorf("check _page_version relation: %w", err)
	}

	publishedPages := qualifiedDefinitionPages(app.Namespace, publishedDefinition)
	nextPages := qualifiedDefinitionPages(app.Namespace, nextDefinition)

	for qualifiedSlug := range publishedPages {
		if _, exists := nextPages[qualifiedSlug]; exists {
			continue
		}
		if err := deleteRuntimePageTx(ctx, tx, qualifiedSlug, pageVersionExists); err != nil {
			return err
		}
	}

	for qualifiedSlug, page := range nextPages {
		if err := upsertRuntimePageTx(ctx, tx, app.Namespace, qualifiedSlug, page, pageVersionExists); err != nil {
			return err
		}
	}

	return nil
}

func qualifiedDefinitionPages(namespace string, definition *AppDefinition) map[string]AppDefinitionPage {
	pages := map[string]AppDefinitionPage{}
	if definition == nil {
		return pages
	}
	for _, page := range definition.Pages {
		qualifiedSlug := QualifiedPageSlug(namespace, page.Slug)
		if qualifiedSlug == "" {
			continue
		}
		pages[qualifiedSlug] = page
	}
	return pages
}

func upsertRuntimePageTx(ctx context.Context, tx pgx.Tx, namespace, qualifiedSlug string, page AppDefinitionPage, pageVersionExists bool) error {
	name := strings.TrimSpace(page.Name)
	if name == "" {
		name = strings.TrimSpace(page.Label)
	}
	if name == "" {
		name = humanizeIdentifier(page.Slug)
	}
	if name == "" {
		name = qualifiedSlug
	}

	editorMode := strings.TrimSpace(strings.ToLower(page.EditorMode))
	if editorMode == "" {
		editorMode = "wysiwyg"
	}
	status := strings.TrimSpace(strings.ToLower(page.Status))
	if status == "" {
		status = "draft"
	}

	if strings.TrimSpace(namespace) == "" {
		if err := migrateLegacySystemPageSlugTx(ctx, tx, page.Slug, qualifiedSlug, pageVersionExists); err != nil {
			return err
		}
	}

	var existing struct {
		Name        string
		Content     string
		EditorMode  string
		Status      string
		PublishedAt *string
	}
	err := tx.QueryRow(ctx, `
		SELECT
			COALESCE(name, ''),
			COALESCE(content, ''),
			COALESCE(editor_mode, ''),
			COALESCE(status, ''),
			CASE WHEN published_at IS NULL THEN NULL ELSE published_at::text END
		FROM _page
		WHERE slug = $1
	`, qualifiedSlug).Scan(&existing.Name, &existing.Content, &existing.EditorMode, &existing.Status, &existing.PublishedAt)
	if err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("check page %q: %w", qualifiedSlug, err)
	}
	if err == pgx.ErrNoRows {
		if _, err := tx.Exec(ctx, `
			INSERT INTO _page (name, slug, content, editor_mode, status, published_at)
			VALUES ($1, $2, $3, $4, $5, CASE WHEN $5 = 'published' THEN NOW() ELSE NULL END)
		`, name, qualifiedSlug, page.Content, editorMode, status); err != nil {
			return fmt.Errorf("insert page %q: %w", qualifiedSlug, err)
		}
		return nil
	}

	if existing.Name == name &&
		existing.Content == page.Content &&
		strings.EqualFold(existing.EditorMode, editorMode) &&
		strings.EqualFold(existing.Status, status) &&
		(status != "published" || existing.PublishedAt != nil) {
		return nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE _page
		SET name = $2,
			content = $3,
			editor_mode = $4,
			status = $5,
			published_at = CASE
				WHEN $5 = 'published' AND published_at IS NULL THEN NOW()
				WHEN $5 = 'draft' THEN NULL
				ELSE published_at
			END
		WHERE slug = $1
	`, qualifiedSlug, name, page.Content, editorMode, status); err != nil {
		return fmt.Errorf("update page %q: %w", qualifiedSlug, err)
	}

	return nil
}

func deleteRuntimePageTx(ctx context.Context, tx pgx.Tx, qualifiedSlug string, pageVersionExists bool) error {
	if pageVersionExists {
		if _, err := tx.Exec(ctx, `DELETE FROM _page_version WHERE page_slug = $1`, qualifiedSlug); err != nil {
			return fmt.Errorf("delete page versions %q: %w", qualifiedSlug, err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM _page WHERE slug = $1`, qualifiedSlug); err != nil {
		return fmt.Errorf("delete page %q: %w", qualifiedSlug, err)
	}
	return nil
}

func migrateLegacySystemPageSlugTx(ctx context.Context, tx pgx.Tx, legacySlug, qualifiedSlug string, pageVersionExists bool) error {
	legacySlug = strings.TrimSpace(strings.ToLower(legacySlug))
	qualifiedSlug = strings.TrimSpace(strings.ToLower(qualifiedSlug))
	if legacySlug == "" || qualifiedSlug == "" || legacySlug == qualifiedSlug {
		return nil
	}

	var qualifiedExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM _page WHERE slug = $1)`, qualifiedSlug).Scan(&qualifiedExists); err != nil {
		return fmt.Errorf("check qualified system page %q: %w", qualifiedSlug, err)
	}
	if qualifiedExists {
		if pageVersionExists {
			if _, err := tx.Exec(ctx, `DELETE FROM _page_version WHERE page_slug = $1`, legacySlug); err != nil {
				return fmt.Errorf("delete legacy page versions %q: %w", legacySlug, err)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM _page WHERE slug = $1`, legacySlug); err != nil {
			return fmt.Errorf("delete legacy page %q: %w", legacySlug, err)
		}
		return nil
	}

	if pageVersionExists {
		if _, err := tx.Exec(ctx, `UPDATE _page_version SET page_slug = $2 WHERE page_slug = $1`, legacySlug, qualifiedSlug); err != nil {
			return fmt.Errorf("rename legacy page versions %q: %w", legacySlug, err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE _page SET slug = $2 WHERE slug = $1`, legacySlug, qualifiedSlug); err != nil {
		return fmt.Errorf("rename legacy page %q: %w", legacySlug, err)
	}
	return nil
}
