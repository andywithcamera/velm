package main

// Slice 5 of the designer rework: the `retry` control block (Design Vision
// D17/D18; Requirements AC-75 to AC-92). Real Postgres, gated on
// TEST_DATABASE_URL: a skip is a broken run, not a pass.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"velm/internal/db"
)

// retryBlock is the document-side shape of a retry block in a test payload.
func retryBlock(key, label, parent string, config map[string]any) db.StructureBlock {
	return db.StructureBlock{Key: key, Kind: "control", Type: "retry", Label: label, Parent: parent, Config: config}
}

// retryDocument wraps a retry block in an `if` scope, because AC-83 refuses a
// root retry: a save that is meant to succeed must be a legal document.
func retryDocument(retry db.StructureBlock) []db.StructureBlock {
	return []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "if", Kind: "control", Type: "if", Label: "Gate", Position: 10,
			Config: map[string]any{"condition": "expr: true"}},
		retry,
	}
}

func storedRetryConfig(t *testing.T, ctx context.Context, containerID, label string) map[string]any {
	t.Helper()
	var raw []byte
	if err := db.Pool.QueryRow(ctx, `
		SELECT config FROM base_task_definition_child
		WHERE container_definition_id = $1 AND block_type = 'retry' AND config->>'label' = $2`,
		containerID, label).Scan(&raw); err != nil {
		t.Fatalf("read stored retry config: %v", err)
	}
	config := map[string]any{}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode stored retry config: %v", err)
	}
	return config
}

// AC-75 and AC-78: retry is a CONTROL block of type retry, accepted by the
// block-type check, and attempts 10 is stored at config.attempts.
func TestRetryBlockSaves(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice5-saves")
	containerID := insertDefinition(t, ctx, slug, "Retry saves", "", tdeTwoInputs, true, true)

	rec := postStructureAs(t, slug, retryDocument(
		retryBlock("r", "Flaky", "if", map[string]any{"attempts": 10})), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry save = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var kind, typ string
	if err := db.Pool.QueryRow(ctx, `
		SELECT block_kind, COALESCE(block_type, '') FROM base_task_definition_child
		WHERE container_definition_id = $1 AND config->>'label' = 'Flaky'`,
		containerID).Scan(&kind, &typ); err != nil {
		t.Fatalf("read stored retry block: %v", err)
	}
	if kind != "control" || typ != "retry" {
		t.Fatalf("stored block is kind=%q type=%q, want control/retry", kind, typ)
	}
	config := storedRetryConfig(t, ctx, containerID, "Flaky")
	if got := strings.TrimSpace(toStringValue(config["attempts"])); got != "10" {
		t.Fatalf("stored config.attempts = %q, want 10", got)
	}
}

func toStringValue(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(strings.Trim(fmt.Sprint(v), `"`))
}

// AC-76: retry is control flow, not a capability, so every author sees it.
func TestRetryNotAdminGated(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice5-palette")
	insertDefinition(t, ctx, slug, "Retry palette", "", tdeTwoInputs, true, true)
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
	})

	editorBody := renderDefinitionViewAs(t, slug, "", false).Body.String()
	if !strings.Contains(editorBody, `data-block-type="retry"`) {
		t.Errorf("non-admin palette has no retry: %s", editorBody)
	}
	adminBody := renderDefinitionViewAs(t, slug, "", true).Body.String()
	if !strings.Contains(adminBody, `data-block-type="retry"`) {
		t.Errorf("admin palette has no retry")
	}
	// The admin gate belongs to run_script and must not have leaked onto retry.
	if strings.Contains(editorBody, `data-block-type="run_script"`) {
		t.Errorf("non-admin palette offers run_script; the gate moved")
	}
}

// AC-77: absent means 1; 0 and 11 are refused naming the block and attempts.
func TestRetryAttemptsBounds(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	for _, tc := range []struct {
		name     string
		attempts any
		want     int
	}{
		{"zero", 0, http.StatusBadRequest},
		{"eleven", 11, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slug := tdeSlug(t, "slice5-attempts-"+tc.name)
			containerID := insertDefinition(t, ctx, slug, "Attempts "+tc.name, "", tdeTwoInputs, true, true)
			before := storedBlockCount(t, ctx, containerID)

			rec := postStructureAs(t, slug, retryDocument(
				retryBlock("r", "Flaky", "if", map[string]any{"attempts": tc.attempts})), true)
			if rec.Code != tc.want {
				t.Fatalf("attempts %v save = %d, want %d: %s", tc.attempts, rec.Code, tc.want, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, "attempts must be between 1 and 10") {
				t.Errorf("refusal does not name the attempts bound: %s", body)
			}
			if !strings.Contains(body, "Flaky") {
				t.Errorf("refusal does not name the block: %s", body)
			}
			if after := storedBlockCount(t, ctx, containerID); after != before {
				t.Fatalf("a refused save changed the tree: %d -> %d", before, after)
			}
		})
	}

	// Absent attempts saves: the default is 1, applied by the runtime.
	slug := tdeSlug(t, "slice5-attempts-absent")
	insertDefinition(t, ctx, slug, "Attempts absent", "", tdeTwoInputs, true, true)
	if rec := postStructureAs(t, slug, retryDocument(retryBlock("r", "Flaky", "if", nil)), true); rec.Code != http.StatusOK {
		t.Fatalf("retry without attempts = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// AC-78 is asserted in TestRetryBlockSaves; AC-79 here.
func TestRetryDelayBounds(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	for _, tc := range []struct {
		name  string
		delay any
		ok    bool
	}{
		{"minus one", -1, false},
		{"3601", 3601, false},
		{"zero", 0, true},
		{"3600", 3600, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slug := tdeSlug(t, "slice5-delay-"+strings.ReplaceAll(tc.name, " ", "-"))
			insertDefinition(t, ctx, slug, "Delay "+tc.name, "", tdeTwoInputs, true, true)
			rec := postStructureAs(t, slug, retryDocument(
				retryBlock("r", "Flaky", "if", map[string]any{"delay_seconds": tc.delay})), true)
			if tc.ok {
				if rec.Code != http.StatusOK {
					t.Fatalf("delay %v save = %d, want 200: %s", tc.delay, rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("delay %v save = %d, want 400", tc.delay, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "delay_seconds must be between 0 and 3600") {
				t.Errorf("refusal does not name delay_seconds: %s", rec.Body.String())
			}
		})
	}
}

// AC-80: backoff is one of none/linear/exponential and nothing else.
func TestRetryBackoffVocabulary(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	for _, tc := range []struct {
		name    string
		backoff string
		ok      bool
	}{
		{"none", "none", true},
		{"linear", "linear", true},
		{"exponential", "exponential", true},
		{"quadratic", "quadratic", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slug := tdeSlug(t, "slice5-backoff-"+tc.name)
			insertDefinition(t, ctx, slug, "Backoff "+tc.name, "", tdeTwoInputs, true, true)
			rec := postStructureAs(t, slug, retryDocument(
				retryBlock("r", "Flaky", "if", map[string]any{"backoff": tc.backoff})), true)
			if tc.ok {
				if rec.Code != http.StatusOK {
					t.Fatalf("backoff %q save = %d, want 200: %s", tc.backoff, rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("backoff %q save = %d, want 400", tc.backoff, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "backoff must be none, linear or exponential") {
				t.Errorf("refusal does not name backoff: %s", rec.Body.String())
			}
		})
	}
}

// AC-81: on_exhausted is fail or continue; absent means fail.
func TestRetryOnExhaustedVocabulary(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	for _, tc := range []struct {
		name  string
		value string
		ok    bool
	}{
		{"fail", "fail", true},
		{"continue", "continue", true},
		{"retry", "retry", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slug := tdeSlug(t, "slice5-exhausted-"+tc.name)
			insertDefinition(t, ctx, slug, "Exhausted "+tc.name, "", tdeTwoInputs, true, true)
			rec := postStructureAs(t, slug, retryDocument(
				retryBlock("r", "Flaky", "if", map[string]any{"on_exhausted": tc.value})), true)
			if tc.ok {
				if rec.Code != http.StatusOK {
					t.Fatalf("on_exhausted %q save = %d, want 200: %s", tc.value, rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("on_exhausted %q save = %d, want 400", tc.value, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "on_exhausted must be fail or continue") {
				t.Errorf("refusal does not name on_exhausted: %s", rec.Body.String())
			}
		})
	}
}

// AC-82: a retry inside a retry is refused at save, naming both blocks.
func TestNestedRetryRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice5-nested")
	containerID := insertDefinition(t, ctx, slug, "Nested retry", "", tdeTwoInputs, true, true)
	before := storedBlockCount(t, ctx, containerID)

	blocks := []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "if", Kind: "control", Type: "if", Label: "Gate", Position: 10,
			Config: map[string]any{"condition": "expr: true"}},
		retryBlock("outer", "Outer retry", "if", map[string]any{"attempts": 2}),
		retryBlock("inner", "Inner retry", "outer", map[string]any{"attempts": 2}),
	}
	rec := postStructureAs(t, slug, blocks, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("nested retry save = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "a retry block cannot contain another retry") {
		t.Errorf("refusal does not name the rule: %s", body)
	}
	if !strings.Contains(body, "Outer retry") || !strings.Contains(body, "Inner retry") {
		t.Errorf("refusal does not name both blocks: %s", body)
	}
	if after := storedBlockCount(t, ctx, containerID); after != before {
		t.Fatalf("a refused save changed the tree: %d -> %d", before, after)
	}
}

// AC-83: a root retry is refused at save.
func TestRootRetryRefused(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice5-root")
	containerID := insertDefinition(t, ctx, slug, "Root retry", "", tdeTwoInputs, true, true)
	before := storedBlockCount(t, ctx, containerID)

	rec := postStructureAs(t, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		retryBlock("r", "Root retry", "", map[string]any{"attempts": 2}),
	}, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("root retry save = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "a retry block needs a parent scope") {
		t.Errorf("refusal does not name the rule: %s", rec.Body.String())
	}
	if after := storedBlockCount(t, ctx, containerID); after != before {
		t.Fatalf("a refused save changed the tree: %d -> %d", before, after)
	}
}

// AC-92, second branch: there is no inline retry policy. A plain task block's
// card carries no retry fields, so there is exactly one way to express a
// retry: the retry block itself (D18).
func TestNoInlineRetryField(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "slice5-inline")
	childSlug, _ := canvasTaskBlock(t, ctx, "slice5-inline-leaf")
	insertDefinition(t, ctx, slug, "Inline retry", "", tdeTwoInputs, true, true)
	seedCanvasTree(t, ctx, slug, []db.StructureBlock{
		{Key: "t", Kind: "trigger", Label: "Start", Position: 0},
		{Key: "a", Kind: "task", Slug: childSlug, Label: "Work", Position: 10},
	})

	body := renderDefinitionViewAs(t, slug, "", true).Body.String()
	fragment := fragmentForLabel(body, "Work")
	if fragment == "" {
		t.Fatalf("task block card not found in the rendered page")
	}
	for _, field := range []string{`data-config="attempts"`, `data-config="backoff"`, `data-config="on_exhausted"`} {
		if strings.Contains(fragment, field) {
			t.Errorf("task block card renders an inline retry field %s; D18 ships the retry block alone", field)
		}
	}
}
