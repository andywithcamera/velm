package main

import "testing"

func TestNormalizeAuthenticatedRouteTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "page prefix", input: "page:_landing", want: "page:_landing"},
		{name: "page path", input: "/p/_landing", want: "page:_landing"},
		{name: "legacy system page prefix", input: "page:landing", want: "page:_landing"},
		{name: "legacy system page path", input: "/p/landing", want: "page:_landing"},
		{name: "namespaced page prefix", input: "page:sales_about", want: "page:sales_about"},
		{name: "namespaced page path", input: "/p/sales_about", want: "page:sales_about"},
		{name: "table prefix", input: "table:_property", want: "table:_property"},
		{name: "table path", input: "/t/_property", want: "table:_property"},
		{name: "generic path", input: "/task", want: "/task"},
		{name: "generic path with query", input: "/admin/app-editor?app=system", want: "/admin/app-editor?app=system"},
		{name: "login disallowed", input: "login", want: ""},
		{name: "login path disallowed", input: "/login?next=%2Ftask", want: ""},
		{name: "root disallowed", input: "/", want: ""},
		{name: "root with query disallowed", input: "/?tab=tasks", want: ""},
		{name: "network path disallowed", input: "//evil.example.com/task", want: ""},
		{name: "invalid", input: "javascript:alert(1)", want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeAuthenticatedRouteTarget(tt.input); got != tt.want {
				t.Fatalf("normalizeAuthenticatedRouteTarget(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
