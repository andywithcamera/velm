package main

// The catalogue over a control tree (Process Designer, Requirements AC-35 and
// AC-44): ordering a definition whose for_each is over the cap is refused with
// the existing error fragment, and the confirmation counts control blocks.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"velm/internal/auth"
	"velm/internal/db"
)

func catalogueTestUser(t *testing.T, ctx context.Context) string {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var id string
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email) VALUES ($1, $2) RETURNING _id::text`,
		"catalogue-control-"+suffix, "catalogue-control-"+suffix+"@example.test").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM _user WHERE _id = $1`, id)
	})
	return id
}

// orderThroughCatalogue posts to the definition's order page, the way the form
// on that page does: the slug is in the path, and the form carries only inputs.
func orderThroughCatalogue(t *testing.T, userID, slug string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-catalogue/"+slug+"/start",
		strings.NewReader(form.Encode()))
	req.SetPathValue("slug", slug)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.WithUserContext(req.Context(), userID, "", "", "admin"))
	handleTaskCatalogOrder(rec, req)
	return rec
}

// AC-35: a spawn refused by the cap reaches the catalogue as a 400 rendered
// through the existing error fragment.
func TestCatalogOrderRefusesOverCap(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	userID := catalogueTestUser(t, ctx)
	slug := tdeSlug(t, "catalogue-over-cap")
	containerID := insertDefinition(t, ctx, slug, "Reboot fleet", "", "", true, true)

	hosts := make([]string, 0, 101)
	for i := 0; i < 101; i++ {
		hosts = append(hosts, fmt.Sprintf("host-%03d", i))
	}
	collection, _ := json.Marshal(hosts)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, block_kind, block_type, config, position, sort_order)
		VALUES ($1, 'control', 'for_each', jsonb_build_object('label', 'fleet', 'collection', $2::jsonb), 10, 10)`,
		containerID, string(collection)); err != nil {
		t.Fatalf("seed for_each: %v", err)
	}

	rec := orderThroughCatalogue(t, userID, slug, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("catalogue order over the cap = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ui-catalog-order-error") {
		t.Fatalf("refusal did not render the existing error fragment: %s", rec.Body.String())
	}
}

// AC-44: the order confirmation counts control blocks as opened blocks.
func TestCatalogOrderConfirmation(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	userID := catalogueTestUser(t, ctx)
	slug := tdeSlug(t, "catalogue-control-chain")
	containerID := insertDefinition(t, ctx, slug, "Upgrade OS", "", "", true, true)

	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, block_kind, position, sort_order)
		VALUES ($1, 'trigger', 10, 10)`, containerID); err != nil {
		t.Fatalf("seed trigger: %v", err)
	}
	// The plumbing check (AC-80) refuses an if with an empty scope, so the if
	// holds one task. Two blocks are still at the root: the if and the end.
	var ifID string
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, block_kind, block_type, config, position, sort_order)
		VALUES ($1, 'control', 'if', '{"label":"always","condition":"expr: true"}'::jsonb, 20, 20)
		RETURNING _id::text`,
		containerID).Scan(&ifID); err != nil {
		t.Fatalf("seed if: %v", err)
	}
	branchSlug := tdeSlug(t, "catalogue-control-branch")
	branchID := insertDefinition(t, ctx, branchSlug, "Branch task", "", "", false, true)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, child_definition_id, parent_block_id, block_kind, position, sort_order)
		VALUES ($1, $2, $3, 'task', 25, 25)`, containerID, branchID, ifID); err != nil {
		t.Fatalf("seed branch task: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child
			(container_definition_id, block_kind, block_type, position, sort_order)
		VALUES ($1, 'control', 'end', 30, 30)`, containerID); err != nil {
		t.Fatalf("seed end: %v", err)
	}

	rec := orderThroughCatalogue(t, userID, slug, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("catalogue order = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	// The redirect lands on the record that was created; the count is read from
	// the database rather than from a sentence, so it cannot drift.
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "/f/base_task/") {
		t.Fatalf("order redirected to %q, want the created task's record", location)
	}
	rootID := strings.TrimPrefix(location, "/f/base_task/")
	var spawned int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM base_task
		WHERE parent_task_id = $1`, rootID).Scan(&spawned); err != nil {
		t.Fatalf("count spawned children: %v", err)
	}
	if spawned != 2 {
		t.Fatalf("the chain opened %d blocks, want the if and the branch task", spawned)
	}
}
