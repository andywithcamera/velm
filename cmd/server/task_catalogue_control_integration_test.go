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

func orderThroughCatalogue(t *testing.T, userID, slug string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-catalogue/order",
		strings.NewReader(url.Values{"definition_slug": {slug}}.Encode()))
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

	rec := orderThroughCatalogue(t, userID, slug)
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

	rec := orderThroughCatalogue(t, userID, slug)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalogue order = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "2 steps opened") {
		t.Fatalf("confirmation does not count the if and end: %s", rec.Body.String())
	}
}
