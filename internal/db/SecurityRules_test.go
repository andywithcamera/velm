package db

import (
	"context"
	"testing"
)

func TestLoadTableSecurityEvaluatorSkipsProtectedTables(t *testing.T) {
	evaluator, err := LoadTableSecurityEvaluator(context.Background(), "_audit_log", "user-1")
	if err != nil {
		t.Fatalf("LoadTableSecurityEvaluator() error = %v", err)
	}
	if evaluator != nil {
		t.Fatalf("expected protected table to skip app security evaluator, got %#v", evaluator)
	}
}

func TestTableSecurityEvaluatorAllowsFieldScopedUpdates(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		rules: []AppDefinitionSecurityRule{
			{
				Name:      "assignee_state",
				Operation: "U",
				Field:     "state",
				Role:      "task_assignee",
				Condition: `state == "in_progress"`,
				Order:     10,
			},
			{
				Name:      "manager_state",
				Operation: "U",
				Field:     "state",
				Role:      "task_manager",
				Order:     20,
			},
		},
		userRoles: map[string]bool{
			"demo.task_assignee": true,
		},
	}

	if !evaluator.AllowsField("U", "state", map[string]any{"state": "in_progress"}) {
		t.Fatal("expected assignee to update state while in progress")
	}
	if evaluator.AllowsField("U", "state", map[string]any{"state": "done"}) {
		t.Fatal("expected assignee to be blocked when condition fails")
	}
}

func TestTableSecurityEvaluatorUsesOrderedAllowAndDenyRules(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		rules: []AppDefinitionSecurityRule{
			{
				Name:      "deny_done_state",
				Effect:    "deny",
				Operation: "U",
				Field:     "state",
				Role:      "task_manager",
				Condition: `state == "done"`,
				Order:     10,
			},
			{
				Name:      "allow_manager_state",
				Operation: "U",
				Field:     "state",
				Role:      "task_manager",
				Order:     20,
			},
		},
		userRoles: map[string]bool{
			"demo.task_manager": true,
		},
	}

	if evaluator.AllowsField("U", "state", map[string]any{"state": "done"}) {
		t.Fatal("expected earlier deny rule to block state update")
	}
	if !evaluator.AllowsField("U", "state", map[string]any{"state": "in_progress"}) {
		t.Fatal("expected later allow rule to permit other state updates")
	}
}

func TestTableSecurityEvaluatorRequiresFieldRuleWhenPresent(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		rules: []AppDefinitionSecurityRule{
			{
				Name:      "editor_table",
				Operation: "U",
				Role:      "task_editor",
				Order:     10,
			},
			{
				Name:      "manager_state",
				Operation: "U",
				Field:     "state",
				Role:      "task_manager",
				Order:     20,
			},
		},
		userRoles: map[string]bool{
			"demo.task_editor": true,
		},
	}

	if !evaluator.AllowsFields("U", []string{"short_description"}, map[string]any{"state": "draft"}) {
		t.Fatal("expected table-level update rule to allow other fields")
	}
	if evaluator.AllowsFields("U", []string{"state"}, map[string]any{"state": "draft"}) {
		t.Fatal("expected state update to require the field-specific rule")
	}
}

func TestTableSecurityEvaluatorSupportsRecordLevelDeny(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		rules: []AppDefinitionSecurityRule{
			{
				Name:      "deny_closed_delete",
				Effect:    "deny",
				Operation: "D",
				Role:      "task_manager",
				Condition: `state == "closed"`,
				Order:     10,
			},
			{
				Name:      "allow_manager_delete",
				Operation: "D",
				Role:      "task_manager",
				Order:     20,
			},
		},
		userRoles: map[string]bool{
			"demo.task_manager": true,
		},
	}

	if evaluator.AllowsRecord("D", map[string]any{"state": "closed"}) {
		t.Fatal("expected closed record delete to be denied")
	}
	if !evaluator.AllowsRecord("D", map[string]any{"state": "in_progress"}) {
		t.Fatal("expected delete to be allowed when deny condition does not match")
	}
}

func TestTableSecurityEvaluatorRequiresConfiguredRoleGate(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		roleGate:  []string{"task_reader"},
		userRoles: map[string]bool{
			"demo.task_editor": true,
		},
	}

	if evaluator.AllowsRecord("R", map[string]any{"_id": "123"}) {
		t.Fatal("expected missing role gate to deny record access")
	}

	evaluator.userRoles["demo.task_reader"] = true
	if !evaluator.AllowsRecord("R", map[string]any{"_id": "123"}) {
		t.Fatal("expected matching role gate to allow record access")
	}
}

func TestTableSecurityEvaluatorAdminRoleBypassesReadSecurity(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		roleGate:  []string{"task_reader"},
		rules: []AppDefinitionSecurityRule{
			{
				Name:      "record_reader",
				Operation: "R",
				Role:      "task_reader",
				Order:     10,
			},
			{
				Name:      "title_reader",
				Operation: "R",
				Field:     "title",
				Role:      "task_reader",
				Order:     20,
			},
		},
		userRoles: map[string]bool{
			"admin": true,
		},
	}

	record, ok := FilterReadableRecord(evaluator, map[string]any{
		"_id":   "123",
		"title": "Visible",
		"state": "also-visible",
	})
	if !ok {
		t.Fatal("expected admin to read the record")
	}
	if got := record["state"]; got != "also-visible" {
		t.Fatalf("state = %#v, want %q", got, "also-visible")
	}
	if !evaluator.AllowsField("R", "title", record) {
		t.Fatal("expected admin to read fields without matching table rules")
	}
}

func TestTableSecurityEvaluatorAdminRoleDoesNotBypassWriteSecurity(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		rules: []AppDefinitionSecurityRule{
			{
				Name:      "manager_only_update",
				Operation: "U",
				Role:      "task_manager",
				Order:     10,
			},
		},
		userRoles: map[string]bool{
			"admin": true,
		},
	}

	if evaluator.AllowsRecord("U", map[string]any{"_id": "123"}) {
		t.Fatal("expected admin read bypass to remain read-only")
	}
}

func TestBuildTableSecuritySavePreviewBlocksDeniedChangedField(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		rules: []AppDefinitionSecurityRule{
			{
				Name:      "allow_editor_record",
				Operation: "U",
				Role:      "task_editor",
				Order:     10,
			},
			{
				Name:      "deny_done_state",
				Effect:    "deny",
				Operation: "U",
				Field:     "state",
				Role:      "task_editor",
				Condition: `state == "done"`,
				Order:     20,
			},
			{
				Name:      "allow_editor_state",
				Operation: "U",
				Field:     "state",
				Role:      "task_editor",
				Order:     30,
			},
		},
		userRoles: map[string]bool{
			"demo.task_editor": true,
		},
	}

	preview := BuildTableSecuritySavePreview([]Column{{NAME: "state"}}, evaluator, false, map[string]any{
		"state": "in_progress",
	}, map[string]string{
		"state": "done",
	}, nil)
	if preview.SaveAllowed {
		t.Fatal("expected save preview to be denied")
	}
	if !preview.RecordAllowed {
		t.Fatal("expected record-level permission to remain allowed")
	}
	if len(preview.BlockedFields) != 1 || preview.BlockedFields[0] != "state" {
		t.Fatalf("blocked fields = %#v", preview.BlockedFields)
	}
}

func TestBuildTableSecuritySavePreviewAllowsUnchangedExistingRecord(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		rules: []AppDefinitionSecurityRule{
			{
				Name:      "state_editor",
				Operation: "U",
				Field:     "state",
				Role:      "task_editor",
				Order:     10,
			},
		},
		userRoles: map[string]bool{
			"demo.task_editor": true,
		},
	}

	preview := BuildTableSecuritySavePreview([]Column{{NAME: "state"}}, evaluator, false, map[string]any{
		"state": "draft",
	}, map[string]string{
		"state": "draft",
	}, nil)
	if !preview.SaveAllowed {
		t.Fatal("expected unchanged existing record to remain saveable")
	}
	if len(preview.ChangedFields) != 0 {
		t.Fatalf("changed fields = %#v", preview.ChangedFields)
	}
}

func TestMergeSecuritySavePreviewsCombinesBlockedFields(t *testing.T) {
	merged := MergeSecuritySavePreviews(
		TableSecuritySavePreview{
			Operation:     "U",
			SaveAllowed:   false,
			RecordAllowed: true,
			ChangedFields: []string{"priority", "state"},
			BlockedFields: []string{"state"},
		},
		TableSecuritySavePreview{
			Operation:     "U",
			SaveAllowed:   false,
			RecordAllowed: false,
			ChangedFields: []string{"state", "title"},
			BlockedFields: []string{"title"},
		},
	)

	if merged.SaveAllowed {
		t.Fatal("expected merged preview to deny save")
	}
	if merged.RecordAllowed {
		t.Fatal("expected merged preview to deny record access")
	}
	if got := len(merged.ChangedFields); got != 3 {
		t.Fatalf("len(changed fields) = %d, want 3", got)
	}
	if got := len(merged.BlockedFields); got != 2 {
		t.Fatalf("len(blocked fields) = %d, want 2", got)
	}
	if merged.BlockedFields[0] != "state" || merged.BlockedFields[1] != "title" {
		t.Fatalf("blocked fields = %#v", merged.BlockedFields)
	}
}

func TestFilterReadableRecordMasksUnreadableFields(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		rules: []AppDefinitionSecurityRule{
			{
				Name:      "record_reader",
				Operation: "R",
				Role:      "task_reader",
				Order:     10,
			},
			{
				Name:      "title_reader",
				Operation: "R",
				Field:     "title",
				Role:      "task_reader",
				Order:     20,
			},
			{
				Name:      "state_manager",
				Operation: "R",
				Field:     "state",
				Role:      "task_manager",
				Order:     30,
			},
		},
		userRoles: map[string]bool{
			"demo.task_reader": true,
		},
	}

	record, ok := FilterReadableRecord(evaluator, map[string]any{
		"_id":   "123",
		"title": "Visible",
		"state": "hidden",
	})
	if !ok {
		t.Fatal("expected record to remain readable")
	}
	if got := record["title"]; got != "Visible" {
		t.Fatalf("title = %#v, want %q", got, "Visible")
	}
	if got := record["state"]; got != nil {
		t.Fatalf("state = %#v, want nil", got)
	}
	if got := record["_id"]; got != "123" {
		t.Fatalf("_id = %#v, want %q", got, "123")
	}
}

func TestFilterReadableRecordDeniesUnreadableRecords(t *testing.T) {
	evaluator := &TableSecurityEvaluator{
		app: RegisteredApp{
			Name:      "demo",
			Namespace: "demo",
		},
		tableName: "base_task",
		rules: []AppDefinitionSecurityRule{
			{
				Name:      "manager_only",
				Operation: "R",
				Role:      "task_manager",
				Order:     10,
			},
		},
		userRoles: map[string]bool{
			"demo.task_reader": true,
		},
	}

	if _, ok := FilterReadableRecord(evaluator, map[string]any{"_id": "123"}); ok {
		t.Fatal("expected record to be denied")
	}
}
