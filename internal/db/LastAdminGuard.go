package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// lastAdminViolationSQLState is raised by _assert_global_admin_remains in
// 082_last_admin_guard.sql. The check lives in the database because admin
// authority has several edges and callers kept missing one; a constraint
// trigger covers every writer, including the generic record editor.
const lastAdminViolationSQLState = "VL001"

// IsLastGlobalAdminViolation reports whether err is the database refusing to
// leave the instance without a global admin. Callers turn this into
// ErrLastGlobalAdminRemoval so the user-facing message is the same whichever
// path produced it.
func IsLastGlobalAdminViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == lastAdminViolationSQLState
	}
	return false
}

// NormalizeLastGlobalAdminError maps the database refusal onto
// ErrLastGlobalAdminRemoval and passes every other error through untouched.
func NormalizeLastGlobalAdminError(err error) error {
	if IsLastGlobalAdminViolation(err) {
		return ErrLastGlobalAdminRemoval
	}
	return err
}
