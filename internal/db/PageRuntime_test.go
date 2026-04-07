package db

import "testing"

func TestQualifiedPageSlug(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		namespace string
		slug      string
		want      string
	}{
		{name: "system", namespace: "", slug: "landing", want: "_landing"},
		{name: "namespace", namespace: "sales", slug: "about", want: "sales_about"},
		{name: "namespace with underscore", namespace: "sales_ops", slug: "about", want: "sales_ops_about"},
		{name: "invalid", namespace: "sales", slug: "bad_slug", want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := QualifiedPageSlug(tt.namespace, tt.slug); got != tt.want {
				t.Fatalf("QualifiedPageSlug(%q, %q) = %q, want %q", tt.namespace, tt.slug, got, tt.want)
			}
		})
	}
}

func TestParseQualifiedPageSlug(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantNS    string
		wantSlug  string
		wantValid bool
	}{
		{name: "system", input: "_landing", wantNS: "", wantSlug: "landing", wantValid: true},
		{name: "namespace", input: "sales_about", wantNS: "sales", wantSlug: "about", wantValid: true},
		{name: "namespace with underscore", input: "sales_ops_about", wantNS: "sales_ops", wantSlug: "about", wantValid: true},
		{name: "invalid", input: "about", wantValid: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotNS, gotSlug, gotValid := ParseQualifiedPageSlug(tt.input)
			if gotValid != tt.wantValid || gotNS != tt.wantNS || gotSlug != tt.wantSlug {
				t.Fatalf("ParseQualifiedPageSlug(%q) = (%q, %q, %t), want (%q, %q, %t)", tt.input, gotNS, gotSlug, gotValid, tt.wantNS, tt.wantSlug, tt.wantValid)
			}
		})
	}
}

func TestFindRuntimePageBySlugWithApps(t *testing.T) {
	app, page, ok := findRuntimePageBySlugWithApps([]RegisteredApp{
		{
			Name:      "base",
			Namespace: "",
			Definition: &AppDefinition{
				Pages: []AppDefinitionPage{
					{Slug: "landing", Name: "Landing"},
				},
			},
		},
		{
			Name:      "sales",
			Namespace: "sales",
			Definition: &AppDefinition{
				Pages: []AppDefinitionPage{
					{Slug: "about", Name: "About"},
				},
			},
		},
	}, "sales_about")

	if !ok {
		t.Fatal("expected namespaced runtime page to resolve")
	}
	if app.Name != "sales" {
		t.Fatalf("app.Name = %q, want %q", app.Name, "sales")
	}
	if page.Slug != "about" {
		t.Fatalf("page.Slug = %q, want %q", page.Slug, "about")
	}

	_, _, ok = findRuntimePageBySlugWithApps([]RegisteredApp{
		{
			Name:      "base",
			Namespace: "",
			Definition: &AppDefinition{
				Pages: []AppDefinitionPage{
					{Slug: "landing", Name: "Landing"},
				},
			},
		},
	}, "_missing")
	if ok {
		t.Fatal("expected unknown runtime page lookup to fail")
	}
}
