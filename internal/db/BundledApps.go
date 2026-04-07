package db

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

//go:embed bundled-apps/*.yaml
var bundledAppsFS embed.FS

const bundledAppsManifestPath = "bundled-apps/manifest.yaml"
const bundledAppRegistryPropertyKey = "bundled_app_names"

var defaultBundledAppNames = []string{"velm"}

type bundledAppsManifest struct {
	Apps []string `yaml:"apps"`
}

func SyncBundledAppDefinitions(ctx context.Context) error {
	manifestPaths, err := bundledAppManifestPaths()
	if err != nil {
		return err
	}

	managedNames, ok, err := loadBundledAppRegistry(ctx)
	if err != nil {
		return err
	}
	if !ok {
		managedNames = append([]string(nil), defaultBundledAppNames...)
	}
	if len(manifestPaths) == 0 && (!ok || sameBundledAppNameSet(managedNames, defaultBundledAppNames)) {
		if err := saveBundledAppRegistry(ctx, defaultBundledAppNames); err != nil {
			return err
		}
		InvalidateAuthzCache()
		return nil
	}

	desiredNames := make(map[string]bool, len(manifestPaths))
	for _, path := range manifestPaths {
		appName, err := syncBundledAppDefinition(ctx, path)
		if err != nil {
			return err
		}
		desiredNames[appName] = true
	}

	for _, appName := range managedNames {
		appName = strings.TrimSpace(strings.ToLower(appName))
		if appName == "" || desiredNames[appName] {
			continue
		}
		if err := pruneBundledAppDefinition(ctx, appName); err != nil {
			return err
		}
	}

	if err := saveBundledAppRegistry(ctx, sortedBundledAppNames(desiredNames)); err != nil {
		return err
	}

	InvalidateAuthzCache()
	return nil
}

func bundledAppManifestPaths() ([]string, error) {
	content, err := bundledAppsFS.ReadFile(bundledAppsManifestPath)
	if err != nil {
		return nil, fmt.Errorf("read bundled app manifest: %w", err)
	}

	var manifest bundledAppsManifest
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		return nil, fmt.Errorf("parse bundled app manifest: %w", err)
	}

	paths := make([]string, 0, len(manifest.Apps))
	seen := map[string]bool{}
	for _, rawPath := range manifest.Apps {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			continue
		}
		if !strings.HasPrefix(path, "bundled-apps/") {
			path = "bundled-apps/" + strings.TrimPrefix(path, "/")
		}
		if !strings.HasSuffix(path, ".yaml") {
			return nil, fmt.Errorf("bundled app path %q must end with .yaml", rawPath)
		}
		if path == bundledAppsManifestPath {
			return nil, fmt.Errorf("bundled app manifest cannot include itself")
		}
		if seen[path] {
			continue
		}
		if _, err := bundledAppsFS.ReadFile(path); err != nil {
			return nil, fmt.Errorf("read bundled app definition %q: %w", path, err)
		}
		seen[path] = true
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func loadBundledAppRegistry(ctx context.Context) ([]string, bool, error) {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	names, ok, err := loadBundledAppRegistryTx(ctx, tx)
	if err != nil {
		return nil, false, err
	}
	return names, ok, tx.Commit(ctx)
}

func loadBundledAppRegistryTx(ctx context.Context, tx pgx.Tx) ([]string, bool, error) {
	exists, err := relationExists(ctx, tx, "_property")
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}

	var raw string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(value, '')
		FROM _property
		WHERE key = $1
		ORDER BY _updated_at DESC, _created_at DESC
		LIMIT 1
	`, bundledAppRegistryPropertyKey).Scan(&raw)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load bundled app registry: %w", err)
	}

	var names []string
	if strings.TrimSpace(raw) != "" {
		parsed, ok, err := parseBundledAppRegistryValue(raw)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		names = parsed
	}
	return normalizeBundledAppNames(names), true, nil
}

func parseBundledAppRegistryValue(raw string) ([]string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true, nil
	}

	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err == nil {
		return normalizeBundledAppNames(names), true, nil
	}

	items := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t'
	})
	if len(items) == 0 {
		return nil, false, nil
	}

	normalized := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(strings.ToLower(item))
		if name == "" {
			continue
		}
		if !IsSafeIdentifier(name) {
			return nil, false, nil
		}
		normalized = append(normalized, name)
	}
	if len(normalized) == 0 {
		return nil, false, nil
	}
	return normalizeBundledAppNames(normalized), true, nil
}

func saveBundledAppRegistry(ctx context.Context, names []string) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := saveBundledAppRegistryTx(ctx, tx, names); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func saveBundledAppRegistryTx(ctx context.Context, tx pgx.Tx, names []string) error {
	exists, err := relationExists(ctx, tx, "_property")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	payload, err := json.Marshal(normalizeBundledAppNames(names))
	if err != nil {
		return fmt.Errorf("marshal bundled app registry: %w", err)
	}

	commandTag, err := tx.Exec(ctx, `
		UPDATE _property
		SET value = $2,
			_updated_at = NOW()
		WHERE key = $1
	`, bundledAppRegistryPropertyKey, string(payload))
	if err != nil {
		return fmt.Errorf("update bundled app registry: %w", err)
	}
	if commandTag.RowsAffected() > 0 {
		return nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO _property (key, value)
		VALUES ($1, $2)
	`, bundledAppRegistryPropertyKey, string(payload)); err != nil {
		return fmt.Errorf("insert bundled app registry: %w", err)
	}
	return nil
}

func normalizeBundledAppNames(names []string) []string {
	normalized := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(strings.ToLower(name))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		normalized = append(normalized, name)
	}
	sort.Strings(normalized)
	return normalized
}

func sameBundledAppNameSet(left, right []string) bool {
	left = normalizeBundledAppNames(left)
	right = normalizeBundledAppNames(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sortedBundledAppNames(names map[string]bool) []string {
	items := make([]string, 0, len(names))
	for name, include := range names {
		if !include {
			continue
		}
		items = append(items, name)
	}
	return normalizeBundledAppNames(items)
}

func syncBundledAppDefinition(ctx context.Context, path string) (string, error) {
	content, err := bundledAppsFS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read bundled app definition %q: %w", path, err)
	}

	definition, err := ParseAppDefinition(string(content))
	if err != nil {
		return "", fmt.Errorf("parse bundled app definition %q: %w", path, err)
	}
	if definition == nil {
		return "", fmt.Errorf("bundled app definition %q is empty", path)
	}

	app := RegisteredApp{
		Name:        definition.Name,
		Namespace:   definition.Namespace,
		Label:       definition.Label,
		Description: definition.Description,
		Status:      "active",
		Definition:  definition,
	}
	if existing, err := GetActiveAppByName(ctx, definition.Name); err == nil {
		app = existing
	} else if !strings.Contains(strings.ToLower(err.Error()), "app not found") {
		return "", err
	}

	if err := prepareDefinitionForApp(app, definition); err != nil {
		return "", err
	}
	if err := validateAppDefinitionForApp(ctx, app, definition); err != nil {
		return "", err
	}

	normalized, err := yaml.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("marshal bundled app definition %q: %w", path, err)
	}
	normalizedContent := string(normalized)

	_, err = Pool.Exec(ctx, `
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
			$1, $2, $3, $4, 'active',
			$5, $5,
			1, 1
		)
		ON CONFLICT (name) DO UPDATE
		SET namespace = EXCLUDED.namespace,
			label = EXCLUDED.label,
			description = EXCLUDED.description,
			status = 'active',
			definition_yaml = EXCLUDED.definition_yaml,
			published_definition_yaml = EXCLUDED.published_definition_yaml,
			definition_version = CASE
				WHEN COALESCE(_app.definition_yaml, '') = EXCLUDED.definition_yaml THEN _app.definition_version
				ELSE GREATEST(_app.definition_version, _app.published_version) + 1
			END,
			published_version = CASE
				WHEN COALESCE(_app.published_definition_yaml, '') = EXCLUDED.published_definition_yaml THEN _app.published_version
				ELSE GREATEST(_app.definition_version, _app.published_version) + 1
			END,
			_updated_at = NOW()
	`, definition.Name, definition.Namespace, definition.Label, definition.Description, normalizedContent)
	if err != nil {
		return "", fmt.Errorf("upsert bundled app definition %q: %w", definition.Name, err)
	}

	if err := EnsurePublishedAppSchema(ctx, definition.Name); err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.ToLower(definition.Name)), nil
}

func pruneBundledAppDefinition(ctx context.Context, appName string) error {
	app, err := GetActiveAppByName(ctx, appName)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "app not found") {
			return nil
		}
		return err
	}

	definition := cloneAppDefinition(app.Definition)
	if definition == nil {
		definition = cloneAppDefinition(app.DraftDefinition)
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin prune bundled app %q: %w", app.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if definition != nil {
		if err := syncPublishedAppPagesTx(ctx, tx, app, definition, nil); err != nil {
			return fmt.Errorf("remove bundled app pages %q: %w", app.Name, err)
		}
		if appOwnsPhysicalSchema(app) {
			if err := reconcileDefinitionSchemaTx(ctx, tx, app, definition, nil); err != nil {
				return fmt.Errorf("remove bundled app schema %q: %w", app.Name, err)
			}
		}
		if err := deleteDefinitionSeedsTx(ctx, tx, app, definition); err != nil {
			return fmt.Errorf("remove bundled app seeds %q: %w", app.Name, err)
		}
		if err := deleteProvisionedSeedGroupsTx(ctx, tx, definition); err != nil {
			return fmt.Errorf("remove bundled app seed groups %q: %w", app.Name, err)
		}
		if err := deleteDefinitionRolesTx(ctx, tx, app, definition); err != nil {
			return fmt.Errorf("remove bundled app roles %q: %w", app.Name, err)
		}
	}

	if err := deleteAutoNumberCountersForAppTx(ctx, tx, app); err != nil {
		return fmt.Errorf("remove bundled app counters %q: %w", app.Name, err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM _app WHERE name = $1 OR namespace = $1`, app.Name); err != nil {
		return fmt.Errorf("delete bundled app %q: %w", app.Name, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit prune bundled app %q: %w", app.Name, err)
	}
	return nil
}

func deleteDefinitionSeedsTx(ctx context.Context, tx pgx.Tx, app RegisteredApp, definition *AppDefinition) error {
	if definition == nil || len(definition.Seeds) == 0 {
		return nil
	}

	for seedIndex := len(definition.Seeds) - 1; seedIndex >= 0; seedIndex-- {
		seed := definition.Seeds[seedIndex]
		tableName := strings.TrimSpace(strings.ToLower(seed.Table))
		if tableName == "" {
			continue
		}
		exists, err := relationExists(ctx, tx, tableName)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}

		quotedTable, err := QuoteIdentifier(tableName)
		if err != nil {
			return err
		}
		for rowIndex := len(seed.Rows) - 1; rowIndex >= 0; rowIndex-- {
			recordID := plannedSeedRecordID(app, tableName, rowIndex, seed.Rows[rowIndex])
			if strings.TrimSpace(recordID) == "" {
				continue
			}
			if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE _id = $1`, quotedTable), recordID); err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteDefinitionRolesTx(ctx context.Context, tx pgx.Tx, app RegisteredApp, definition *AppDefinition) error {
	if definition == nil || len(definition.Roles) == 0 {
		return nil
	}

	roleNames := make([]string, 0, len(definition.Roles))
	for _, role := range definition.Roles {
		roleName := appScopedRoleName(app, role.Name)
		if roleName == "" {
			continue
		}
		roleNames = append(roleNames, roleName)
	}
	roleNames = normalizeBundledAppNames(roleNames)
	if len(roleNames) == 0 {
		return nil
	}

	statements := []string{
		`DELETE FROM _user_role WHERE role_id IN (SELECT _id FROM _role WHERE name = ANY($1))`,
		`DELETE FROM _group_role WHERE role_id IN (SELECT _id FROM _role WHERE name = ANY($1))`,
		`DELETE FROM _role_permission WHERE role_id IN (SELECT _id FROM _role WHERE name = ANY($1))`,
		`DELETE FROM _role_inheritance WHERE role_id IN (SELECT _id FROM _role WHERE name = ANY($1)) OR inherits_role_id IN (SELECT _id FROM _role WHERE name = ANY($1))`,
		`DELETE FROM _role WHERE name = ANY($1)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement, roleNames); err != nil {
			return err
		}
	}
	return nil
}

func deleteProvisionedSeedGroupsTx(ctx context.Context, tx pgx.Tx, definition *AppDefinition) error {
	groupNames := definitionSeedGroupNames(definition)
	if len(groupNames) == 0 {
		return nil
	}

	exists, err := relationExists(ctx, tx, "_group")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	for _, groupName := range groupNames {
		if _, err := tx.Exec(ctx, `
			DELETE FROM _group
			WHERE LOWER(name) = LOWER($1)
			  AND COALESCE(description, '') = $2
		`, groupName, provisionedSeedGroupDescription); err != nil {
			return err
		}
	}
	return nil
}

func definitionSeedGroupNames(definition *AppDefinition) []string {
	if definition == nil || len(definition.Seeds) == 0 {
		return nil
	}

	seen := map[string]bool{}
	names := make([]string, 0, 4)
	for _, seed := range definition.Seeds {
		for _, row := range seed.Rows {
			collectSeedGroupNames(row, seen, &names)
		}
	}
	sort.Strings(names)
	return names
}

func collectSeedGroupNames(rawValue any, seen map[string]bool, names *[]string) {
	switch value := rawValue.(type) {
	case string:
		trimmed := strings.TrimSpace(value)
		if !strings.HasPrefix(trimmed, "@group:") {
			return
		}
		groupName := strings.TrimSpace(strings.TrimPrefix(trimmed, "@group:"))
		if groupName == "" || seen[groupName] {
			return
		}
		seen[groupName] = true
		*names = append(*names, groupName)
	case []any:
		for _, item := range value {
			collectSeedGroupNames(item, seen, names)
		}
	case map[string]any:
		for _, item := range value {
			collectSeedGroupNames(item, seen, names)
		}
	}
}

func deleteAutoNumberCountersForAppTx(ctx context.Context, tx pgx.Tx, app RegisteredApp) error {
	exists, err := relationExists(ctx, tx, "base_counters")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	scope := resolvedAutoNumberScope(app.Name, app.Namespace)
	if strings.TrimSpace(scope) == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM base_counters WHERE app_prefix LIKE $1`, scope+`_%`); err != nil {
		return err
	}
	return nil
}

func EnsurePublishedAppSchema(ctx context.Context, appName string) error {
	app, err := GetActiveAppByName(ctx, appName)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "app not found") {
			return nil
		}
		return err
	}

	definition := app.Definition
	if definition == nil {
		definition = app.DraftDefinition
	}
	if definition == nil || !definitionOwnsPhysicalSchema(definition) {
		return nil
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin published app schema sync for %q: %w", app.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := applyDefinitionSchemaTx(ctx, tx, definition); err != nil {
		return err
	}
	if err := applyDefinitionSeedsTx(ctx, tx, app, definition); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit published app schema sync for %q: %w", app.Name, err)
	}
	return nil
}
