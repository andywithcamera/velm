package db

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

const systemAuthenticatedRootRoutePropertyKey = "authenticated_root_route_target"
const systemLandingPageSlug = "landing"
const systemDefaultAuthenticatedRouteTarget = "/task"
const systemLandingPageName = "Landing"
const systemLandingPageDescription = "Default signed-in landing page for the out-of-the-box base app."
const legacySystemLandingPageContent = `<section><h1>Welcome</h1><p>This is the default landing page for signed-in users.</p><p>Use the navigation to open tables, pages, and admin tools. Update the <code>authenticated_root_route_target</code> system property to send users somewhere else after login.</p></section>`
const legacyAdminDashboardPageContent = `<section><h1>ADMIN DASHBOARD</h1><p>USERS</p><p>GROUPS</p><p>ROLES</p><p>APP EDITOR</p><p>SYSTEM PROPERTIES</p><p>SYSTEM LOG</p><p>RUN SCRIPT</p></section>`
const systemLandingPageContent = `<section class="ui-admin-landing">
  <div class="ui-admin-landing-hero">
    <p class="ui-admin-landing-kicker">Admin Workspace</p>
    <h1>Welcome back, admin.</h1>
    <p>Everything operational is staged here. Use this dashboard to manage people, permissions, platform configuration, audit activity, and one-off system actions without hunting through the wider UI.</p>
    <div class="ui-admin-landing-hero-actions">
      <a href="/admin/app-editor" class="ui-btn ui-btn-primary">Open App Editor</a>
      <a href="/admin/audit" class="ui-btn ui-btn-secondary">Review System Log</a>
    </div>
  </div>
  <div class="ui-admin-landing-grid">
    <a href="/t/_user" class="ui-admin-landing-card">
      <span class="ui-admin-landing-card-kicker">Directory</span>
      <strong>Users</strong>
      <span>Manage platform accounts and review who can sign in.</span>
    </a>
    <a href="/t/_group" class="ui-admin-landing-card">
      <span class="ui-admin-landing-card-kicker">Directory</span>
      <strong>Groups</strong>
      <span>Organize teams and shared ownership around access.</span>
    </a>
    <a href="/t/_role" class="ui-admin-landing-card">
      <span class="ui-admin-landing-card-kicker">Security</span>
      <strong>Roles</strong>
      <span>Define privilege sets and inspect how permissions are modeled.</span>
    </a>
    <a href="/admin/app-editor" class="ui-admin-landing-card">
      <span class="ui-admin-landing-card-kicker">Builder</span>
      <strong>App Editor</strong>
      <span>Change application structure, pages, forms, and scripted behavior.</span>
    </a>
    <a href="/t/_property" class="ui-admin-landing-card">
      <span class="ui-admin-landing-card-kicker">Platform</span>
      <strong>System Properties</strong>
      <span>Adjust root routes, feature flags, and runtime configuration.</span>
    </a>
    <a href="/admin/audit" class="ui-admin-landing-card">
      <span class="ui-admin-landing-card-kicker">Observability</span>
      <strong>System Log</strong>
      <span>Review request history, audit events, and platform activity.</span>
    </a>
    <a href="/admin/run-script" class="ui-admin-landing-card">
      <span class="ui-admin-landing-card-kicker">Operations</span>
      <strong>Run Script</strong>
      <span>Execute controlled ad hoc scripts against the live platform.</span>
    </a>
  </div>
</section>`

func SyncSystemAppDefinitionWithPhysicalTables(ctx context.Context) error {
	app, err := GetActiveAppByName(ctx, "system")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "app not found") {
			return nil
		}
		return err
	}
	if strings.TrimSpace(app.Namespace) != "" {
		return nil
	}

	draft := cloneAppDefinition(app.DraftDefinition)
	if draft == nil {
		draft = cloneAppDefinition(app.Definition)
	}
	published := cloneAppDefinition(app.Definition)
	if published == nil {
		published = cloneAppDefinition(app.DraftDefinition)
	}
	if draft == nil && published == nil {
		return nil
	}

	draftChanged := false
	if draft != nil {
		var err error
		draftChanged, err = appendMissingSystemPhysicalTables(ctx, draft)
		if err != nil {
			return err
		}
		if ensureSystemLandingPage(draft) {
			draftChanged = true
		}
	}

	publishedChanged := false
	if published != nil {
		var err error
		publishedChanged, err = appendMissingSystemPhysicalTables(ctx, published)
		if err != nil {
			return err
		}
	}

	activeDefinition := published
	if activeDefinition == nil {
		activeDefinition = draft
	}
	if err := ensureMissingSystemPhysicalTables(ctx, app, activeDefinition); err != nil {
		return err
	}

	if draft != nil {
		var err error
		changed, err := appendMissingSystemPhysicalTables(ctx, draft)
		if err != nil {
			return err
		}
		draftChanged = draftChanged || changed
		if ensureSystemLandingPage(draft) {
			draftChanged = true
		}
	}

	if published != nil {
		var err error
		changed, err := appendMissingSystemPhysicalTables(ctx, published)
		if err != nil {
			return err
		}
		publishedChanged = publishedChanged || changed
		if ensureSystemLandingPage(published) {
			publishedChanged = true
		}
	}

	if draftChanged || publishedChanged {
		if draft != nil {
			if err := prepareDefinitionForApp(app, draft); err != nil {
				return err
			}
			if err := validateAppDefinitionForApp(ctx, app, draft); err != nil {
				return err
			}
		}
		if published != nil {
			if err := prepareDefinitionForApp(app, published); err != nil {
				return err
			}
			if err := validateAppDefinitionForApp(ctx, app, published); err != nil {
				return err
			}
		}

		draftContent := strings.TrimSpace(app.DefinitionYAML)
		if draft != nil {
			content, err := yaml.Marshal(draft)
			if err != nil {
				return fmt.Errorf("marshal system draft definition: %w", err)
			}
			draftContent = string(content)
		}

		publishedContent := strings.TrimSpace(app.PublishedDefinitionYAML)
		if published != nil {
			content, err := yaml.Marshal(published)
			if err != nil {
				return fmt.Errorf("marshal system published definition: %w", err)
			}
			publishedContent = string(content)
		}

		_, err = Pool.Exec(ctx, `
			UPDATE _app
			SET definition_yaml = $2,
				published_definition_yaml = $3,
				definition_version = CASE
					WHEN COALESCE(definition_yaml, '') = $2 THEN definition_version
					ELSE GREATEST(definition_version, published_version) + 1
				END,
				published_version = CASE
					WHEN COALESCE(published_definition_yaml, '') = $3 THEN published_version
					ELSE GREATEST(definition_version, published_version) + 1
				END,
				_updated_at = NOW()
			WHERE name = $1 OR namespace = $1
		`, app.Name, draftContent, publishedContent)
		if err != nil {
			return fmt.Errorf("sync system app definition: %w", err)
		}
	}

	return syncSystemLandingPageAndRoute(ctx, activeDefinition)
}

func EnsureRequiredSystemTablesAfter042(ctx context.Context) error {
	app, err := GetActiveAppByName(ctx, "system")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "app not found") {
			return nil
		}
		return err
	}
	if strings.TrimSpace(app.Namespace) != "" {
		return nil
	}

	definition := cloneAppDefinition(app.Definition)
	if definition == nil {
		definition = cloneAppDefinition(app.DraftDefinition)
	}
	if definition == nil {
		return nil
	}

	return ensureNamedSystemPhysicalTables(ctx, app, definition, map[string]bool{
		"_user": true,
	})
}

func appendMissingSystemPhysicalTables(ctx context.Context, definition *AppDefinition) (bool, error) {
	if definition == nil {
		return false, nil
	}

	tableNames, err := ListPhysicalBaseTables(ctx)
	if err != nil {
		return false, err
	}

	physical := make(map[string]bool, len(tableNames))
	for _, tableName := range tableNames {
		physical[tableName] = true
	}

	added := false
	updated := false
	filtered := make([]AppDefinitionTable, 0, len(definition.Tables))
	existing := make(map[string]bool, len(definition.Tables))
	for _, table := range definition.Tables {
		name := strings.TrimSpace(strings.ToLower(table.Name))
		if strings.HasPrefix(name, "_") && physical[name] {
			item, err := buildSystemAppDefinitionTable(ctx, name)
			if err != nil {
				return false, err
			}
			filtered = append(filtered, item)
			existing[name] = true
			if !reflect.DeepEqual(table, item) {
				updated = true
			}
			continue
		}
		filtered = append(filtered, table)
		existing[name] = true
	}
	definition.Tables = filtered

	for _, tableName := range tableNames {
		if !strings.HasPrefix(tableName, "_") || existing[tableName] {
			continue
		}

		item, err := buildSystemAppDefinitionTable(ctx, tableName)
		if err != nil {
			return false, err
		}
		definition.Tables = append(definition.Tables, item)
		existing[tableName] = true
		added = true
	}

	if added {
		sort.SliceStable(definition.Tables[len(filtered):], func(i, j int) bool {
			return definition.Tables[len(filtered)+i].Name < definition.Tables[len(filtered)+j].Name
		})
	}

	return added || updated, nil
}

func ensureMissingSystemPhysicalTables(ctx context.Context, app RegisteredApp, definition *AppDefinition) error {
	return ensureNamedSystemPhysicalTables(ctx, app, definition, nil)
}

func ensureNamedSystemPhysicalTables(ctx context.Context, app RegisteredApp, definition *AppDefinition, allowed map[string]bool) error {
	if definition == nil {
		return nil
	}

	apps, err := ListActiveApps(ctx)
	if err != nil {
		return err
	}

	currentApp := app
	currentApp.Definition = definition
	currentApp.DraftDefinition = definition
	apps = mergeRegisteredApps(apps, currentApp)

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ensure system physical tables: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, table := range definition.Tables {
		tableName := strings.TrimSpace(strings.ToLower(table.Name))
		if !strings.HasPrefix(tableName, "_") {
			continue
		}
		if allowed != nil && !allowed[tableName] {
			continue
		}

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tableName).Scan(&exists); err != nil {
			return fmt.Errorf("check physical system table %q: %w", tableName, err)
		}
		if exists {
			continue
		}

		resolvedTable := table
		resolvedColumns, err := resolveDefinitionColumnsWithApps(apps, currentApp, table, map[string]bool{})
		if err != nil {
			return fmt.Errorf("resolve system table %q columns: %w", tableName, err)
		}
		resolvedTable.Columns = resolvedColumns

		if err := ensureDefinitionTableSchemaTx(ctx, tx, "", currentApp, apps, resolvedTable); err != nil {
			return fmt.Errorf("create missing system table %q: %w", tableName, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ensure system physical tables: %w", err)
	}
	return nil
}

func buildSystemAppDefinitionTable(ctx context.Context, tableName string) (AppDefinitionTable, error) {
	table, ok, err := GetPhysicalTable(ctx, tableName)
	if err != nil {
		return AppDefinitionTable{}, err
	}
	if !ok {
		return AppDefinitionTable{}, fmt.Errorf("physical table %q not found", tableName)
	}

	columns, err := GetPhysicalColumns(ctx, tableName)
	if err != nil {
		return AppDefinitionTable{}, err
	}

	return buildSystemAppDefinitionTableFromPhysicalData(table, columns), nil
}

func buildSystemAppDefinitionTableFromPhysicalData(table Table, columns []Column) AppDefinitionTable {
	definitionColumns := make([]AppDefinitionColumn, 0, len(columns))
	visibleColumns := make([]string, 0, len(columns))
	listColumns := make([]string, 0, 5)
	columnNames := make(map[string]bool, len(columns))
	hasUpdatedAt := false
	for _, column := range columns {
		definitionColumn := buildSystemAppDefinitionColumn(column)
		columnNames[definitionColumn.Name] = true
		if definitionColumn.Name == "_updated_at" {
			hasUpdatedAt = true
		}
		definitionColumns = append(definitionColumns, AppDefinitionColumn{
			Name:              definitionColumn.Name,
			Label:             definitionColumn.Label,
			DataType:          definitionColumn.DataType,
			IsNullable:        definitionColumn.IsNullable,
			DefaultValue:      definitionColumn.DefaultValue,
			ValidationRegex:   definitionColumn.ValidationRegex,
			ValidationExpr:    definitionColumn.ValidationExpr,
			ConditionExpr:     definitionColumn.ConditionExpr,
			ValidationMessage: definitionColumn.ValidationMessage,
			ReferenceTable:    definitionColumn.ReferenceTable,
			Choices:           definitionColumn.Choices,
		})
		if !strings.HasPrefix(column.NAME, "_") {
			visibleColumns = append(visibleColumns, column.NAME)
			if includeColumnInSystemDefaultList(definitionColumn) {
				listColumns = append(listColumns, column.NAME)
			}
		}
	}

	displayField := NormalizeDisplayFieldName(table.DISPLAY_FIELD)
	if displayField != "" && !columnNames[displayField] {
		displayField = ""
	}
	if displayField == "" {
		displayField = InferDisplayFieldName(visibleColumns)
	}

	item := AppDefinitionTable{
		Name:          table.NAME,
		LabelSingular: strings.TrimSpace(table.LABEL_SINGULAR),
		LabelPlural:   strings.TrimSpace(table.LABEL_PLURAL),
		Description:   strings.TrimSpace(table.DESCRIPTION),
		DisplayField:  displayField,
		Columns:       definitionColumns,
	}
	if len(visibleColumns) > 0 {
		if len(listColumns) == 0 {
			listColumns = append(listColumns, visibleColumns...)
		}
		if len(listColumns) > 4 {
			listColumns = append([]string(nil), listColumns[:4]...)
		}
		if hasUpdatedAt {
			listColumns = append(listColumns, "_updated_at")
		}
		item.Forms = []AppDefinitionForm{{
			Name:   "default",
			Label:  "Default",
			Fields: append([]string(nil), visibleColumns...),
		}}
		item.Lists = []AppDefinitionList{{
			Name:    "default",
			Label:   "Default",
			Columns: append([]string(nil), listColumns...),
		}}
	}

	return item
}

func buildSystemAppDefinitionColumn(column Column) AppDefinitionColumn {
	dataType := normalizeSystemAppDefinitionDataType(column)

	return AppDefinitionColumn{
		Name:              column.NAME,
		Label:             strings.TrimSpace(column.LABEL),
		DataType:          dataType,
		IsNullable:        column.IS_NULLABLE,
		DefaultValue:      "",
		ValidationRegex:   strings.TrimSpace(column.VALIDATION_REGEX.String),
		ValidationExpr:    strings.TrimSpace(column.VALIDATION_EXPR.String),
		ConditionExpr:     strings.TrimSpace(column.CONDITION_EXPR.String),
		ValidationMessage: strings.TrimSpace(column.VALIDATION_MSG.String),
		ReferenceTable:    strings.TrimSpace(column.REFERENCE_TABLE.String),
		Choices:           append([]ChoiceOption(nil), column.CHOICES...),
	}
}

func normalizeSystemAppDefinitionDataType(column Column) string {
	if strings.TrimSpace(column.REFERENCE_TABLE.String) != "" {
		return "reference"
	}

	switch normalizeDataType(column.DATA_TYPE) {
	case "bigint", "bigserial", "serial":
		return "integer"
	default:
		return normalizeDataType(column.DATA_TYPE)
	}
}

func includeColumnInSystemDefaultList(column AppDefinitionColumn) bool {
	switch BaseDataType(column.DataType) {
	case "long_text", "richtext", "markdown", "code", "json", "jsonb":
		return false
	default:
		return true
	}
}

func ensureSystemLandingPage(definition *AppDefinition) bool {
	if definition == nil {
		return false
	}

	for i := range definition.Pages {
		if strings.TrimSpace(strings.ToLower(definition.Pages[i].Slug)) != systemLandingPageSlug {
			continue
		}

		changed := false
		page := &definition.Pages[i]
		if strings.TrimSpace(page.Name) == "" {
			page.Name = systemLandingPageName
			changed = true
		}
		if strings.TrimSpace(page.Label) == "" {
			page.Label = systemLandingPageName
			changed = true
		}
		if strings.TrimSpace(page.Description) == "" {
			page.Description = systemLandingPageDescription
			changed = true
		}
		if strings.TrimSpace(strings.ToLower(page.EditorMode)) == "" {
			page.EditorMode = "html"
			changed = true
		}
		if !strings.EqualFold(strings.TrimSpace(page.Status), "published") {
			page.Status = "published"
			changed = true
		}
		if shouldReplaceSystemLandingPageContent(page.Content) {
			page.Content = systemLandingPageContent
			changed = true
		}
		return changed
	}

	definition.Pages = append(definition.Pages, AppDefinitionPage{
		Name:        systemLandingPageName,
		Slug:        systemLandingPageSlug,
		Label:       systemLandingPageName,
		Description: systemLandingPageDescription,
		EditorMode:  "html",
		Status:      "published",
		Content:     systemLandingPageContent,
	})
	sort.SliceStable(definition.Pages, func(i, j int) bool {
		return definition.Pages[i].Slug < definition.Pages[j].Slug
	})
	return true
}

func shouldReplaceSystemLandingPageContent(content string) bool {
	normalized := normalizeSystemLandingPageContent(content)
	if normalized == "" {
		return true
	}
	switch normalized {
	case normalizeSystemLandingPageContent(systemLandingPageContent),
		normalizeSystemLandingPageContent(legacySystemLandingPageContent):
		return true
	case normalizeSystemLandingPageContent(legacyAdminDashboardPageContent):
		return true
	default:
		return false
	}
}

func normalizeSystemLandingPageContent(content string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
}

func syncSystemLandingPageAndRoute(ctx context.Context, definition *AppDefinition) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin sync system landing page: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := syncSystemLandingPageAndRouteTx(ctx, tx, definition); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit sync system landing page: %w", err)
	}
	return nil
}

func syncSystemLandingPageAndRouteTx(ctx context.Context, tx pgx.Tx, definition *AppDefinition) error {
	propertyExists, err := relationExists(ctx, tx, "_property")
	if err != nil {
		return fmt.Errorf("check _property relation: %w", err)
	}
	if propertyExists {
		if err := upsertSystemAuthenticatedRoutePropertyTx(ctx, tx, systemDefaultAuthenticatedRouteTarget); err != nil {
			return err
		}
	}

	page, ok := findSystemPageBySlug(definition, systemLandingPageSlug)
	if !ok {
		return nil
	}

	exists, err := relationExists(ctx, tx, "_page")
	if err != nil {
		return fmt.Errorf("check _page relation: %w", err)
	}
	if !exists {
		return nil
	}
	qualifiedSlug := QualifiedPageSlug("", page.Slug)
	if qualifiedSlug == "" {
		return nil
	}
	return upsertRuntimePageTx(ctx, tx, "", qualifiedSlug, page, false)
}

func upsertSystemAuthenticatedRoutePropertyTx(ctx context.Context, tx pgx.Tx, target string) error {
	target = normalizeSystemAuthenticatedRouteTarget(target)
	if target == "" {
		return nil
	}

	var current string
	err := tx.QueryRow(ctx, `
		SELECT value
		FROM _property
		WHERE key = $1
		LIMIT 1
	`, systemAuthenticatedRootRoutePropertyKey).Scan(&current)
	if err == nil {
		if replacement, shouldWrite := replacementSystemAuthenticatedRouteTarget(current, target); shouldWrite {
			if _, err := tx.Exec(ctx, `
				UPDATE _property
				SET value = $2
				WHERE key = $1
			`, systemAuthenticatedRootRoutePropertyKey, replacement); err != nil {
				return fmt.Errorf("update authenticated route property: %w", err)
			}
		}
		return nil
	}
	if err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("select authenticated route property: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO _property (key, value)
		SELECT $1, $2
		WHERE NOT EXISTS (
			SELECT 1
			FROM _property
			WHERE key = $1
		)
	`, systemAuthenticatedRootRoutePropertyKey, target); err != nil {
		return fmt.Errorf("insert authenticated route property: %w", err)
	}
	return nil
}

func replacementSystemAuthenticatedRouteTarget(current, fallback string) (string, bool) {
	fallback = normalizeSystemAuthenticatedRouteTarget(fallback)
	if fallback == "" {
		return "", false
	}

	current = strings.TrimSpace(current)
	if current == "" {
		return fallback, true
	}

	normalizedCurrent := normalizeSystemAuthenticatedRouteTarget(current)
	if normalizedCurrent == "" {
		return fallback, true
	}
	if normalizedCurrent == fallback && current != fallback {
		return fallback, true
	}
	return "", false
}

func normalizeSystemAuthenticatedRouteTarget(input string) string {
	target := strings.TrimSpace(input)
	lowerTarget := strings.ToLower(target)
	switch {
	case strings.HasPrefix(lowerTarget, "page:"):
		slug := normalizeSystemPageRouteSlug(strings.TrimSpace(target[len("page:"):]))
		if slug != "" {
			return "page:" + slug
		}
	case strings.HasPrefix(lowerTarget, "/p/"):
		slug := normalizeSystemPageRouteSlug(strings.TrimSpace(target[len("/p/"):]))
		if slug != "" {
			return "page:" + slug
		}
	case strings.HasPrefix(lowerTarget, "table:"):
		tableName := strings.TrimSpace(strings.ToLower(target[len("table:"):]))
		if IsSafeIdentifier(tableName) {
			return "table:" + tableName
		}
	case strings.HasPrefix(lowerTarget, "/t/"):
		tableName := strings.TrimSpace(strings.ToLower(target[len("/t/"):]))
		if IsSafeIdentifier(tableName) {
			return "table:" + tableName
		}
	}
	if path := normalizeSystemAuthenticatedRoutePath(target); path != "" {
		return path
	}
	return ""
}

func normalizeSystemPageRouteSlug(input string) string {
	slug := strings.TrimSpace(strings.ToLower(input))
	switch {
	case IsQualifiedPageSlug(slug):
		return slug
	case isSimplePageSlug(slug):
		return QualifiedPageSlug("", slug)
	default:
		return ""
	}
}

func normalizeSystemAuthenticatedRoutePath(input string) string {
	target := strings.TrimSpace(input)
	if target == "" || strings.HasPrefix(target, "//") {
		return ""
	}

	parsed, err := url.ParseRequestURI(target)
	if err != nil || parsed == nil || parsed.Host != "" || parsed.Scheme != "" {
		return ""
	}

	path := parsed.EscapedPath()
	if path == "" || path == "/" {
		return ""
	}

	if strings.ToLower(path) == "/login" {
		return ""
	}

	normalized := path
	if parsed.RawQuery != "" {
		normalized += "?" + parsed.RawQuery
	}
	return normalized
}

func findSystemPageBySlug(definition *AppDefinition, slug string) (AppDefinitionPage, bool) {
	if definition == nil {
		return AppDefinitionPage{}, false
	}
	slug = strings.TrimSpace(strings.ToLower(slug))
	for _, page := range definition.Pages {
		if strings.TrimSpace(strings.ToLower(page.Slug)) == slug {
			return page, true
		}
	}
	return AppDefinitionPage{}, false
}
