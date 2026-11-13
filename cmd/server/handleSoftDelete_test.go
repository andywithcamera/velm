package main

import (
	"strings"
	"testing"
)

// columns mimics a table's column set for softDeleteSetClauses.
func columnsOf(names ...string) func(string) bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return func(c string) bool { return set[c] }
}

// Regression for #301: base_task_definition has _deleted_at but never got
// _deleted_by, so the previous fixed UPDATE named a column that does not exist
// and every definition delete returned 500. The clause must never mention a
// column the table does not have.
func TestSoftDeleteClausesOmitMissingDeletedBy(t *testing.T) {
	clauses, args := softDeleteSetClauses(columnsOf("_deleted_at", "_updated_at"), false, "user-1")

	joined := strings.Join(clauses, ", ")
	if strings.Contains(joined, "_deleted_by") {
		t.Fatalf("clause names _deleted_by on a table without it: %q", joined)
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want none when _deleted_by is absent", args)
	}
	if !strings.Contains(joined, "_deleted_at = NOW()") {
		t.Fatalf("clause = %q, want the soft delete marker", joined)
	}
}

// The common case must still record who deleted the row.
func TestSoftDeleteClausesRecordDeletedBy(t *testing.T) {
	clauses, args := softDeleteSetClauses(
		columnsOf("_deleted_at", "_deleted_by", "_updated_at"), false, "user-1")

	joined := strings.Join(clauses, ", ")
	if !strings.Contains(joined, "_deleted_by = NULLIF($1, '')::uuid") {
		t.Fatalf("clause = %q, want _deleted_by bound to $1", joined)
	}
	if len(args) != 1 || args[0] != "user-1" {
		t.Fatalf("args = %v, want [user-1]", args)
	}
}

// Restore clears rather than stamps, and must not bind a user id.
func TestRestoreClausesClearDeletedBy(t *testing.T) {
	clauses, args := softDeleteSetClauses(
		columnsOf("_deleted_at", "_deleted_by", "_updated_at"), true, "")

	joined := strings.Join(clauses, ", ")
	if !strings.Contains(joined, "_deleted_at = NULL") {
		t.Fatalf("clause = %q, want _deleted_at cleared", joined)
	}
	if !strings.Contains(joined, "_deleted_by = NULL") {
		t.Fatalf("clause = %q, want _deleted_by cleared", joined)
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want none on restore", args)
	}

	restoreNoBy, _ := softDeleteSetClauses(columnsOf("_deleted_at"), true, "")
	if strings.Contains(strings.Join(restoreNoBy, ", "), "_deleted_by") {
		t.Fatalf("restore names _deleted_by on a table without it: %v", restoreNoBy)
	}
}
