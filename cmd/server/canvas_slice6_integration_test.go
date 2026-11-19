package main

// Slice 6 of the designer rework: the `run_task` control block, its input
// mapping and the save-time composition refusals (Design Vision D4, D8, D9;
// Requirements AC-93 to AC-104, AC-110). Real Postgres, gated on
// TEST_DATABASE_URL: a skip is a broken run, not a pass.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"velm/internal/db"
)

// runTaskBlock is the document-side shape of a run_task block.
func runTaskBlock(key, label, parent, target string, mapping map[string]any) db.StructureBlock {
	return db.StructureBlock{Key: key, Kind: "control", Type: "run_task", Label: label, Parent: parent, Slug: target, Mapping: mapping}
}

func runTaskDocument(block db.StructureBlock) []db.StructureBlock {
	return []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		block,
	}
}

// seedRunTaskTarget seeds a live leaf definition with the given input schema.
func seedRunTaskTarget(t *testing.T, ctx context.Context, name, schema string) string {
	t.Helper()
	slug := tdeSlug(t, name)
	insertDefinition(t, ctx, slug, titleizeBreadcrumbLabel(name), "", schema, false, true)
	return slug
}

func storedRunTaskMapping(t *testing.T, ctx context.Context, containerID, label string) map[string]any {
	t.Helper()
	var raw []byte
	if err := db.Pool.QueryRow(ctx, `
		SELECT input_mapping FROM base_task_definition_child
		WHERE container_definition_id = $1 AND block_type = 'run_task' AND config->>'label' = $2`,
		containerID, label).Scan(&raw); err != nil {
		t.Fatalf("read stored run_task mapping: %v", err)
	}
	mapping := map[string]any{}
	if err := jsonUnmarshal(raw, &mapping); err != nil {
		t.Fatalf("decode stored run_task mapping: %v", err)
	}
	return mapping
}

// AC-93: run_task is a control block accepted by the block-type check.
func TestRunTaskBlockSaves(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-saves")
	containerID := insertDefinition(t, ctx, slug, "Run task saves", "", tdeTwoInputs, true, true)
	target := seedRunTaskTarget(t, ctx, "slice6-saves-child", tdeTwoInputs)

	rec := postStructureAs(t, slug, runTaskDocument(
		runTaskBlock("rt", "Call reboot", "", target, map[string]any{
			"server": map[string]any{"from": "expr", "value": "$inputs.server"},
		})), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("run_task save = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var kind, typ string
	if err := db.Pool.QueryRow(ctx, `
		SELECT block_kind, COALESCE(block_type, '') FROM base_task_definition_child
		WHERE container_definition_id = $1 AND config->>'label' = 'Call reboot'`,
		containerID).Scan(&kind, &typ); err != nil {
		t.Fatalf("read stored run_task block: %v", err)
	}
	if kind != "control" || typ != "run_task" {
		t.Fatalf("stored block is kind=%q type=%q, want control/run_task", kind, typ)
	}
}

// AC-94: run_task is composition, not a capability, so every author sees it.
func TestRunTaskInPalette(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-palette")
	insertDefinition(t, ctx, slug, "Run task palette", "", "", true, true)
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
	})
	if body := renderDefinitionViewAs(t, slug, "", false).Body.String(); !strings.Contains(body, `data-block-type="run_task"`) {
		t.Errorf("non-admin palette has no run_task")
	}
	if body := renderDefinitionViewAs(t, slug, "", true).Body.String(); !strings.Contains(body, `data-block-type="run_task"`) {
		t.Errorf("admin palette has no run_task")
	}
}

// AC-95: the target select lists live definitions except the container itself,
// and a deleted definition is not a target.
func TestRunTaskTargetExcludesSelfAndDeleted(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-targets")
	insertDefinition(t, ctx, slug, "Run task targets", "", "", true, true)
	live := seedRunTaskTarget(t, ctx, "slice6-target-live", "")
	deleted := seedRunTaskTarget(t, ctx, "slice6-target-deleted", "")
	if _, err := db.Pool.Exec(ctx, `UPDATE base_task_definition SET _deleted_at = now() WHERE slug = $1`, deleted); err != nil {
		t.Fatalf("soft-delete target: %v", err)
	}
	seedCanvasTree(t, ctx, slug, runTaskDocument(runTaskBlock("rt", "Call it", "", live, nil)))

	var blockID string
	if err := db.Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task_definition_child
		WHERE container_definition_id = (SELECT _id FROM base_task_definition WHERE slug = $1)
		  AND block_type = 'run_task'`, slug).Scan(&blockID); err != nil {
		t.Fatalf("read run_task block id: %v", err)
	}
	body := renderDefinitionViewAs(t, slug, "?view=canvas&block="+blockID, false).Body.String()
	if !strings.Contains(body, `value="`+live+`"`) {
		t.Errorf("target select is missing the live definition %s", live)
	}
	if strings.Contains(body, `value="`+deleted+`"`) {
		t.Errorf("target select offers the deleted definition %s", deleted)
	}
	if strings.Contains(body, `value="`+slug+`"`) {
		t.Errorf("target select offers the container itself %s", slug)
	}
}

// AC-96: the stored mapping has exactly the target's declared inputs, so a
// row for an input the target does not declare is dropped.
func TestRunTaskMappingKeysMatchSchema(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-mapping")
	containerID := insertDefinition(t, ctx, slug, "Run task mapping", "", tdeTwoInputs, true, true)
	target := seedRunTaskTarget(t, ctx, "slice6-mapping-child", tdeTwoInputs)

	rec := postStructureAs(t, slug, runTaskDocument(
		runTaskBlock("rt", "Call it", "", target, map[string]any{
			"server": map[string]any{"from": "expr", "value": "$inputs.server"},
			"reason": map[string]any{"from": "literal", "value": "post-upgrade"},
			"bogus":  map[string]any{"from": "literal", "value": "not an input"},
		})), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("run_task save = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	mapping := storedRunTaskMapping(t, ctx, containerID, "Call it")
	if len(mapping) != 2 {
		t.Fatalf("stored mapping has %d keys, want the target's 2: %#v", len(mapping), mapping)
	}
	for _, name := range []string{"server", "reason"} {
		if _, present := mapping[name]; !present {
			t.Errorf("stored mapping is missing %q: %#v", name, mapping)
		}
	}
	if _, present := mapping["bogus"]; present {
		t.Errorf("stored mapping kept a key the target does not declare: %#v", mapping)
	}
}

// AC-96: a target with no inputs stores {} and the save still succeeds.
func TestRunTaskZeroInputTarget(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-zero")
	containerID := insertDefinition(t, ctx, slug, "Run task zero", "", "", true, true)
	target := seedRunTaskTarget(t, ctx, "slice6-zero-child", "")

	rec := postStructureAs(t, slug, runTaskDocument(
		runTaskBlock("rt", "No inputs", "", target, nil)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("zero-input run_task save = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if mapping := storedRunTaskMapping(t, ctx, containerID, "No inputs"); len(mapping) != 0 {
		t.Fatalf("stored mapping = %#v, want {}", mapping)
	}
}

// AC-97: the expanded card renders one mapping row per target input, labelled
// with the input's label.
func TestRunTaskRendersRowPerInput(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-rows")
	insertDefinition(t, ctx, slug, "Run task rows", "", tdeTwoInputs, true, true)
	target := seedRunTaskTarget(t, ctx, "slice6-rows-child", tdeTwoInputs)
	seedCanvasTree(t, ctx, slug, runTaskDocument(runTaskBlock("rt", "Call it", "", target, nil)))

	var blockID string
	if err := db.Pool.QueryRow(ctx, `
		SELECT _id::text FROM base_task_definition_child
		WHERE container_definition_id = (SELECT _id FROM base_task_definition WHERE slug = $1)
		  AND block_type = 'run_task'`, slug).Scan(&blockID); err != nil {
		t.Fatalf("read run_task block id: %v", err)
	}
	body := renderDefinitionViewAs(t, slug, "?view=canvas&block="+blockID, false).Body.String()
	for _, want := range []string{
		`data-config-mapping="server"`,
		`data-config-mapping="reason"`,
		`>Server<`,
		`>Reason<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expanded run_task card is missing %q", want)
		}
	}
}

// AC-98: an expr: source that does not resolve against the parent scope is
// refused 400, naming the block and the input, and the stored tree is intact.
func TestRunTaskUnresolvableExpressionRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-unresolvable")
	containerID := insertDefinition(t, ctx, slug, "Run task unresolvable", "", tdeTwoInputs, true, true)
	target := seedRunTaskTarget(t, ctx, "slice6-unresolvable-child", tdeTwoInputs)
	seedCanvasTree(t, ctx, slug, runTaskDocument(runTaskBlock("rt", "Old call", "", target, nil)))
	before := storedBlockCount(t, ctx, containerID)

	rec := postStructureAs(t, slug, runTaskDocument(
		runTaskBlock("rt", "Call it", "", target, map[string]any{
			"server": map[string]any{"from": "expr", "value": "expr: $inputs.nope"},
			"reason": map[string]any{"from": "literal", "value": "post-upgrade"},
		})), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unresolvable expression save = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"run task " + target, "input server cannot be resolved"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("refusal does not contain %q: %s", want, rec.Body.String())
		}
	}
	if after := storedBlockCount(t, ctx, containerID); after != before {
		t.Fatalf("refused save changed the stored tree: %d rows, want %d", after, before)
	}
}

// AC-101: a required input left unmapped is refused with the named message.
func TestRunTaskMissingRequiredInputRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-required")
	insertDefinition(t, ctx, slug, "Run task required", "", tdeTwoInputs, true, true)
	target := seedRunTaskTarget(t, ctx, "slice6-required-child", tdeTwoInputs)

	rec := postStructureAs(t, slug, runTaskDocument(
		runTaskBlock("rt", "Call it", "", target, map[string]any{
			"reason": map[string]any{"from": "literal", "value": "post-upgrade"},
		})), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing required input save = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	want := "run task " + target + ": input server is required"
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("refusal does not contain %q: %s", want, rec.Body.String())
	}
}

// AC-102: a target slug that no longer exists is refused at save.
func TestRunTaskUnknownTargetRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-unknown")
	insertDefinition(t, ctx, slug, "Run task unknown", "", "", true, true)
	missing := tdeSlug(t, "slice6-does-not-exist")

	rec := postStructureAs(t, slug, runTaskDocument(
		runTaskBlock("rt", "Call it", "", missing, nil)), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown target save = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if want := "that definition does not exist: " + missing; !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("refusal does not contain %q: %s", want, rec.Body.String())
	}
}

// AC-103: a run_task naming its own definition is refused at save.
func TestRunTaskSelfCallRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-self")
	insertDefinition(t, ctx, slug, "Run task self", "", "", true, true)

	rec := postStructureAs(t, slug, runTaskDocument(
		runTaskBlock("rt", "Call itself", "", slug, nil)), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self-call save = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	want := "calls " + slug + ", which contains this definition: that cycle cannot run"
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("refusal does not contain %q: %s", want, rec.Body.String())
	}
}

// AC-104: an indirect cycle is refused at save at one hop and at two.
func TestRunTaskIndirectCycleRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	for _, tc := range []struct {
		name  string
		middle []string
	}{
		{"one hop", []string{"b"}},
		{"two hops", []string{"b", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := taskDefinitionsTestDB(t)
			upgrade := tdeSlug(t, "slice6-cycle-upgrade")
			insertDefinition(t, ctx, upgrade, "Upgrade", "", "", true, true)
			middleSlugs := make([]string, 0, len(tc.middle))
			for _, name := range tc.middle {
				middleSlug := tdeSlug(t, "slice6-cycle-"+name)
				insertDefinition(t, ctx, middleSlug, "Middle "+name, "", "", true, true)
				middleSlugs = append(middleSlugs, middleSlug)
			}
			// Build the tail of the cycle: the last middle calls upgrade, each
			// earlier middle calls the one after it.
			for index := len(middleSlugs) - 1; index >= 0; index-- {
				target := upgrade
				if index+1 < len(middleSlugs) {
					target = middleSlugs[index+1]
				}
				if rec := postStructureAs(t, middleSlugs[index], runTaskDocument(
					runTaskBlock("rt", "Next", "", target, nil)), false); rec.Code != http.StatusOK {
					t.Fatalf("seed middle %d = %d, want 200: %s", index, rec.Code, rec.Body.String())
				}
			}
			// Now upgrade calls the head of the chain: a cycle.
			rec := postStructureAs(t, upgrade, runTaskDocument(
				runTaskBlock("rt", "Call next", "", middleSlugs[0], nil)), false)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("indirect cycle save = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			want := "which contains this definition: that cycle cannot run"
			if !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("refusal does not contain %q: %s", want, rec.Body.String())
			}
		})
	}
}

// AC-110: save, load and re-save preserves the target and both mapping modes.
func TestRunTaskRoundTrip(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice6-roundtrip")
	containerID := insertDefinition(t, ctx, slug, "Run task round trip", "", tdeTwoInputs, true, true)
	target := seedRunTaskTarget(t, ctx, "slice6-roundtrip-child", tdeTwoInputs)

	want := map[string]any{
		"server": map[string]any{"from": "expr", "value": "$inputs.server"},
		"reason": map[string]any{"from": "literal", "value": "$5 credit, unchanged"},
	}
	rec := postStructureAs(t, slug, runTaskDocument(
		runTaskBlock("rt", "Call it", "", target, want)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("first save = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Load through the same document the canvas reads, then save it again.
	blocks, err := db.LoadDefinitionDocument(ctx, containerID)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	document := documentToStructure(blocks)
	rec = postStructureAs(t, slug, document, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-save = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := storedRunTaskMapping(t, ctx, containerID, "Call it")
	for name, raw := range want {
		row, ok := got[name].(map[string]any)
		if !ok {
			t.Fatalf("re-saved mapping %q is %#v, want an object", name, got[name])
		}
		wantRow := raw.(map[string]any)
		if row["from"] != wantRow["from"] || row["value"] != wantRow["value"] {
			t.Errorf("re-saved mapping %q = %#v, want %#v", name, row, wantRow)
		}
	}
}
