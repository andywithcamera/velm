package db

import "testing"

func TestDeterministicSeedUUIDIsStable(t *testing.T) {
	first := deterministicSeedUUID("dw|dw_story|story-form-actions-runtime")
	second := deterministicSeedUUID("dw|dw_story|story-form-actions-runtime")
	third := deterministicSeedUUID("dw|dw_story|story-release-cockpit")

	if first != second {
		t.Fatalf("deterministicSeedUUID() should be stable, got %q and %q", first, second)
	}
	if first == third {
		t.Fatalf("deterministicSeedUUID() should vary by source, got %q and %q", first, third)
	}
}

func TestBuildAppSeedStateCollectsAliases(t *testing.T) {
	definition := &AppDefinition{
		Seeds: []AppDefinitionSeed{
			{
				Table: "dw_story",
				Rows: []map[string]any{
					{
						"_seed_key": "story-alpha",
						"title":     "Alpha",
					},
					{
						"_seed_key": "story-beta",
						"title":     "Beta",
					},
				},
			},
		},
	}

	state, err := buildAppSeedState(RegisteredApp{Name: "devworks", Namespace: "dw"}, definition)
	if err != nil {
		t.Fatalf("buildAppSeedState() error = %v", err)
	}

	if len(state.Aliases) != 2 {
		t.Fatalf("expected 2 aliases, got %d", len(state.Aliases))
	}
	if state.Aliases["story-alpha"] == "" || state.Aliases["story-beta"] == "" {
		t.Fatalf("expected aliases to be populated, got %#v", state.Aliases)
	}
	if state.Aliases["story-alpha"] == state.Aliases["story-beta"] {
		t.Fatalf("expected distinct alias ids, got %#v", state.Aliases)
	}
}
