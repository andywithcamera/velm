package db

import (
	"context"
	"strings"
	"testing"
)

// demoEvaluator builds a read-restricted evaluator for one app: a deny rule that
// hides closed rows from the reader role, then an allow rule for everything
// else. Rules are ordered and the first match decides, so the deny has to come
// first — an earlier allow would shadow it.
func demoEvaluator(userID string) *TableSecurityEvaluator {
	userRoles := map[string]bool{}
	if strings.TrimSpace(userID) != "" {
		userRoles["demo.task_reader"] = true
	}
	return &TableSecurityEvaluator{
		app:       RegisteredApp{Name: "demo", Namespace: "demo"},
		tableName: "base_task",
		rules: []AppDefinitionSecurityRule{
			{Name: "hide_closed", Effect: "deny", Operation: "R", Role: "task_reader", Condition: `state == "closed"`, Order: 10},
			{Name: "reader_can_read", Operation: "R", Role: "task_reader", Order: 20},
		},
		userRoles: userRoles,
	}
}

func TestScriptActorForOptionsDoesNotInferSystem(t *testing.T) {
	// An empty user ID must not be upgraded to the system actor: that would
	// hand every row to a caller that simply forgot to pass an identity.
	actor := scriptActorForOptions(ScriptExecutionOptions{})
	if actor.IsSystem() {
		t.Fatal("empty UserID must not be treated as a system run")
	}
	if actor.UserID() != "" {
		t.Fatalf("UserID = %q, want empty", actor.UserID())
	}

	if !scriptActorForOptions(ScriptExecutionOptions{System: true}).IsSystem() {
		t.Fatal("System: true must produce the system actor")
	}

	// System run on behalf of a user: rules off, identity kept for audit.
	both := scriptActorForOptions(ScriptExecutionOptions{System: true, UserID: "user-1"})
	if !both.IsSystem() || both.UserID() != "user-1" {
		t.Fatalf("actor = %+v, want system with UserID user-1", both)
	}

	user := scriptActorForOptions(ScriptExecutionOptions{UserID: " user-2 "})
	if user.IsSystem() || user.UserID() != "user-2" {
		t.Fatalf("actor = %+v, want trimmed user-2", user)
	}
}

func TestScriptAccessSystemActorIgnoresRules(t *testing.T) {
	access := newScriptAccess(context.Background(), SystemScriptActor())
	access.loadRules = func(_ context.Context, _, userID string) (*TableSecurityEvaluator, error) {
		return demoEvaluator(userID), nil
	}

	restricted, err := access.restricts("base_task", securityOperationRead)
	if err != nil {
		t.Fatalf("restricts() error = %v", err)
	}
	if restricted {
		t.Fatal("system actor must not be restricted")
	}

	rows := []map[string]any{{"_id": "1", "state": "closed"}, {"_id": "2", "state": "open"}}
	visible, err := access.filter("base_task", securityOperationRead, rows)
	if err != nil {
		t.Fatalf("filter() error = %v", err)
	}
	if len(visible) != 2 {
		t.Fatalf("system actor saw %d rows, want all 2", len(visible))
	}
}

func TestScriptAccessAnonymousActorFailsClosed(t *testing.T) {
	// No identity at all: the role gate cannot match, so nothing is readable.
	access := newScriptAccess(context.Background(), UserScriptActor(""))
	access.loadRules = func(_ context.Context, _, userID string) (*TableSecurityEvaluator, error) {
		return demoEvaluator(userID), nil
	}

	allowed, err := access.allows("base_task", securityOperationRead, map[string]any{"_id": "1", "state": "open"})
	if err != nil {
		t.Fatalf("allows() error = %v", err)
	}
	if allowed {
		t.Fatal("anonymous actor must be refused wherever a rule exists")
	}
}

func TestScriptAccessFiltersRowsHiddenFromRole(t *testing.T) {
	access := newScriptAccess(context.Background(), UserScriptActor("user-1"))
	access.loadRules = func(_ context.Context, tableName, userID string) (*TableSecurityEvaluator, error) {
		if tableName != "base_task" {
			t.Fatalf("loadRules table = %q, want base_task", tableName)
		}
		if userID != "user-1" {
			t.Fatalf("loadRules userID = %q, want user-1", userID)
		}
		return demoEvaluator(userID), nil
	}

	restricted, err := access.restricts("base_task", securityOperationRead)
	if err != nil {
		t.Fatalf("restricts() error = %v", err)
	}
	if !restricted {
		t.Fatal("table with read rules must be reported as restricted")
	}

	// Closed rows are hidden, so the caller gets fewer rows than it asked for
	// and two of the three come back.
	rows := []map[string]any{
		{"_id": "1", "state": "closed"},
		{"_id": "2", "state": "open"},
		{"_id": "3", "state": "open"},
	}
	visible, err := access.filter("base_task", securityOperationRead, rows)
	if err != nil {
		t.Fatalf("filter() error = %v", err)
	}
	if len(visible) != 2 {
		t.Fatalf("visible = %d rows, want 2", len(visible))
	}
	for _, row := range visible {
		if row["state"] == "closed" {
			t.Fatalf("filter leaked closed row %#v", row)
		}
	}
}

func TestScriptAccessCachesEvaluatorPerTable(t *testing.T) {
	access := newScriptAccess(context.Background(), UserScriptActor("user-1"))
	loads := 0
	access.loadRules = func(_ context.Context, _, userID string) (*TableSecurityEvaluator, error) {
		loads++
		return demoEvaluator(userID), nil
	}

	for range 3 {
		if _, err := access.allows("base_task", securityOperationRead, map[string]any{"state": "open"}); err != nil {
			t.Fatalf("allows() error = %v", err)
		}
	}
	if loads != 1 {
		t.Fatalf("rules loaded %d times, want 1", loads)
	}
}

func TestScriptAccessUnrestrictedTablePassesThrough(t *testing.T) {
	access := newScriptAccess(context.Background(), UserScriptActor("user-1"))
	access.loadRules = func(context.Context, string, string) (*TableSecurityEvaluator, error) {
		return nil, nil // no app definition covers this table
	}

	restricted, err := access.restricts("base_task", securityOperationRead)
	if err != nil {
		t.Fatalf("restricts() error = %v", err)
	}
	if restricted {
		t.Fatal("table with no rules must not be restricted")
	}

	allowed, err := access.allows("base_task", securityOperationRead, map[string]any{"_id": "1"})
	if err != nil {
		t.Fatalf("allows() error = %v", err)
	}
	if !allowed {
		t.Fatal("table with no rules must allow every row")
	}
}

func TestDenyRecordReadsAsNotFound(t *testing.T) {
	err := denyRecordError("base_task", "42")
	message := err.Error()
	if !strings.Contains(message, "not found") {
		t.Fatalf("error = %q, want it to read as not found", message)
	}
	if strings.Contains(strings.ToLower(message), "denied") || strings.Contains(strings.ToLower(message), "forbidden") {
		t.Fatalf("error = %q, must not confirm the record exists", message)
	}
	// Same wording the missing-record path produces, so the two are
	// indistinguishable to a script.
	if message != `record "42" not found in "base_task"` {
		t.Fatalf("error = %q, want the missing-record wording", message)
	}
}
