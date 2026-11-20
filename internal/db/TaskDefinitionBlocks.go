package db

// Control blocks at runtime (Process Designer, Plan T4 / Requirements §2).
//
// A definition is a tree of blocks (098). The engine used to spawn one
// base_task per child definition; it now walks the block tree, so `if`,
// `for_each` and `end` are real rows on the board rather than a gap in the
// run. Design Vision §5 is the rule this follows: every control block
// materialises as a base_task with work_type='CONTROL', and the work in its
// scope are its children.
//
// The run tree carries its block identity in the engine-owned `variables`
// document rather than in new base_task columns:
//
//	variables.block = {id, kind, type, label, position, container_definition_id}
//
// That keeps the spawn path working against a pre-098 schema (a definition
// authored before the tree existed has no block columns to read, and the
// pre-migration parity test spawns one such definition in place), and it
// keeps every fact about the block in one place: the run tree is a frozen
// copy of the definition tree.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cel.dev/cel-go/cel"
)

// forEachCap is the v1 row-explosion limit from Design Vision §5: a
// for_each over one more item than this is refused, not truncated.
const forEachCap = 100

// DefinitionBlock is one row of base_task_definition_child, in tree form.
type DefinitionBlock struct {
	ID                string
	ParentID          string
	ChildDefinitionID string
	Kind              string
	Type              string
	Label             string
	Position          int
	SortOrder         int
	InputMapping      map[string]any
	DependsOnBlockIDs []string
	DependsOn         []string
	DependsOnReason   string
	Config            map[string]any
	// Branch is "then"/"else" under an if; empty elsewhere.
	Branch string
	// Per-block assignment (098, AC-47). Empty means the block did not set
	// the column and inherits the definition's value.
	AssignmentGroupID  string
	AssignmentStrategy string
	AssignedUserID     string
}

// IsControl reports whether this block shapes the flow.
func (b DefinitionBlock) IsControl() bool { return b.Kind == "control" }

// controlKindLabel is the word the run card shows for a control block:
// "If", "For each", "End".
func (b DefinitionBlock) controlKindLabel() string {
	switch b.Type {
	case "run_script":
		return "Run script"
	case "retry":
		return "Retry"
	case "run_task":
		return "Run task"
	case "if":
		return "If"
	case "for_each":
		return "For each"
	case "end":
		return "End"
	case "wait":
		return "Wait"
	case "parallel":
		return "Parallel"
	default:
		return "Control"
	}
}

// blockConfigString reads a string setting from config.
func (b DefinitionBlock) blockConfigString(key string) string {
	v, ok := b.Config[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// blockTreeAvailable reports whether 098's block columns exist. A database
// mid-migration (or the pre-migration spawn test's scratch 097 database)
// answers false, and the spawn path then reads the legacy flat rows. This is
// one cheap catalogue query per container spawn, not per task.
func blockTreeAvailable(ctx context.Context, q scriptQuerier) bool {
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'base_task_definition_child' AND column_name = 'block_kind'
		)`).Scan(&exists)
	return err == nil && exists
}

// getDefinitionBlocks reads a definition's blocks in document order. On a
// pre-098 schema every row is a top-level task block in sort_order.
func getDefinitionBlocks(ctx context.Context, q scriptQuerier, containerID string) ([]DefinitionBlock, error) {
	tree := blockTreeAvailable(ctx, q)
	query := `
		SELECT _id::text, child_definition_id::text, input_mapping, depends_on,
			depends_on_reason, sort_order
		FROM base_task_definition_child
		WHERE container_definition_id = $1
		ORDER BY sort_order, _created_at`
	if tree {
		query = `
			SELECT _id::text, COALESCE(parent_block_id::text, ''), COALESCE(child_definition_id::text, ''),
				COALESCE(block_kind, 'task'), COALESCE(block_type, ''), config,
				position, sort_order, input_mapping, depends_on,
				COALESCE(depends_on_block_id, '{}'), depends_on_reason,
				COALESCE(assignment_group_id::text, ''), COALESCE(assignment_strategy, ''),
				COALESCE(assigned_user_id::text, ''), COALESCE(branch, '')
			FROM base_task_definition_child
			WHERE container_definition_id = $1
			ORDER BY position, sort_order, _created_at`
	}
	rows, err := q.Query(ctx, query, containerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var blocks []DefinitionBlock
	for rows.Next() {
		var b DefinitionBlock
		var deps []string
		var cfg []byte
		if tree {
			var blockDeps []string
			if err := rows.Scan(&b.ID, &b.ParentID, &b.ChildDefinitionID, &b.Kind, &b.Type,
				&cfg, &b.Position, &b.SortOrder, &b.InputMapping, &deps, &blockDeps,
				&b.DependsOnReason, &b.AssignmentGroupID, &b.AssignmentStrategy,
				&b.AssignedUserID, &b.Branch); err != nil {
				return nil, err
			}
			b.DependsOnBlockIDs = blockDeps
			b.Config = map[string]any{}
			if len(cfg) > 0 {
				_ = json.Unmarshal(cfg, &b.Config)
			}
		} else {
			if err := rows.Scan(&b.ID, &b.ChildDefinitionID, &b.InputMapping, &deps,
				&b.DependsOnReason, &b.SortOrder); err != nil {
				return nil, err
			}
			b.Kind = "task"
			b.Position = b.SortOrder
			b.Config = map[string]any{}
		}
		b.DependsOn = deps
		if b.Label == "" {
			b.Label = b.blockConfigString("label")
		}
		if b.Label == "" && b.ChildDefinitionID != "" {
			// A task block is labelled by the definition it calls; the
			// caller that has the definition loaded fills this in where it
			// needs a human label. The block's own config wins.
			b.Label = ""
		}
		blocks = append(blocks, b)
	}
	return blocks, rows.Err()
}

// topLevelBlocks returns the blocks at one scope, in document order.
func topLevelBlocks(blocks []DefinitionBlock, parentID string) []DefinitionBlock {
	out := make([]DefinitionBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.ParentID == parentID {
			out = append(out, b)
		}
	}
	return out
}

// branchOf is the stored discriminator with 098/102's NULL semantics: a
// child of an `if` that predates the branch column is THEN, and a row that is
// not under an `if` has no branch at all.
func branchOf(b DefinitionBlock) string {
	if b.Branch == "else" {
		return "else"
	}
	return "then"
}

// scopeBlocks returns the children of one scope, optionally restricted to one
// branch of an `if`. branch "" means every child (a for_each scope has no
// branches).
func scopeBlocks(blocks []DefinitionBlock, parentID, branch string) []DefinitionBlock {
	children := topLevelBlocks(blocks, parentID)
	if branch == "" {
		return children
	}
	out := make([]DefinitionBlock, 0, len(children))
	for _, b := range children {
		if branchOf(b) == branch {
			out = append(out, b)
		}
	}
	return out
}

// parentScopeBlocks returns the blocks that describe the children of the
// task whose variables are given: a container's own root scope, or the scope
// of the control block the task was spawned from.
//
// Only a control task is scoped by its block id. A work task that happens to
// carry block identity (any task under an if or for_each does) is a container
// in its own right: its block id names its row in the *parent's* definition,
// not a block of its own, so its children are its definition's root scope.
func parentScopeBlocks(blocks []DefinitionBlock, parentVars map[string]any) []DefinitionBlock {
	if id, _, kind, _, _, _, ok := blockFromVars(parentVars); ok && kind == "control" {
		return topLevelBlocks(blocks, id)
	}
	return topLevelBlocks(blocks, "")
}

// scopeNeedsBlockIdentity reports whether a run row must carry its block id in
// variables.block. It is needed when a definition id no longer identifies one
// row: a control block, or the same child definition used twice in one scope.
//
// A definition authored before 098 is flat, all-task and one-row-per-child, so
// it needs no stamp and spawns with exactly the variables it had on 097 — the
// pre-098 parity requirement (Requirements AC-17/18). Stamping every row would
// add an engine key to every historical run and break that parity.
func scopeNeedsBlockIdentity(blocks []DefinitionBlock, parentID string) bool {
	if parentID != "" {
		// A control block's scope: its children are created per iteration, so
		// a definition id may name several siblings and must not be the key.
		return true
	}
	seen := map[string]bool{}
	for _, b := range topLevelBlocks(blocks, parentID) {
		if b.Kind != "task" || b.ChildDefinitionID == "" || seen[b.ChildDefinitionID] {
			return true
		}
		seen[b.ChildDefinitionID] = true
	}
	return false
}

// blockMeta is the engine-owned identity stamped into a run task's
// variables. containerDefinitionID lets promotion find the block tree for a
// control task, which has no definition_id of its own.
func blockMeta(containerDefID string, b DefinitionBlock) map[string]any {
	return map[string]any{
		"id":                      b.ID,
		"key":                     b.blockConfigString("key"),
		"kind":                    b.Kind,
		"type":                    b.Type,
		"label":                   b.Label,
		"position":                float64(b.Position),
		"container_definition_id": containerDefID,
	}
}

// blockFromVars recovers the block identity of a spawned task. ok is false
// for a task the engine did not create from a block (a spawn-rule follow-on,
// for example), which keeps the legacy promotion path alive.
func blockFromVars(vars map[string]any) (id, containerDefID, kind, typ, label string, position int, ok bool) {
	raw, found := vars["block"].(map[string]any)
	if !found {
		return "", "", "", "", "", 0, false
	}
	id, _ = raw["id"].(string)
	containerDefID, _ = raw["container_definition_id"].(string)
	kind, _ = raw["kind"].(string)
	typ, _ = raw["type"].(string)
	label, _ = raw["label"].(string)
	if p, isNum := raw["position"].(float64); isNum {
		position = int(p)
	}
	return id, containerDefID, kind, typ, label, position, id != ""
}

// mergeScope copies item/index/parent into a task's variable document.
func mergeScope(vars map[string]any, scope map[string]any) {
	for _, key := range []string{"item", "index", "parent"} {
		if v, ok := scope[key]; ok {
			vars[key] = v
		}
	}
}

// scopeDefinitionID resolves the definition whose blocks describe a task's
// children. A work task answers with its own definition_id; a control task
// answers with the definition it was authored in.
func scopeDefinitionID(ctx context.Context, q scriptQuerier, taskID string) (string, map[string]any, error) {
	defID, err := getDefinitionIDByTaskID(ctx, q, taskID)
	if err != nil {
		return "", nil, err
	}
	vars, err := readTaskVariables(ctx, q, taskID)
	if err != nil {
		return "", nil, err
	}
	if defID == "" {
		if _, containerDefID, _, _, _, _, ok := blockFromVars(vars); ok {
			defID = containerDefID
		}
	}
	return defID, vars, nil
}

// spawnDefinitionScope creates the run rows for one definition's top-level
// blocks, under containerTaskID. It is called when a container spawns (all
// rows pending) and when a control block opens (its scoped rows), so there
// is one code path for both.
//
// skip marks a scope whose `if` was false: every task in it is created
// closed with closure_reason 'skipped', so the run tree still matches the
// definition tree (AC-27) and nothing publishes outputs (AC-28).
func spawnDefinitionScope(ctx context.Context, q scriptQuerier, defID, containerTaskID, requestedBy string, runInputs, scope map[string]any, skip bool) error {
	blocks, err := getDefinitionBlocks(ctx, q, defID)
	if err != nil {
		return err
	}
	stamp := scopeNeedsBlockIdentity(blocks, "")
	for _, b := range topLevelBlocks(blocks, "") {
		if err := spawnBlockInstance(ctx, q, defID, containerTaskID, requestedBy, b, runInputs, scope, skip, stamp); err != nil {
			return err
		}
	}
	return nil
}

// blockDisplayLabel names a block in a refusal or a log line: its own label,
// else the definition it calls, else its kind.
func blockDisplayLabel(b DefinitionBlock) string {
	if b.Label != "" {
		return b.Label
	}
	if b.ChildDefinitionID != "" {
		return b.ChildDefinitionID
	}
	return b.Kind + " block"
}

// blockAssignmentRoute copies a block's assignment override onto a task route.
// Nil pointers mean "not set on the block, inherit the definition's value".
func blockAssignmentRoute(route *taskRoute, b DefinitionBlock) {
	if b.AssignmentGroupID != "" {
		group := b.AssignmentGroupID
		route.AssignGroupOverride = &group
	}
	if b.AssignmentStrategy != "" {
		route.AssignStrategy = b.AssignmentStrategy
	}
	if b.AssignedUserID != "" {
		user := b.AssignedUserID
		route.AssignUserOverride = &user
	}
	route.AssignInputName = b.blockConfigString("user_input")
	route.AssignLabel = blockDisplayLabel(b)
}

// spawnBlockInstance creates one run row for one block.
func spawnBlockInstance(ctx context.Context, q scriptQuerier, containerDefID, parentTaskID, requestedBy string, b DefinitionBlock, runInputs, scope map[string]any, skip, stamp bool) error {
	switch b.Kind {
	case "trigger":
		// The trigger is what started the run; the definition itself is the
		// root task. It is a block in the document but never a child row, so
		// the if and end below it are the root's first children (AC-22).
		return nil
	case "control":
		return spawnControlTask(ctx, q, containerDefID, parentTaskID, requestedBy, b, runInputs, scope, skip)
	case "task":
		var meta map[string]any
		if stamp {
			meta = blockMeta(containerDefID, b)
		}
		route := &taskRoute{BlockMeta: meta, Scope: scope, Skip: skip, AssignInputs: runInputs}
		blockAssignmentRoute(route, b)
		if skip {
			route.State = taskStateDone
			route.ClosureReason = taskClosureSkipped
		}
		_, err := spawnTaskWithRouteByID(ctx, q, b.ChildDefinitionID, parentTaskID, requestedBy, map[string]any{}, route)
		return err
	default:
		// action / gate / stage are not runnable in this release; the save
		// gate refuses them, so reaching here means a row was written by
		// something else. Refuse loudly rather than silently skip work.
		return fmt.Errorf("block %q: %s blocks are not available in this release", b.Label, b.Kind)
	}
}

// spawnControlTask inserts the CONTROL row for an if / for_each / end block.
//
// A control block does no work for a person, so it inherits the scope's
// assignment rather than resolving a strategy of its own: it carries the
// parent task's group and assignee, and only a block that sets assignment
// columns resolves them (AC-66). A control task with no assignee is legal:
// 084 only requires one at in_progress.
func spawnControlTask(ctx context.Context, q scriptQuerier, containerDefID, parentTaskID, requestedBy string, b DefinitionBlock, runInputs, scope map[string]any, skip bool) error {
	state := taskStatePending
	closureReason := ""
	if skip {
		state = taskStateDone
		closureReason = taskClosureSkipped
	}
	groupID, assigneeID := "", ""
	if strings.TrimSpace(parentTaskID) != "" {
		if err := q.QueryRow(ctx, `
			SELECT COALESCE(assignment_group_id::text, ''), COALESCE(assigned_user_id::text, '')
			FROM base_task WHERE _id = $1`, parentTaskID).Scan(&groupID, &assigneeID); err != nil {
			return fmt.Errorf("control block %q: read parent assignment: %w", blockDisplayLabel(b), err)
		}
	}
	if b.AssignmentGroupID != "" || b.AssignmentStrategy != "" || b.AssignedUserID != "" {
		spec := assignmentSpec{
			GroupID:     groupID,
			Strategy:    b.AssignmentStrategy,
			NamedUserID: assigneeID,
			InputName:   b.blockConfigString("user_input"),
			Label:       blockDisplayLabel(b),
		}
		if b.AssignmentGroupID != "" {
			spec.GroupID = b.AssignmentGroupID
		}
		if b.AssignedUserID != "" {
			spec.NamedUserID = b.AssignedUserID
		}
		var err error
		groupID, assigneeID, err = resolveAssignment(ctx, q, spec, requestedBy, runInputs)
		if err != nil {
			return err
		}
	}
	if skip {
		assigneeID = ""
	}
	title := controlTaskTitle(ctx, q, parentTaskID, b)
	vars := map[string]any{
		"inputs":  map[string]any{},
		"locals":  map[string]any{},
		"outputs": map[string]any{},
		"block":   blockMeta(containerDefID, b),
	}
	mergeScope(vars, scope)
	varsJSON, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	var taskID, number string
	if err := q.QueryRow(ctx, `
		INSERT INTO base_task (number, title, work_type, state, parent_task_id,
			assignment_group_id, assigned_user_id, requested_by_user_id, variables, closure_reason)
		VALUES ('', $1, 'CONTROL', $2, $3, $4, $5, $6, $7::jsonb, $8)
		RETURNING _id, number`,
		title, state, parentTaskID, nullableText(groupID), nullableText(assigneeID),
		nullableText(requestedBy), string(varsJSON), nullableText(closureReason)).Scan(&taskID, &number); err != nil {
		return fmt.Errorf("spawn control block %q: %w", b.Label, err)
	}
	if _, err := q.Exec(ctx, `UPDATE base_task SET title = $2 WHERE _id = $1`,
		taskID, composeInstanceTitle(title, number)); err != nil {
		return err
	}
	if skip {
		// A skipped scope's own control blocks still exist in the run tree,
		// and their scopes are skipped too.
		return spawnDefinitionScopeForBlock(ctx, q, containerDefID, taskID, requestedBy, b, runInputs, scope, true)
	}
	return nil
}

// spawnDefinitionScopeForBlock creates the children of one control block
// under controlTaskID.
func spawnDefinitionScopeForBlock(ctx context.Context, q scriptQuerier, containerDefID, controlTaskID, requestedBy string, b DefinitionBlock, runInputs, scope map[string]any, skip bool) error {
	return spawnDefinitionBranchScope(ctx, q, containerDefID, controlTaskID, requestedBy, b, runInputs, scope, skip, "")
}

// spawnDefinitionBranchScope creates the children of one branch of a control
// block. branch "" means the whole scope (for_each, end, run_script).
func spawnDefinitionBranchScope(ctx context.Context, q scriptQuerier, containerDefID, controlTaskID, requestedBy string, b DefinitionBlock, runInputs, scope map[string]any, skip bool, branch string) error {
	blocks, err := getDefinitionBlocks(ctx, q, containerDefID)
	if err != nil {
		return err
	}
	stamp := scopeNeedsBlockIdentity(blocks, b.ID)
	for _, child := range scopeBlocks(blocks, b.ID, branch) {
		if err := spawnBlockInstance(ctx, q, containerDefID, controlTaskID, requestedBy, child, runInputs, scope, skip, stamp); err != nil {
			return err
		}
	}
	return nil
}

// controlTaskTitle is AC-23: the card names its kind and the block's label,
// e.g. "Upgrade OS · If exit_code == 0".
func controlTaskTitle(ctx context.Context, q scriptQuerier, parentTaskID string, b DefinitionBlock) string {
	root, err := rootInstanceName(ctx, q, parentTaskID)
	if err != nil || root == "" {
		root = "Run"
	}
	descriptor := b.controlKindLabel()
	if b.Label != "" {
		descriptor += " " + b.Label
	}
	return root + " · " + descriptor
}

func nullableText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// EvaluateIfCondition evaluates an `if` block's condition. The value is a
// CEL expression carried behind the `expr:` prefix (Design Vision §6); a
// condition without the prefix is not a boolean and is refused by the save
// gate, so reaching here with one is an engine error, named.
//
// AC-25: `expr: inputs.server.role == "primary"` is true for
// {"role":"primary"} and false for {"role":"standby"}.
func EvaluateIfCondition(condition string, inputs map[string]any) (bool, error) {
	trimmed := strings.TrimSpace(condition)
	if !strings.HasPrefix(trimmed, "expr:") {
		return false, fmt.Errorf("condition %q is not an expression: prefix it with %q", condition, "expr:")
	}
	expression := strings.TrimSpace(strings.TrimPrefix(trimmed, "expr:"))
	if expression == "" {
		return false, fmt.Errorf("condition has no expression after %q", "expr:")
	}
	env, err := cel.NewEnv(
		cel.Variable("inputs", cel.DynType),
		cel.Variable("item", cel.DynType),
		cel.Variable("index", cel.DynType),
		cel.Variable("outputs", cel.DynType),
		cel.Variable("locals", cel.DynType),
	)
	if err != nil {
		return false, err
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("condition %q: %w", expression, issues.Err())
	}
	program, err := env.Program(ast)
	if err != nil {
		return false, fmt.Errorf("condition %q: %w", expression, err)
	}
	if inputs == nil {
		inputs = map[string]any{}
	}
	out, _, err := program.Eval(map[string]any{"inputs": inputs})
	if err != nil {
		return false, fmt.Errorf("condition %q: %w", expression, err)
	}
	value, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("condition %q is not a boolean", expression)
	}
	return value, nil
}

// evaluateControlCondition evaluates a block's `condition` config against a
// task's variable scope: the condition sees the block's own inputs, the
// collected item and its parent scope.
func evaluateControlCondition(b DefinitionBlock, vars map[string]any) (bool, error) {
	condition := b.blockConfigString("condition")
	if condition == "" {
		return false, fmt.Errorf("block %q has no condition", b.Label)
	}
	return evaluateIfWithScope(condition, vars)
}

// resolveForEachCollection resolves config.collection against a task's
// variable scope and enforces the array shape and the v1 cap. The error text
// names the block and the field (AC-33, AC-36) and is never truncated.
func resolveForEachCollection(b DefinitionBlock, vars map[string]any) ([]any, error) {
	raw, ok := b.Config["collection"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("block %q: collection is required", b.Label)
	}
	value := raw
	if text, isString := raw.(string); isString {
		resolved, resolvedOK := resolveScopeRef(vars, text)
		if !resolvedOK {
			return nil, fmt.Errorf("block %q: collection %q cannot be resolved at spawn", b.Label, text)
		}
		value = resolved
	}
	items, isArray := value.([]any)
	if !isArray {
		return nil, fmt.Errorf("block %q: collection must be an array, got %T", b.Label, value)
	}
	if len(items) > forEachCap {
		return nil, fmt.Errorf("block %q: for_each collection has %d items; the maximum is %d per block", b.Label, len(items), forEachCap)
	}
	return items, nil
}

// prevalidateControlBlocks refuses an unrunnable block before any row is
// written (AC-34): every for_each whose collection can already be resolved
// against the spawn inputs must be an array of at most for_eachCap items.
// Collections that depend on upstream outputs are checked again when the
// block is reached.
func prevalidateControlBlocks(ctx context.Context, q scriptQuerier, defID string, inputs map[string]any) error {
	blocks, err := getDefinitionBlocks(ctx, q, defID)
	if err != nil {
		return err
	}
	scope := map[string]any{"inputs": nonNilInputs(inputs)}
	for _, b := range blocks {
		if !b.IsControl() || b.Type != "for_each" {
			continue
		}
		raw, ok := b.Config["collection"]
		if !ok || raw == nil {
			return fmt.Errorf("block %q: collection is required", b.Label)
		}
		text, isString := raw.(string)
		if isString && strings.HasPrefix(text, "$") {
			if resolved, resolvedOK := resolveScopeRef(scope, text); resolvedOK {
				if _, isArray := resolved.([]any); !isArray {
					return fmt.Errorf("block %q: collection must be an array, got %T", b.Label, resolved)
				}
				if items := resolved.([]any); len(items) > forEachCap {
					return fmt.Errorf("block %q: for_each collection has %d items; the maximum is %d per block", b.Label, len(items), forEachCap)
				}
			}
			continue
		}
		if _, err := resolveForEachCollection(b, scope); err != nil {
			return err
		}
	}
	return nil
}

// promoteControlInstance opens a control block that has just become ready:
// evaluate once, then create and promote its scope.
func promoteControlInstance(ctx context.Context, q scriptQuerier, parentTaskID string, inst childInstanceState, b DefinitionBlock, containerDefID, actorUserID string, depth int) error {
	parentVars, err := readTaskVariables(ctx, q, parentTaskID)
	if err != nil {
		return err
	}
	// A control block shapes the flow rather than doing work, so it has no
	// input mapping of its own: its scope is the enclosing task's scope, and
	// its own parent pointer is the enclosing task. Children therefore read
	// $inputs.* from here and $parent.* one level above it (AC-31), instead
	// of a control block silently swallowing the scope.
	scope := inst.vars
	if scope == nil {
		scope = map[string]any{}
	}
	if own, _ := scope["inputs"].(map[string]any); len(own) == 0 {
		if parentInputs, ok := parentVars["inputs"].(map[string]any); ok {
			scope["inputs"] = parentInputs
		}
	}
	if own, _ := scope["outputs"].(map[string]any); len(own) == 0 {
		if parentOutputs, ok := parentVars["outputs"].(map[string]any); ok {
			scope["outputs"] = parentOutputs
		}
	}
	if _, ok := scope["locals"].(map[string]any); !ok {
		scope["locals"] = map[string]any{}
	}
	// Sibling results ($blocks.<key>.output.*) are written onto the
	// enclosing task, so the condition of an `if` reads them from there.
	if blocks, ok := parentVars["blocks"].(map[string]any); ok {
		scope["blocks"] = blocks
	}
	scope["parent"] = parentVars
	// A block's from_input reads the run's request; the scope carries it.
	runInputs, _ := scope["inputs"].(map[string]any)
	if err := transitionTaskInTx(ctx, q, inst.taskID, taskStateReady); err != nil {
		return err
	}
	// Persist the inherited scope: a child's own promotion reads these
	// variables, and the run row should say what the block ran against.
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`,
		inst.taskID, string(scopeJSON)); err != nil {
		return err
	}
	switch b.Type {
	case "run_script":
		return runScriptBlock(ctx, q, inst, b, actorUserID)
	case "run_task":
		// Slice 6: spawn the target with the mapped inputs and wait for it.
		return runRunTaskBlock(ctx, q, inst, b, containerDefID, actorUserID, scope, runInputs)
	case "if":
		condition, evalErr := evaluateControlCondition(b, scope)
		if evalErr != nil {
			return fmt.Errorf("block %q: %w", b.Label, evalErr)
		}
		// AC-68: the taken branch runs, the other is created skipped so the
		// run tree keeps both halves of the document.
		taken, other := "then", "else"
		if !condition {
			taken, other = "else", "then"
		}
		if err := spawnDefinitionBranchScope(ctx, q, containerDefID, inst.taskID, actorUserID, b, runInputs, scope, false, taken); err != nil {
			return err
		}
		if err := spawnDefinitionBranchScope(ctx, q, containerDefID, inst.taskID, actorUserID, b, runInputs, scope, true, other); err != nil {
			return err
		}
		// AC-27: an `if` whose taken branch is empty is skipped rather than
		// left ready forever; with branch content it runs like any scope.
		var takenChildren int
		if err := q.QueryRow(ctx, `
			SELECT COUNT(*) FROM base_task_definition_child
			WHERE parent_block_id = $1 AND COALESCE(branch, 'then') = $2`,
			b.ID, taken).Scan(&takenChildren); err != nil {
			return fmt.Errorf("block %q: read taken branch: %w", b.Label, err)
		}
		if takenChildren == 0 {
			return skipTaskInTx(ctx, q, inst.taskID, actorUserID)
		}
		return promoteChildrenDepth(ctx, q, inst.taskID, actorUserID, depth+1)
	case "retry":
		// One attempt opens now; the attempt's own children are created
		// afresh from the first block (D17, AC-84/85).
		return startRetryAttempt(ctx, q, inst.taskID, b, containerDefID, actorUserID, runInputs, scope, depth)
	case "for_each":
		items, collErr := resolveForEachCollection(b, scope)
		if collErr != nil {
			return collErr
		}
		for index, item := range items {
			elementScope := map[string]any{"item": item, "index": float64(index), "parent": parentVars}
			if err := spawnDefinitionScopeForBlock(ctx, q, containerDefID, inst.taskID, actorUserID, b, runInputs, elementScope, false); err != nil {
				return err
			}
		}
		return promoteChildrenDepth(ctx, q, inst.taskID, actorUserID, depth+1)
	default:
		return fmt.Errorf("block %q: %s blocks are not evaluable in this release", b.Label, b.Type)
	}
}

// runScriptBlock executes a `run_script` control block (Design Vision D4/D5,
// Requirements AC-60 to AC-71).
//
// The body is a goja script through the existing ExecuteJavaScript path, not
// a shell. It is admin-only at run time as well as at save time: a row may
// have been written straight into the table, so the runtime cannot trust the
// save gate.
func runScriptBlock(ctx context.Context, q scriptQuerier, inst childInstanceState, b DefinitionBlock, actorUserID string) error {
	if actorUserID != "" {
		admin, err := UserHasPermission(ctx, actorUserID, "admin", "")
		if err != nil {
			return fmt.Errorf("block %q: read the actor role: %w", b.Label, err)
		}
		if !admin {
			return closeScriptTask(ctx, q, inst.taskID, actorUserID, false,
				fmt.Errorf("run script blocks require the admin role"))
		}
	}

	body := b.blockConfigString("script")
	if body == "" {
		return closeScriptTask(ctx, q, inst.taskID, actorUserID, false,
			fmt.Errorf("block %q: the script body is empty", b.Label))
	}
	timeout := time.Duration(0)
	if ms := b.Config["timeout_ms"]; ms != nil {
		if n, ok := numberValue(ms); ok && n > 0 {
			timeout = time.Duration(int64(n)) * time.Millisecond
		}
	}
	outputVar := b.blockConfigString("output_var")
	if outputVar == "" {
		outputVar = "result"
	}
	vars, err := readTaskVariables(ctx, q, inst.taskID)
	if err != nil {
		return err
	}
	inputs, _ := vars["inputs"].(map[string]any)
	if inputs == nil {
		inputs = map[string]any{}
	}
	taskGlobals := map[string]any{"number": "", "description": ""}
	// The spawning task record: its number and description are what the
	// author sees on the board.
	if parentID := parentTaskOf(ctx, q, inst.taskID); parentID != "" {
		var number, description string
		_ = q.QueryRow(ctx, `SELECT number, COALESCE(description, '') FROM base_task WHERE _id = $1`,
			parentID).Scan(&number, &description)
		taskGlobals = map[string]any{"number": number, "description": description}
	}
	assigned := map[string]any{}
	result, runErr := executeJavaScriptWithQuerier(ctx, q, ScriptExecutionOptions{
		Code:        body,
		TableName:   "base_task",
		TriggerType: "task_engine",
		EventName:   "run_script",
		UserID:      actorUserID,
		System:      strings.TrimSpace(actorUserID) == "",
		Timeout:     timeout,
		Input:       inputs,
		Globals: map[string]any{
			"inputs":  inputs,
			"outputs": assigned,
			"task":    taskGlobals,
		},
		Capture: []string{outputVar, "outputs"},
	})
	if captured, ok := result.Globals["outputs"].(map[string]any); ok {
		assigned = captured
	}
	shape := scriptResultShape(result, runErr, outputVar)
	if runErr != nil {
		shape["stderr"] = runErr.Error()
		shape["success"] = false
	}
	// AC-65: all four keys, always.
	outputs := map[string]any{
		"success": shape["success"],
		"stdout":  shape["stdout"],
		"stderr":  shape["stderr"],
		"json":    shape["json"],
	}
	if len(assigned) > 0 {
		outputs["outputs"] = assigned
	}
	// AC-64: the assigned outputs live at variables.block.outputs.
	block := map[string]any{}
	if raw, ok := vars["block"].(map[string]any); ok {
		for k, v := range raw {
			block[k] = v
		}
	}
	block["outputs"] = assigned
	block["result"] = outputs
	if runErr != nil {
		block["log"] = []any{runErr.Error()}
	}
	vars["block"] = block
	varsJSON, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`,
		inst.taskID, string(varsJSON)); err != nil {
		return err
	}
	// A failed script is still readable by a following `if`: publish the
	// result object with success:false before closing.
	if key := b.blockConfigString("key"); key != "" {
		if err := publishBlockOutput(ctx, q, inst.taskID, key, outputs); err != nil {
			return err
		}
	}
	if runErr != nil {
		return closeScriptTask(ctx, q, inst.taskID, actorUserID, false, runErr)
	}
	return closeScriptTask(ctx, q, inst.taskID, actorUserID, true, nil)
}

func numberValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func parentTaskOf(ctx context.Context, q scriptQuerier, taskID string) string {
	var parent string
	_ = q.QueryRow(ctx, `SELECT COALESCE(parent_task_id::text, '') FROM base_task WHERE _id = $1`,
		taskID).Scan(&parent)
	return parent
}

// scriptResultShape assembles AC-65's four keys. The body's `result` global
// is the source of `json`; a string is parsed, anything else is null.
func scriptResultShape(result ScriptExecutionResult, runErr error, outputVar string) map[string]any {
	shape := map[string]any{"success": runErr == nil, "stdout": result.Output, "stderr": "", "json": nil}
	raw, ok := result.Globals[outputVar]
	if !ok {
		// The body may simply return its object instead of assigning the
		// result variable; that object is the JSON result too.
		if result.Result == nil {
			return shape
		}
		raw = result.Result
	}
	switch value := raw.(type) {
	case map[string]any:
		shape["json"] = value
	case string:
		var parsed map[string]any
		if json.Unmarshal([]byte(value), &parsed) == nil {
			shape["json"] = parsed
		}
	}
	return shape
}

// publishBlockOutput makes a block's result readable as
// $blocks.<key>.output.<field> on the enclosing task.
func publishBlockOutput(ctx context.Context, q scriptQuerier, taskID, key string, output map[string]any) error {
	parentID := parentTaskOf(ctx, q, taskID)
	if parentID == "" {
		return nil
	}
	vars, err := readTaskVariables(ctx, q, parentID)
	if err != nil {
		return err
	}
	blocks, _ := vars["blocks"].(map[string]any)
	if blocks == nil {
		blocks = map[string]any{}
	}
	blocks[key] = map[string]any{"output": output}
	vars["blocks"] = blocks
	raw, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, parentID, string(raw))
	return err
}

// closeScriptTask closes the script's control row. A refusal or a thrown body
// is a failed closure (084's reason), so the board says what happened.
func closeScriptTask(ctx context.Context, q scriptQuerier, taskID, actorUserID string, ok bool, cause error) error {
	reason := "completed"
	if !ok {
		reason = "failed"
	}
	if err := transitionTaskInTx(ctx, q, taskID, taskStateDone); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE base_task SET closure_reason = $2 WHERE _id = $1`, taskID, reason); err != nil {
		return err
	}
	if ok {
		if err := onTaskClosed(ctx, q, taskID, reason, actorUserID); err != nil {
			return err
		}
		return rollUpAncestors(ctx, q, taskID, actorUserID)
	}
	// A failed block records why. The run stays where it is: the failure is
	// visible on the block rather than silently rolled up as complete.
	if cause != nil {
		vars, err := readTaskVariables(ctx, q, taskID)
		if err == nil {
			block, _ := vars["block"].(map[string]any)
			if block == nil {
				block = map[string]any{}
			}
			block["error"] = cause.Error()
			block["log"] = []any{cause.Error()}
			vars["block"] = block
			if raw, err := json.Marshal(vars); err == nil {
				_, _ = q.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, taskID, string(raw))
			}
		}
	}
	// A failed close does not reach onTaskClosed, so a retry parent hears
	// about the failure here instead (AC-84/88).
	if _, err := retryChildClosed(ctx, q, taskID, actorUserID); err != nil {
		return err
	}
	return nil
}

// evaluateIfWithScope evaluates an `if` against the whole task scope. AC-68
// writes `$blocks.<key>.output.success`; the `$` reference form is rewritten
// to a CEL identifier and the block outputs are passed in.
func evaluateIfWithScope(condition string, vars map[string]any) (bool, error) {
	trimmed := strings.TrimSpace(condition)
	if !strings.HasPrefix(trimmed, "expr:") {
		return false, fmt.Errorf("condition %q is not an expression: prefix it with %q", condition, "expr:")
	}
	expression := strings.TrimSpace(strings.TrimPrefix(trimmed, "expr:"))
	expression = strings.ReplaceAll(expression, "$blocks.", "blocks.")
	expression = strings.ReplaceAll(expression, "$inputs.", "inputs.")
	expression = strings.ReplaceAll(expression, "$outputs.", "outputs.")
	expression = strings.ReplaceAll(expression, "$locals.", "locals.")
	env, err := cel.NewEnv(
		cel.Variable("inputs", cel.DynType),
		cel.Variable("item", cel.DynType),
		cel.Variable("index", cel.DynType),
		cel.Variable("outputs", cel.DynType),
		cel.Variable("locals", cel.DynType),
		cel.Variable("blocks", cel.DynType),
	)
	if err != nil {
		return false, err
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("condition %q: %w", expression, issues.Err())
	}
	program, err := env.Program(ast)
	if err != nil {
		return false, fmt.Errorf("condition %q: %w", expression, err)
	}
	activation := map[string]any{}
	for _, key := range []string{"inputs", "item", "index", "outputs", "locals", "blocks"} {
		activation[key] = vars[key]
	}
	if activation["inputs"] == nil {
		activation["inputs"] = map[string]any{}
	}
	out, _, err := program.Eval(activation)
	if err != nil {
		return false, fmt.Errorf("condition %q: %w", expression, err)
	}
	value, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("condition %q is not a boolean", expression)
	}
	return value, nil
}
