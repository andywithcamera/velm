package main

// Slice 1 (legibility): the menu entry and the catalogue's step counts need a
// real Postgres, because both are built from base_task_definition rows. A skip
// here is a broken run: the legacy suite reported green on nothing once when
// Postgres was absent, so these tests go through taskDefinitionsTestDB, which
// fails if TEST_DATABASE_URL is missing or migrations do not apply.

import (
	"testing"
)

// AC: the menu carries "Task Catalogue", directly above "Task Definitions".
func TestMenuCarriesTaskCatalogueAboveTaskDefinitions(t *testing.T) {
	taskDefinitionsTestDB(t)

	if err := loadMenuFromDB(); err != nil {
		t.Fatalf("loadMenuFromDB: %v", err)
	}

	catalogueAt, definitionsAt := -1, -1
	for i, item := range menuItems {
		switch item.Href {
		case "/task-catalogue":
			catalogueAt = i
			if item.Title != "Task Catalogue" {
				t.Errorf("catalogue menu entry title = %q, want \"Task Catalogue\"", item.Title)
			}
		case "/task-definitions":
			definitionsAt = i
		}
	}
	if catalogueAt < 0 {
		t.Fatal("the menu has no /task-catalogue entry")
	}
	if definitionsAt < 0 {
		t.Fatal("the menu has no /task-definitions entry")
	}
	if catalogueAt != definitionsAt-1 {
		t.Errorf("Task Catalogue is at index %d and Task Definitions at %d; want the catalogue immediately above", catalogueAt, definitionsAt)
	}
}

// AC: a chain reports how many steps it will open; a leaf reports none. The
// count comes from base_task_definition_child, so it cannot drift from what
// the engine will actually spawn.
func TestLoadTaskCatalogLabelsChainsAndCountsSteps(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)

	chainSlug := tdeSlug(t, "catalogue-chain")
	firstSlug := tdeSlug(t, "catalogue-step-one")
	secondSlug := tdeSlug(t, "catalogue-step-two")
	leafSlug := tdeSlug(t, "catalogue-leaf")

	chainID := insertDefinition(t, ctx, chainSlug, "ZZ Catalogue Chain", "Two steps", "", true, true)
	firstID := insertDefinition(t, ctx, firstSlug, "ZZ Catalogue Step One", "First step", "", false, true)
	secondID := insertDefinition(t, ctx, secondSlug, "ZZ Catalogue Step Two", "Second step", "", false, true)
	_ = insertDefinition(t, ctx, leafSlug, "ZZ Catalogue Leaf", "No steps at all", "", false, true)

	linkStep(t, ctx, chainID, firstID, 10)
	linkStep(t, ctx, chainID, secondID, 20)

	catalogue, err := loadTaskCatalog(ctx, "catalogue")
	if err != nil {
		t.Fatalf("loadTaskCatalog: %v", err)
	}

	found := map[string]taskCatalogEntry{}
	for _, category := range catalogue {
		for _, entry := range category.Entries {
			found[entry.Slug] = entry
		}
	}

	chain, ok := found[chainSlug]
	if !ok {
		t.Fatalf("catalogue is missing the container %s", chainSlug)
	}
	if !chain.IsContainer {
		t.Error("container entry is not marked as a container")
	}
	if chain.StepCount != 2 {
		t.Errorf("container StepCount = %d, want 2", chain.StepCount)
	}
	if got, want := chain.KindLabel(), "Chain · 2 steps"; got != want {
		t.Errorf("container KindLabel = %q, want %q", got, want)
	}

	leaf, ok := found[leafSlug]
	if !ok {
		t.Fatalf("catalogue is missing the leaf %s", leafSlug)
	}
	if leaf.IsContainer {
		t.Error("leaf entry is marked as a container")
	}
	if leaf.StepCount != 0 {
		t.Errorf("leaf StepCount = %d, want 0", leaf.StepCount)
	}
	if got, want := leaf.KindLabel(), "Single task"; got != want {
		t.Errorf("leaf KindLabel = %q, want %q", got, want)
	}
}
