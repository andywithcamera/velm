package db

import "strings"

// removedSystemTables lists system tables that have been retired: dropped by a
// migration and no longer part of the schema.
//
// The list has to exist because the system app definition is data, not code. It
// is seeded by 042_system_app.sql and every existing instance holds its own
// copy, so it still declares tables that a later migration dropped.
// ensureNamedSystemPhysicalTables recreates any declared system table that is
// missing physically, which silently undid those drops: migration 083 dropped
// _user_role and the post-migration sync put an empty one straight back.
//
// Every name here is filtered out of the definition during sync and skipped by
// the creator, so a retired table stays retired while old definitions converge.
var removedSystemTables = map[string]bool{
	// #199 R4: roles are granted by group membership only.
	"_user_role": true,
}

// isRemovedSystemTable reports whether a table name has been retired.
func isRemovedSystemTable(tableName string) bool {
	return removedSystemTables[normaliseTableName(tableName)]
}

func normaliseTableName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
