package main

import (
	"context"
	"strings"

	"velm/internal/db"
)

// injectUserPasswordFormColumn puts the synthetic password field first on a
// _user form and drops the raw password_hash column.
//
// password_hash is never rendered or edited: the form collects a plaintext
// password into the form-only "password" field, and the save path hashes it
// (internal/db/UserPassword.go). Doing it this way means there is no code path
// that writes a client-supplied value straight into the hash column.
func injectUserPasswordFormColumn(tableName string, columns []db.Column) []db.Column {
	if !strings.EqualFold(strings.TrimSpace(tableName), db.UserTableName) {
		return columns
	}
	out := make([]db.Column, 0, len(columns)+1)
	out = append(out, db.UserPasswordFormColumn())
	for _, column := range columns {
		if strings.EqualFold(column.NAME, db.UserPasswordColumn) {
			continue
		}
		out = append(out, column)
	}
	return out
}

// loadFormPasswordState returns the warning shown above a _user form when the
// record's stored password cannot work, or "" when there is nothing to say.
func loadFormPasswordState(ctx context.Context, tableName, recordID string) string {
	if !strings.EqualFold(strings.TrimSpace(tableName), db.UserTableName) {
		return ""
	}
	id := strings.TrimSpace(recordID)
	if id == "" || id == "new" {
		return ""
	}
	switch db.LoadUserPasswordState(ctx, id) {
	case db.UserPasswordStateMissing:
		return "No password is set for this user, so they cannot sign in. Set one below."
	case db.UserPasswordStateUnusable:
		return "The stored password is not a valid hash, so no password can ever match it. Set a password below to fix sign-in."
	default:
		return ""
	}
}

// dropHiddenFormValues removes stored values for hidden columns so secrets (a
// password hash, an encrypted API key) never travel to the browser inside a
// form. The inputs are still rendered, so layout and scripts that reference
// them keep working.
func dropHiddenFormValues(columns []db.Column, values map[string]string) {
	for _, column := range columns {
		if strings.HasPrefix(column.NAME, "_") {
			continue
		}
		if column.IS_HIDDEN {
			delete(values, column.NAME)
		}
	}
}
