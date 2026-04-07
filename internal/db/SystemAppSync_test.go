package db

import (
	"database/sql"
	"testing"
)

func TestBuildSystemAppDefinitionTableFromPhysicalData(t *testing.T) {
	t.Parallel()

	table := Table{
		NAME:           "_user_notification",
		LABEL_SINGULAR: "Notification",
		LABEL_PLURAL:   "Notifications",
		DESCRIPTION:    "Per-user notifications.",
		DISPLAY_FIELD:  "title",
	}
	columns := []Column{
		{NAME: "_id", LABEL: "ID", DATA_TYPE: "uuid"},
		{NAME: "_updated_at", LABEL: "Updated At", DATA_TYPE: "timestamptz"},
		{NAME: "title", LABEL: "Title", DATA_TYPE: "text"},
		{NAME: "body", LABEL: "Body", DATA_TYPE: "long_text", IS_NULLABLE: true},
		{NAME: "definition_version", LABEL: "Definition Version", DATA_TYPE: "bigint"},
		{NAME: "user_id", LABEL: "User", DATA_TYPE: "uuid", REFERENCE_TABLE: sql.NullString{String: "_user", Valid: true}},
	}

	item := buildSystemAppDefinitionTableFromPhysicalData(table, columns)
	if item.Name != "_user_notification" {
		t.Fatalf("Name = %q", item.Name)
	}
	if item.DisplayField != "title" {
		t.Fatalf("DisplayField = %q, want %q", item.DisplayField, "title")
	}
	if len(item.Columns) != 6 {
		t.Fatalf("len(Columns) = %d, want 6", len(item.Columns))
	}
	if got := item.Columns[4].DataType; got != "integer" {
		t.Fatalf("definition_version DataType = %q, want %q", got, "integer")
	}
	if got := item.Columns[5].DataType; got != "reference" {
		t.Fatalf("user_id DataType = %q, want %q", got, "reference")
	}
	if got := item.Columns[5].ReferenceTable; got != "_user" {
		t.Fatalf("ReferenceTable = %q, want %q", got, "_user")
	}
	if len(item.Forms) != 1 || len(item.Forms[0].Fields) != 4 {
		t.Fatalf("Forms = %#v", item.Forms)
	}
	if item.Forms[0].Fields[0] != "title" || item.Forms[0].Fields[3] != "user_id" {
		t.Fatalf("Form fields = %#v", item.Forms[0].Fields)
	}
	if len(item.Lists) != 1 || len(item.Lists[0].Columns) != 4 {
		t.Fatalf("Lists = %#v", item.Lists)
	}
	if item.Lists[0].Columns[0] != "title" || item.Lists[0].Columns[2] != "user_id" || item.Lists[0].Columns[3] != "_updated_at" {
		t.Fatalf("List columns = %#v", item.Lists[0].Columns)
	}
}

func TestBuildSystemAppDefinitionColumnDropsPhysicalDefaults(t *testing.T) {
	t.Parallel()

	column := Column{
		NAME:          "definition_version",
		LABEL:         "Definition Version",
		DATA_TYPE:     "bigint",
		DEFAULT_VALUE: sql.NullString{String: "0", Valid: true},
	}

	item := buildSystemAppDefinitionColumn(column)
	if item.DefaultValue != "" {
		t.Fatalf("DefaultValue = %q, want empty", item.DefaultValue)
	}
	if item.DataType != "integer" {
		t.Fatalf("DataType = %q, want %q", item.DataType, "integer")
	}
}

func TestIncludeColumnInSystemDefaultList(t *testing.T) {
	t.Parallel()

	if includeColumnInSystemDefaultList(AppDefinitionColumn{Name: "body", DataType: "long_text"}) {
		t.Fatalf("expected long_text columns to be excluded from default lists")
	}
	if includeColumnInSystemDefaultList(AppDefinitionColumn{Name: "pref_value", DataType: "jsonb"}) {
		t.Fatalf("expected jsonb columns to be excluded from default lists")
	}
	if !includeColumnInSystemDefaultList(AppDefinitionColumn{Name: "title", DataType: "text"}) {
		t.Fatalf("expected text columns to be included in default lists")
	}
}

func TestBuildSystemAppDefinitionTableFallsBackWhenDisplayFieldMissing(t *testing.T) {
	t.Parallel()

	table := Table{
		NAME:           "_example",
		LABEL_SINGULAR: "Example",
		LABEL_PLURAL:   "Examples",
		DISPLAY_FIELD:  "field_name",
	}
	columns := []Column{
		{NAME: "_id", LABEL: "ID", DATA_TYPE: "uuid"},
		{NAME: "title", LABEL: "Title", DATA_TYPE: "text"},
		{NAME: "description", LABEL: "Description", DATA_TYPE: "text"},
	}

	item := buildSystemAppDefinitionTableFromPhysicalData(table, columns)
	if item.DisplayField != "title" {
		t.Fatalf("DisplayField = %q, want %q", item.DisplayField, "title")
	}
}

func TestEnsureSystemLandingPageAddsDefaultPage(t *testing.T) {
	t.Parallel()

	definition := &AppDefinition{}
	changed := ensureSystemLandingPage(definition)
	if !changed {
		t.Fatalf("expected landing page helper to report a change")
	}
	if len(definition.Pages) != 1 {
		t.Fatalf("len(Pages) = %d, want 1", len(definition.Pages))
	}
	page := definition.Pages[0]
	if page.Slug != systemLandingPageSlug {
		t.Fatalf("Slug = %q, want %q", page.Slug, systemLandingPageSlug)
	}
	if page.Status != "published" {
		t.Fatalf("Status = %q, want %q", page.Status, "published")
	}
	if page.EditorMode != "html" {
		t.Fatalf("EditorMode = %q, want %q", page.EditorMode, "html")
	}
	if page.Content != systemLandingPageContent {
		t.Fatalf("expected landing page content to be seeded with the admin dashboard")
	}
}

func TestEnsureSystemLandingPagePreservesContentAndForcesPublished(t *testing.T) {
	t.Parallel()

	definition := &AppDefinition{
		Pages: []AppDefinitionPage{{
			Name:    "Custom Landing",
			Slug:    systemLandingPageSlug,
			Content: "<section>Custom</section>",
			Status:  "draft",
		}},
	}

	changed := ensureSystemLandingPage(definition)
	if !changed {
		t.Fatalf("expected landing page helper to normalize an existing page")
	}
	page := definition.Pages[0]
	if page.Content != "<section>Custom</section>" {
		t.Fatalf("Content = %q", page.Content)
	}
	if page.Status != "published" {
		t.Fatalf("Status = %q, want %q", page.Status, "published")
	}
	if page.Label != systemLandingPageName {
		t.Fatalf("Label = %q, want %q", page.Label, systemLandingPageName)
	}
}

func TestEnsureSystemLandingPageReplacesLegacyDefaultContent(t *testing.T) {
	t.Parallel()

	definition := &AppDefinition{
		Pages: []AppDefinitionPage{{
			Name:    systemLandingPageName,
			Slug:    systemLandingPageSlug,
			Content: legacySystemLandingPageContent,
			Status:  "published",
		}},
	}

	changed := ensureSystemLandingPage(definition)
	if !changed {
		t.Fatalf("expected legacy landing page content to be refreshed")
	}
	page := definition.Pages[0]
	if page.Content != systemLandingPageContent {
		t.Fatalf("Content = %q, want refreshed admin dashboard content", page.Content)
	}
}

func TestEnsureSystemLandingPageReplacesLegacyAdminDashboardContent(t *testing.T) {
	t.Parallel()

	definition := &AppDefinition{
		Pages: []AppDefinitionPage{{
			Name:    systemLandingPageName,
			Slug:    systemLandingPageSlug,
			Content: legacyAdminDashboardPageContent,
			Status:  "published",
		}},
	}

	changed := ensureSystemLandingPage(definition)
	if !changed {
		t.Fatalf("expected legacy admin dashboard content to be refreshed")
	}
	page := definition.Pages[0]
	if page.Content != systemLandingPageContent {
		t.Fatalf("Content = %q, want refreshed admin dashboard content", page.Content)
	}
}

func TestNormalizeSystemAuthenticatedRouteTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "page prefix", input: "page:_landing", want: "page:_landing"},
		{name: "legacy page prefix", input: "page:landing", want: "page:_landing"},
		{name: "page path", input: "/p/landing", want: "page:_landing"},
		{name: "table path", input: "/t/_property", want: "table:_property"},
		{name: "generic path", input: "/task", want: "/task"},
		{name: "generic path with query", input: "/admin/app-editor?app=system", want: "/admin/app-editor?app=system"},
		{name: "root disallowed", input: "/", want: ""},
		{name: "login disallowed", input: "/login?next=%2Ftask", want: ""},
		{name: "invalid", input: "javascript:alert(1)", want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeSystemAuthenticatedRouteTarget(tt.input); got != tt.want {
				t.Fatalf("normalizeSystemAuthenticatedRouteTarget(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReplacementSystemAuthenticatedRouteTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		current    string
		fallback   string
		wantValue  string
		wantUpdate bool
	}{
		{name: "missing uses fallback", current: "", fallback: systemDefaultAuthenticatedRouteTarget, wantValue: systemDefaultAuthenticatedRouteTarget, wantUpdate: true},
		{name: "legacy landing preserved", current: "page:landing", fallback: systemDefaultAuthenticatedRouteTarget, wantValue: "", wantUpdate: false},
		{name: "invalid resets to fallback", current: "javascript:alert(1)", fallback: systemDefaultAuthenticatedRouteTarget, wantValue: systemDefaultAuthenticatedRouteTarget, wantUpdate: true},
		{name: "custom path preserved", current: "/task", fallback: systemDefaultAuthenticatedRouteTarget, wantValue: "", wantUpdate: false},
		{name: "custom table preserved", current: "table:_property", fallback: systemDefaultAuthenticatedRouteTarget, wantValue: "", wantUpdate: false},
		{name: "default already normalized", current: systemDefaultAuthenticatedRouteTarget, fallback: systemDefaultAuthenticatedRouteTarget, wantValue: "", wantUpdate: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotValue, gotUpdate := replacementSystemAuthenticatedRouteTarget(tt.current, tt.fallback)
			if gotValue != tt.wantValue || gotUpdate != tt.wantUpdate {
				t.Fatalf("replacementSystemAuthenticatedRouteTarget(%q, %q) = (%q, %t), want (%q, %t)", tt.current, tt.fallback, gotValue, gotUpdate, tt.wantValue, tt.wantUpdate)
			}
		})
	}
}
