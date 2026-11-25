package main

// Slice 1 (legibility): the menu entry and the catalogue's step counts need a
// real Postgres, because both are built from base_task_definition rows. A skip
// here is a broken run: the legacy suite reported green on nothing once when
// Postgres was absent, so these tests go through taskDefinitionsTestDB, which
// fails if TEST_DATABASE_URL is missing or migrations do not apply.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"velm/internal/auth"
	"velm/internal/db"
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
			if item.Title != "Catalogue" {
				t.Errorf("catalogue menu entry title = %q, want \"Catalogue\"", item.Title)
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

// AC: the menu is a fixed set of doors, not one entry per entity table.
// Entities is the base_entity table view (not the schema builder) and Docs
// survives the tidy-up. Both are hrefs, so the menu cannot drift to the
// builder or to a page that does not exist.
func TestMenuDoorsAreEntitiesTableAndDocs(t *testing.T) {
	taskDefinitionsTestDB(t)

	if err := loadMenuFromDB(); err != nil {
		t.Fatalf("loadMenuFromDB: %v", err)
	}

	hrefs := make(map[string]string, len(menuItems))
	for _, item := range menuItems {
		hrefs[item.Href] = item.Title
	}

	if title, ok := hrefs["/t/base_entity"]; !ok {
		t.Errorf("menu has no /t/base_entity entry; entities door missing (menu: %v)", hrefs)
	} else if title != "Entities" {
		t.Errorf("entities menu title = %q, want \"Entities\"", title)
	}
	if _, ok := hrefs["/builder/schema"]; ok {
		t.Errorf("menu points at /builder/schema; the entities door is the base_entity table view")
	}
	if title, ok := hrefs["/docs"]; !ok {
		t.Errorf("menu has no /docs entry; docs door missing (menu: %v)", hrefs)
	} else if title != "Docs" {
		t.Errorf("docs menu title = %q, want \"Docs\"", title)
	}
}

// orderPageFor renders GET /task-catalogue/{slug}/start the way the catalogue
// cards and global search link to it.
func orderPageFor(t *testing.T, userID, slug string, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	ensureTestSessionStore()
	target := "/task-catalogue/" + slug + "/start"
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("slug", slug)
	req = req.WithContext(auth.WithUserContext(req.Context(), userID, "", "", "admin"))
	handleTaskCatalogOrderPage(rec, req)
	return rec
}

// The order page says what the chain will do, in the order it will do it. The
// reader decides here, so the steps are the point of the page — the card could
// only ever show a count.
func TestTaskCatalogOrderPageListsChainStepsInOrder(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	userID := catalogueTestUser(t, ctx)

	slug := tdeSlug(t, "order-page-chain")
	containerID := insertDefinition(t, ctx, slug, "ZZ Order Page Chain", "Ship it", "", true, true)

	firstSlug := tdeSlug(t, "order-page-first")
	secondSlug := tdeSlug(t, "order-page-second")
	firstID := insertDefinition(t, ctx, firstSlug, "ZZ Write the change note", "", "", false, true)
	secondID := insertDefinition(t, ctx, secondSlug, "ZZ Push the PR", "", "", false, true)
	linkStep(t, ctx, containerID, firstID, 10)
	linkStep(t, ctx, containerID, secondID, 20)

	rec := orderPageFor(t, userID, slug, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("order page = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	first := strings.Index(body, "ZZ Write the change note")
	second := strings.Index(body, "ZZ Push the PR")
	if first < 0 || second < 0 {
		t.Fatalf("order page does not list both steps:\n%s", body)
	}
	if first > second {
		t.Errorf("the steps are out of order: %q appears after %q", "Write the change note", "Push the PR")
	}
	for _, want := range []string{
		"Chain · 2 steps",
		"Starting this opens a chain of 2 steps.",
		`action="/task-catalogue/` + slug + `/start"`,
		`name="csrf_token"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("order page missing %q", want)
		}
	}
}

// An inactive definition is not in the catalogue and cannot be ordered through
// its page either: filtering the list but leaving the page live would just move
// the dead end one click further in. The definition page still shows it, which
// is where its author reactivates it.
func TestInactiveDefinitionIsNeitherListedNorOrderable(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	userID := catalogueTestUser(t, ctx)

	slug := tdeSlug(t, "inactive-entry")
	insertDefinition(t, ctx, slug, "ZZ Inactive Entry", "Not in this release", "", false, false)

	catalogue, err := loadTaskCatalog(ctx, slug)
	if err != nil {
		t.Fatalf("loadTaskCatalog: %v", err)
	}
	for _, category := range catalogue {
		for _, entry := range category.Entries {
			if entry.Slug == slug {
				t.Fatalf("inactive definition %q is listed in the catalogue", slug)
			}
		}
	}

	rec := orderPageFor(t, userID, slug, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("order page for an inactive definition = %d, want 200 with a reason", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "inactive") {
		t.Errorf("the page does not say the definition is inactive:\n%s", body)
	}
	// The layout carries its own search form, so the check is for this
	// definition's order form, not for any submit button on the page.
	if strings.Contains(body, `action="/task-catalogue/`+slug+`/start"`) {
		t.Errorf("the page still offers the order form:\n%s", body)
	}
}

// A refused order re-renders the page with the answers still in the fields,
// and creates nothing. Retyping a form to fix one field is how a requester
// learns to distrust the page.
func TestRefusedOrderKeepsAnswersAndSpawnsNothing(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	userID := catalogueTestUser(t, ctx)

	// "server" is required and omitted here; "reason" is nullable and filled in.
	// The nullable one is the one that must survive the refusal.
	slug := tdeSlug(t, "order-page-inputs")
	insertDefinition(t, ctx, slug, "ZZ Order Page Inputs", "", tdeTwoInputs, false, true)

	var before int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM base_task`).Scan(&before); err != nil {
		t.Fatalf("count tasks: %v", err)
	}

	rec := orderThroughCatalogue(t, userID, slug, url.Values{"reason": {"Patch needed"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("order missing a required field = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Patch needed") {
		t.Errorf("the refusal discarded what was typed:\n%s", body)
	}
	if !strings.Contains(body, "ui-catalog-order-error") {
		t.Errorf("the refusal is not marked as an error:\n%s", body)
	}

	var after int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM base_task`).Scan(&after); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if after != before {
		t.Fatalf("a refused order created %d tasks", after-before)
	}
}

// Search knows which definition it matched, so it links straight to that
// definition's order page instead of sending the reader back to the grid to
// find it again.
func TestSearchLinksDefinitionsToTheirOrderPage(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "search-order")
	insertDefinition(t, ctx, slug, "ZZ Search Order Target", "", "", false, true)

	results, err := searchTaskDefinitionResults(ctx, "ZZ Search Order Target", 10)
	if err != nil {
		t.Fatalf("searchTaskDefinitionResults: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("search found nothing for the definition it was given")
	}
	if got, want := results[0].Href, "/task-catalogue/"+slug+"/start"; got != want {
		t.Errorf("search result href = %q, want %q", got, want)
	}
}
