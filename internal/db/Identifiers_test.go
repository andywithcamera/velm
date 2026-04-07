package db

import "testing"

func TestIsSafeIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "simple", input: "work_item", want: true},
		{name: "underscore", input: "_table", want: true},
		{name: "alphanumeric", input: "asset_item_42", want: true},
		{name: "starts with digit", input: "1table", want: false},
		{name: "contains dash", input: "custom-table", want: false},
		{name: "contains space", input: "custom table", want: false},
		{name: "contains quote", input: `abc"def`, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsSafeIdentifier(tt.input)
			if got != tt.want {
				t.Fatalf("IsSafeIdentifier(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestQuoteIdentifier(t *testing.T) {
	t.Parallel()

	quoted, err := QuoteIdentifier("work_item")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if quoted != `"work_item"` {
		t.Fatalf("got %s, want %s", quoted, `"work_item"`)
	}

	if _, err := QuoteIdentifier("work_item;drop table"); err == nil {
		t.Fatal("expected an error for unsafe identifier")
	}
}
