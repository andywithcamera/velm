package main

import "testing"

func TestParseRelationshipMapDepthDefaultsAndClamps(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{name: "empty defaults", raw: "", want: relationshipMapDefaultDepth},
		{name: "invalid defaults", raw: "abc", want: relationshipMapDefaultDepth},
		{name: "below minimum clamps", raw: "0", want: relationshipMapMinDepth},
		{name: "within range", raw: "3", want: 3},
		{name: "above maximum clamps", raw: "99", want: relationshipMapMaxDepth},
	}

	for _, tt := range tests {
		if got := parseRelationshipMapDepth(tt.raw); got != tt.want {
			t.Fatalf("%s: parseRelationshipMapDepth(%q) = %d, want %d", tt.name, tt.raw, got, tt.want)
		}
	}
}

func TestRelationshipMapPreferLevelPrefersCloserToCenterThenNegative(t *testing.T) {
	if !relationshipMapPreferLevel(3, 1) {
		t.Fatal("expected closer level to win")
	}
	if !relationshipMapPreferLevel(1, -1) {
		t.Fatal("expected negative level to win when distance ties")
	}
	if relationshipMapPreferLevel(-2, -2) {
		t.Fatal("expected identical level not to replace existing value")
	}
}

func TestSelectRelationshipMapTethersUsesExtremesAndDegree(t *testing.T) {
	nodes := []relationshipMapNode{
		{ID: "root", Name: "Root", Level: 0, Degree: 4},
		{ID: "up-low", Name: "Cache", Level: -2, Degree: 1},
		{ID: "up-high", Name: "API", Level: -2, Degree: 3},
		{ID: "down-low", Name: "Events", Level: 2, Degree: 2},
		{ID: "down-high", Name: "Database", Level: 2, Degree: 5},
	}

	ceiling, floor := selectRelationshipMapTethers(nodes)
	if ceiling != "up-high" {
		t.Fatalf("ceiling = %q, want %q", ceiling, "up-high")
	}
	if floor != "down-high" {
		t.Fatalf("floor = %q, want %q", floor, "down-high")
	}
}
