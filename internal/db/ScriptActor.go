package db

import (
	"context"
	"fmt"
	"strings"
)

// Row-level security operations, in the same vocabulary the evaluator uses.
const (
	securityOperationCreate = "C"
	securityOperationRead   = "R"
	securityOperationUpdate = "U"
	securityOperationDelete = "D"
)

// ScriptActor is the identity a script runs as. Scripts run in one of two ways:
// on behalf of a signed-in user, or on behalf of the system (scheduled triggers,
// task-engine runs with no actor). Row-level `security` rules apply to the first
// and not to the second, and the actor is what says which is which.
//
// The zero value is neither a user nor the system: it carries no user ID and is
// not marked as system, so it matches no role gate and every read is refused.
// Construct one with UserScriptActor or SystemScriptActor rather than letting it
// fall through, so that "who is this running as" is answered at every call site.
type ScriptActor struct {
	userID string
	system bool
}

// UserScriptActor runs a script as the signed-in user. An empty user ID is not
// upgraded to the system: it stays a user with no identity, which the evaluator
// refuses wherever a role gate or rule exists.
func UserScriptActor(userID string) ScriptActor {
	return ScriptActor{userID: strings.TrimSpace(userID)}
}

// SystemScriptActor runs a script with no user behind it. Row-level rules are
// not applied. Only use it where the work genuinely has no acting user.
func SystemScriptActor() ScriptActor {
	return ScriptActor{system: true}
}

func (a ScriptActor) UserID() string { return a.userID }

func (a ScriptActor) IsSystem() bool { return a.system }

func (a ScriptActor) String() string {
	if a.system {
		return "system"
	}
	if a.userID == "" {
		return "anonymous"
	}
	return a.userID
}

// scriptActorForOptions decides who a script runs as. There is no inference: a
// script is a system script only when the entry point says so.
//
// A caller may set both System and UserID. That reads as "the system acts, on
// behalf of this user": row-level rules do not apply, but UserID is still
// stamped on every write for audit. Only non-HTTP entry points set System,
// because the HTTP handlers always have a signed-in user.
func scriptActorForOptions(opts ScriptExecutionOptions) ScriptActor {
	if opts.System {
		// Keep the user ID when one was supplied: it is not used for access
		// decisions, but it is what an audit trail records.
		return ScriptActor{system: true, userID: strings.TrimSpace(opts.UserID)}
	}
	return UserScriptActor(opts.UserID)
}

// scriptAccess answers "may this actor touch this row" for one script run. It
// caches the per-table evaluator so a loop over records loads the rules once.
type scriptAccess struct {
	ctx    context.Context
	actor  ScriptActor
	tables map[string]*TableSecurityEvaluator
	// loadRules resolves the rules for one table. Nil means the real loader;
	// tests replace it to exercise the access decisions without a database.
	loadRules func(ctx context.Context, tableName, userID string) (*TableSecurityEvaluator, error)
}

func newScriptAccess(ctx context.Context, actor ScriptActor) *scriptAccess {
	return &scriptAccess{ctx: ctx, actor: actor, tables: map[string]*TableSecurityEvaluator{}}
}

func (access *scriptAccess) evaluator(tableName string) (*TableSecurityEvaluator, error) {
	if access == nil || access.actor.IsSystem() {
		return nil, nil
	}
	key := strings.TrimSpace(strings.ToLower(tableName))
	if cached, ok := access.tables[key]; ok {
		return cached, nil
	}
	loadRules := access.loadRules
	if loadRules == nil {
		loadRules = LoadTableSecurityEvaluator
	}
	evaluator, err := loadRules(access.ctx, key, access.actor.UserID())
	if err != nil {
		return nil, err
	}
	access.tables[key] = evaluator
	return evaluator, nil
}

// restricts reports whether any rule or role gate governs this operation on the
// table. When it returns false the query can be trusted as-is.
func (access *scriptAccess) restricts(tableName, operation string) (bool, error) {
	evaluator, err := access.evaluator(tableName)
	if err != nil || evaluator == nil {
		return false, err
	}
	if len(evaluator.roleGate) > 0 {
		return true, nil
	}
	return evaluator.HasOperationRules(operation), nil
}

// allows checks one record. It fails closed: an actor with no identity is
// refused wherever a gate or rule exists.
func (access *scriptAccess) allows(tableName, operation string, record map[string]any) (bool, error) {
	evaluator, err := access.evaluator(tableName)
	if err != nil {
		return false, err
	}
	if evaluator == nil {
		// System actor, or no rules on the table.
		return true, nil
	}
	return evaluator.AllowsRecord(operation, record), nil
}

// filter keeps the records this actor may see. It is a no-op for the system
// actor and for tables with no rules.
func (access *scriptAccess) filter(tableName, operation string, records []map[string]any) ([]map[string]any, error) {
	restricted, err := access.restricts(tableName, operation)
	if err != nil || !restricted {
		return records, err
	}
	evaluator, err := access.evaluator(tableName)
	if err != nil {
		return nil, err
	}
	allowed := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if evaluator.AllowsRecord(operation, record) {
			allowed = append(allowed, record)
		}
	}
	return allowed, nil
}

// denyRecordError is what a script sees when a row exists but the actor may not
// touch it. It reads as "not found" on purpose: record IDs are allocated in
// sequence, so a distinct "forbidden" message would confirm which IDs exist.
func denyRecordError(tableName, id string) error {
	return fmt.Errorf("record %q not found in %q", id, tableName)
}
