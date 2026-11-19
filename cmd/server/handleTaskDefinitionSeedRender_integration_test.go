package main

// AC-16: the editor renders a real, delivery_full-shaped container read-only.
//
// Migration 090 shapes the seven stages of `delivery_full`: it sets each stage's
// `depends_on` to the stage before it and writes the mappings that carry an
// earlier step's output downward. Nothing in this repository inserts those
// definitions (they are live data, and 090 guards every statement with a lookup
// so it applies cleanly on a database that does not have them), so this test
// seeds the same shape itself. It is a read test: it renders the editor and
// proves the page changed nothing, which is the half of AC-16 that matters —
// the editor must cope with stored data it did not write, mappings included.
//
// A skip here means TEST_DATABASE_URL is wrong, not that the code is fine.

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"velm/internal/db"
)

// deliveryFullStages is migration 090's stage order, dependency and mapping
// list, in engine order.
type deliveryFullStage struct {
	Slug      string
	Display   string
	Schema    string
	Depends   string // slug of the stage it waits for, "" for the first
	Mapping   string // jsonb literal, "" for none
	SortOrder int
}

var deliveryFullStages = []deliveryFullStage{
	{
		Slug: "st_principle", Display: "Approval in principle", SortOrder: 10,
		Schema:  "- name: idea\n  data_type: text\n",
		Mapping: `{"idea": "$inputs.idea"}`,
	},
	{
		Slug: "st_intake", Display: "Intake validation", SortOrder: 20,
		Schema:  "- name: idea\n  data_type: text\n- name: route\n  data_type: text\n",
		Depends: "st_principle", Mapping: `{"idea": "$inputs.idea"}`,
	},
	{
		Slug: "st_requirement", Display: "Requirement", SortOrder: 30,
		Schema:  "- name: route\n  data_type: text\n- name: requirement_artifact_id\n  data_type: text\n",
		Depends: "st_intake", Mapping: `{"route": "$outputs.st_intake.route"}`,
	},
	{
		Slug: "st_design", Display: "Design", SortOrder: 40,
		Schema:  "- name: route\n  data_type: text\n- name: requirement_artifact_id\n  data_type: text\n- name: design_artifact_id\n  data_type: text\n",
		Depends: "st_requirement",
		Mapping: `{"route": "$outputs.st_intake.route", "requirement_artifact_id": "$outputs.st_requirement.requirement_artifact_id"}`,
	},
	{
		Slug: "st_build", Display: "Build", SortOrder: 50,
		Schema:  "- name: design_artifact_id\n  data_type: text\n- name: commit_sha\n  data_type: text\n",
		Depends: "st_design", Mapping: `{"design_artifact_id": "$outputs.st_design.design_artifact_id"}`,
	},
	{
		Slug: "st_test", Display: "Test", SortOrder: 60,
		Schema:  "- name: commit_sha\n  data_type: text\n",
		Depends: "st_build", Mapping: `{"commit_sha": "$outputs.st_build.commit_sha"}`,
	},
	{
		Slug: "st_release", Display: "Release", SortOrder: 70,
		Schema:  "- name: commit_sha\n  data_type: text\n",
		Depends: "st_test", Mapping: `{"commit_sha": "$outputs.st_build.commit_sha"}`,
	},
}

func seedDeliveryFull(t *testing.T, ctx context.Context) string {
	t.Helper()
	containerID := insertDefinition(t, ctx, "delivery_full", "Full Delivery", "Feature request to release.", "", true, true)
	idBySlug := map[string]string{}
	for _, stage := range deliveryFullStages {
		idBySlug[stage.Slug] = insertDefinition(t, ctx, stage.Slug, stage.Display, "", stage.Schema, false, true)
	}
	for _, stage := range deliveryFullStages {
		mapping := "{}"
		if stage.Mapping != "" {
			mapping = stage.Mapping
		}
		dependsOn := "{}"
		if stage.Depends != "" {
			dependsOn = "{" + idBySlug[stage.Depends] + "}"
		}
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO base_task_definition_child
				(container_definition_id, child_definition_id, sort_order, depends_on,
				 depends_on_reason, input_mapping)
			VALUES ($1, $2, $3, $4::uuid[], $5, $6::jsonb)`,
			containerID, idBySlug[stage.Slug], stage.SortOrder, dependsOn,
			map[bool]string{true: "completed", false: ""}[stage.Depends != ""], mapping); err != nil {
			t.Fatalf("seed stage %s: %v", stage.Slug, err)
		}
	}
	return containerID
}

func deliveryFullSnapshot(t *testing.T, ctx context.Context, containerID string) string {
	t.Helper()
	var snapshot string
	if err := db.Pool.QueryRow(ctx, `
		SELECT _updated_at::text || '|' || COALESCE(input_schema_yaml, '') || '|' || COALESCE(variables_yaml, '')
		FROM base_task_definition WHERE _id = $1`, containerID).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot definition: %v", err)
	}
	rows, err := db.Pool.Query(ctx, `
		SELECT COALESCE(child_definition_id::text, '') || ':' || sort_order::text || ':' ||
		       COALESCE(array_to_string(depends_on, ','), '') || ':' ||
		       COALESCE(depends_on_reason, '') || ':' || COALESCE(input_mapping::text, '')
		FROM base_task_definition_child
		WHERE container_definition_id = $1
		ORDER BY child_definition_id`, containerID)
	if err != nil {
		t.Fatalf("snapshot steps: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatalf("scan step snapshot: %v", err)
		}
		snapshot += "\n" + row
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot steps: %v", err)
	}
	return snapshot
}

// AC-16: the seeded container renders in engine order with its stored
// dependencies and mappings, and rendering changes nothing.
func TestDeliveryFullSeedRendersInEngineOrder(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	containerID := seedDeliveryFull(t, ctx)

	// D6/AC-5 to AC-7: an existing tree gains its trigger block the first time
	// it is opened, so the definition is opened once here. AC-16's "rendering
	// wrote nothing" is then about a definition that has already been through
	// that one deliberate backfill, which is the invariant that matters: an
	// editor page must not rewrite stored data on every render.
	_ = renderDefinitionView(t, "delivery_full", "?view=rows")
	before := deliveryFullSnapshot(t, ctx, containerID)
	// The stepper is the rows surface now that the canvas is the default.
	rec := renderDefinitionView(t, "delivery_full", "?view=rows")
	if rec.Code != 200 {
		t.Fatalf("editor status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	list := stepListHTML(t, body)
	if list == "" {
		t.Fatalf("delivery_full rendered no step list")
	}
	direct := stepListChildren(t, body)
	if len(direct) != len(deliveryFullStages) {
		t.Fatalf("step list has %d element children, want %d <li>", len(direct), len(deliveryFullStages))
	}
	for _, child := range direct {
		if child.Data != "li" {
			t.Errorf("step list has a non-<li> child (<%s>)", child.Data)
		}
	}

	// Engine order: the seven stages appear in 090's order, not in insertion or
	// alphabetical order.
	last := -1
	for _, stage := range deliveryFullStages {
		at := strings.Index(list, stage.Slug)
		if at < 0 {
			t.Fatalf("stage %q missing from the stepper; rendered: %s", stage.Slug, list)
		}
		if at < last {
			t.Errorf("stage %q is out of engine order", stage.Slug)
		}
		last = at
	}

	// The stored mappings render, exactly as 090 writes them.
	for _, want := range []string{
		"route → $outputs.st_intake.route",
		"requirement_artifact_id → $outputs.st_requirement.requirement_artifact_id",
		"design_artifact_id → $outputs.st_design.design_artifact_id",
		"commit_sha → $outputs.st_build.commit_sha",
	} {
		if !strings.Contains(list, want) {
			t.Errorf("stepper does not show mapping %q", want)
		}
	}
	// A dependency renders as a sibling position: st_design (#4) waits on
	// st_requirement (#3).
	if !strings.Contains(list, "completed") || !strings.Contains(list, "#3") {
		t.Errorf("stepper does not show the stage dependency or its reason")
	}
	// Engine order in the document, position numbers on each head.
	for index, stage := range deliveryFullStages {
		if !strings.Contains(list, "#"+strconv.Itoa(index+1)+" ") && !strings.Contains(list, "#"+strconv.Itoa(index+1)+"<") {
			t.Errorf("stage %q does not show position #%d", stage.Slug, index+1)
		}
	}

	// AC-16's second half: the test mutates nothing, and rendering wrote nothing.
	after := deliveryFullSnapshot(t, ctx, containerID)
	if before != after {
		t.Errorf("rendering the editor changed stored data:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	steps, err := db.ListDefinitionSteps(ctx, containerID)
	if err != nil {
		t.Fatalf("reload steps: %v", err)
	}
	if len(steps) != len(deliveryFullStages) {
		t.Errorf("reload shows %d steps, want %d", len(steps), len(deliveryFullStages))
	}
	for index, step := range steps {
		if step.ChildSlug != deliveryFullStages[index].Slug {
			t.Errorf("reload step %d = %q, want %q", index, step.ChildSlug, deliveryFullStages[index].Slug)
		}
	}
}
