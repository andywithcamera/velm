package db

import (
	"fmt"
	"testing"
	"time"
)

// #204: a definition that declares inputs must collect them at order time.
// Before this, input_schema_yaml had no reader at all, so "Order" spawned a
// task with an empty input set and the doer never saw the request.
func TestOrderedTaskCarriesDeclaredInputs(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, _ := seedActorAndGroup(ctx, t)

	defID := seedDefinition(ctx, t, "in-doc-"+suffix, "Create Doc", false, "")
	_, err := Pool.Exec(ctx, `
		UPDATE base_task_definition SET input_schema_yaml = $2 WHERE _id::text = $1`,
		defID, "- name: description\n  label: What should the doc be about?\n  data_type: long_text\n  is_nullable: false\n")
	if err != nil {
		t.Fatalf("seed input schema: %v", err)
	}

	fields, err := ParseTaskInputSchema("- name: description\n  label: What should the doc be about?\n  data_type: long_text\n")
	if err != nil {
		t.Fatalf("ParseTaskInputSchema: %v", err)
	}
	inputs, err := TaskInputValues(fields, map[string]string{"description": "write me a doc about cheese"})
	if err != nil {
		t.Fatalf("TaskInputValues: %v", err)
	}

	taskID, err := SpawnTaskInstanceBySlug(ctx, Pool, "in-doc-"+suffix, "", actorID, inputs)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// The request must reach the row the engine writes, not just the caller.
	vars := taskVars(ctx, t, taskID)
	got, ok := vars["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("variables.inputs missing: %+v", vars)
	}
	if got["description"] != "write me a doc about cheese" {
		t.Fatalf("inputs.description = %v", got["description"])
	}

	// And it must be readable by the doer's record page (#262).
	view, err := LoadTaskRequest(ctx, taskID)
	if err != nil {
		t.Fatalf("LoadTaskRequest: %v", err)
	}
	if !view.HasInputs || len(view.Inputs) != 1 {
		t.Fatalf("request view inputs = %+v", view.Inputs)
	}
	if view.Inputs[0].Label != "What should the doc be about?" {
		t.Fatalf("label = %q", view.Inputs[0].Label)
	}
	if view.Inputs[0].Value != "write me a doc about cheese" {
		t.Fatalf("value = %q", view.Inputs[0].Value)
	}
	if view.DefinitionName != "Create Doc" {
		t.Fatalf("definition name = %q", view.DefinitionName)
	}
}

// #262: a task with no definition must render cleanly rather than error.
func TestLoadTaskRequestWithoutDefinition(t *testing.T) {
	ctx := taskEngineTestDB(t)
	if _, err := LoadTaskRequest(ctx, ""); err != nil {
		t.Fatalf("empty id must not fail: %v", err)
	}
}

// A chain is one conversation. A comment written on stage one must be readable
// from stage four, because a rejection with no visible reason is the symptom
// that made this necessary.
func TestChainCommentsSpanEveryStage(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, groupID := seedActorAndGroup(ctx, t)

	draftDef := seedDefinition(ctx, t, "cc-draft-"+suffix, "Draft", false, "")
	reviewDef := seedDefinition(ctx, t, "cc-review-"+suffix, "Review", false, "")
	containerDef := seedDefinition(ctx, t, "cc-doc-"+suffix, "Create Doc", true, "")
	seedChildLinkWithDeps(ctx, t, containerDef, draftDef, 0, nil, "")
	seedChildLinkWithDeps(ctx, t, containerDef, reviewDef, 1, []string{draftDef}, "")

	rootID, err := SpawnTaskInstance(ctx, Pool, containerDef, "", actorID, map[string]any{"inputs": map[string]any{"description": "cheese"}})
	if err != nil {
		t.Fatalf("spawn container: %v", err)
	}
	assignAll(ctx, t, rootID, groupID, actorID)
	draftID := childByDef(ctx, t, rootID, draftDef)
	reviewID := childByDef(ctx, t, rootID, reviewDef)
	if draftID == "" || reviewID == "" {
		t.Fatalf("expected both stages, got draft=%q review=%q", draftID, reviewID)
	}

	if err := AddRecordComment(ctx, "base_task", draftID, actorID, "draft is attached"); err != nil {
		t.Fatalf("comment on draft: %v", err)
	}
	if err := AddRecordComment(ctx, "base_task", reviewID, actorID, "please spell it cheddar"); err != nil {
		t.Fatalf("comment on review: %v", err)
	}

	// Every stage reads the same thread...
	for _, stage := range []struct{ name, id string }{
		{"root", rootID}, {"draft", draftID}, {"review", reviewID},
	} {
		entries, err := ListRecordCommentEntries(ctx, "base_task", stage.id, 50)
		if err != nil {
			t.Fatalf("list comments on %s: %v", stage.name, err)
		}
		bodies := map[string]bool{}
		for _, entry := range entries {
			bodies[entry.Body] = true
		}
		if !bodies["draft is attached"] || !bodies["please spell it cheddar"] {
			t.Fatalf("%s sees %v, want both stages", stage.name, bodies)
		}
	}

	// ...and the stage a comment came from is recorded, so the reader can tell
	// whose remark it is.
	entries, err := ListRecordCommentEntries(ctx, "base_task", reviewID, 50)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	for _, entry := range entries {
		if entry.Body == "draft is attached" && entry.RecordID != draftID {
			t.Fatalf("draft comment recorded on record %q, want %q", entry.RecordID, draftID)
		}
		if entry.Body == "draft is attached" && entry.RecordLabel == "" {
			t.Fatalf("draft comment has no stage label")
		}
	}
}

// The widening must be confined to tasks: merging unrelated records' comments
// would be a worse bug than the one being fixed.
func TestNonTaskCommentsStayPerRecord(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, _ := seedActorAndGroup(ctx, t)

	const other = "cc_other_record"
	recordA := "cc-a-" + suffix
	recordB := "cc-b-" + suffix
	if err := AddRecordComment(ctx, other, recordA, actorID, "on a"); err != nil {
		t.Fatalf("comment a: %v", err)
	}
	if err := AddRecordComment(ctx, other, recordB, actorID, "on b"); err != nil {
		t.Fatalf("comment b: %v", err)
	}

	entries, err := ListRecordCommentEntries(ctx, other, recordA, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].Body != "on a" {
		t.Fatalf("record A sees %+v, want only its own comment", entries)
	}
}

// A choice is stored as its value but must be shown as its label: the request
// panel should read back the words the requester picked.
func TestRequestViewShowsChoiceLabelNotValue(t *testing.T) {
	ctx := taskEngineTestDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	actorID, _ := seedActorAndGroup(ctx, t)

	defID := seedDefinition(ctx, t, "in-choice-"+suffix, "Order Work", false, "")
	schema := "- name: priority\n  label: Priority\n  data_type: choice\n  choices:\n    - value: high\n      label: High\n"
	if _, err := Pool.Exec(ctx, `UPDATE base_task_definition SET input_schema_yaml = $2 WHERE _id::text = $1`, defID, schema); err != nil {
		t.Fatalf("seed schema: %v", err)
	}

	taskID, err := SpawnTaskInstanceBySlug(ctx, Pool, "in-choice-"+suffix, "", actorID, map[string]any{"priority": "high"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	view, err := LoadTaskRequest(ctx, taskID)
	if err != nil {
		t.Fatalf("LoadTaskRequest: %v", err)
	}
	if len(view.Inputs) != 1 || view.Inputs[0].Value != "High" {
		t.Fatalf("request view = %+v, want the choice label High", view.Inputs)
	}
}
