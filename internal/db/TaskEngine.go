package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Task engine (issue #112, design: Task Composition Model.md).
//
// Uniform invariant: work happens only after a task enters in_progress.
// Container IS a task: all children spawn at parent spawn in the ready
// state. Per-child run_if conditions gate against the parent's variable
// scope. Script tasks run under the assigned Automation principal — that
// user is the audit principal for every CRUD write the script performs
// via ctx.records (captured by _audit_data_change, 009).

// State codes on the real model: pending/ready/in_progress/closed, stored
// in the text `state` column and guarded by chk_base_task_state. The design
// doc (rev 3) has children spawn in PENDING; the engine promotes to ready
// (same run_if gate: ready/pending -> in_progress, false -> closed).
//
// 'skipped' is not a state. A block that must not run is CLOSED with
// closure_reason 'skipped' (migration 104): it is done, and it is done
// because it did not run. That keeps one terminal state, one set of queries
// meaning "still outstanding", and a reason the UI can show.
const (
	taskStatePending    = "pending"
	taskStateReady      = "ready"
	taskStateInProgress = "in_progress"
	taskStateDone       = "closed"

	taskClosureSkipped = "skipped"
)

// taskTransitions is the engine's allowed-edge set, validated in Go
// against the text state column (077 removed base_task_transition).
var taskTransitions = map[string]map[string]bool{
	"ready":       {"ready": true, "in_progress": true, "closed": true},
	"in_progress": {"in_progress": true, "pending": true, "closed": true},
	"pending":     {"pending": true, "ready": true, "in_progress": true, "closed": true},
	"closed":      {},
}

// allowedTaskTransition reports whether from -> to is an engine edge.
// Same-state writes are allowed (engine re-asserts current state).
func allowedTaskTransition(from, to string) bool {
	edges, ok := taskTransitions[from]
	return ok && edges[to]
}

type TaskDefinition struct {
	ID             string
	Slug           string
	DisplayName    string
	Description    string
	IsContainer    bool
	ScriptText     string
	RunIfScript    string
	DefaultGroupID string
	// DefaultAssigneeID routes the instance to a person as well as a group.
	// A group alone leaves the task unassigned, and the 085 trigger refuses
	// in_progress without an assignee, so every spawned task needed a
	// dispatcher click before anyone could start it.
	DefaultAssigneeID string
	// Ask is what this definition asks of its doer (#225), inherited by
	// every instance it spawns. Empty when the definition predates the
	// column or has no verb; the spawn then falls back to the intake default.
	Ask string
	// AssignmentStrategy is one of the six values in Assignment.go, or empty
	// for the pre-strategy behaviour (098, Requirements AC-65).
	AssignmentStrategy string
	SpawnRules         []TaskSpawnRule
	IsActive           bool
}

type TaskSpawnRule struct {
	On             string         `json:"on"`
	DefinitionSlug string         `json:"definition_slug"`
	InputMapping   map[string]any `json:"input_mapping"`
	Assign         string         `json:"assign"` // "same" | "pool"
}

// getTaskDefinition loads a definition by id. Uses scriptQuerier so it
// works with Pool or an open transaction.
func getTaskDefinition(ctx context.Context, q scriptQuerier, id string) (*TaskDefinition, error) {
	var d TaskDefinition
	var groupID *string
	var spawnRulesJSON []byte
	err := q.QueryRow(ctx, `
		SELECT _id::text, slug, display_name, COALESCE(description, ''), is_container,
			script_text, run_if_script, default_group_id::text, spawn_rules, is_active,
			COALESCE(default_assignee_id::text, ''), COALESCE(ask, ''),
			-- to_jsonb keeps this readable on a pre-098 schema, where the
			-- column does not exist yet: the pre-migration parity test spawns
			-- a definition in a scratch 097 database through this loader.
			COALESCE(to_jsonb(d) ->> 'assignment_strategy', '')
		FROM base_task_definition d WHERE d._id = $1 AND d._deleted_at IS NULL`, id).Scan(
		&d.ID, &d.Slug, &d.DisplayName, &d.Description, &d.IsContainer, &d.ScriptText, &d.RunIfScript,
		&groupID, &spawnRulesJSON, &d.IsActive, &d.DefaultAssigneeID, &d.Ask, &d.AssignmentStrategy)
	if err != nil {
		return nil, fmt.Errorf("load task definition: %w", err)
	}
	if groupID != nil {
		d.DefaultGroupID = *groupID
	}
	d.SpawnRules = parseSpawnRules(spawnRulesJSON)
	return &d, nil
}

func parseSpawnRules(raw []byte) []TaskSpawnRule {
	rules := []TaskSpawnRule{}
	if len(raw) == 0 {
		return rules
	}
	_ = json.Unmarshal(raw, &rules)
	return rules
}

type childSpec struct {
	ChildDefinitionID string
	InputMapping      map[string]any
	DependsOn         []string
	DependsOnReason   string
}

// SpawnTaskInstance creates a task instance from a definition. Children of
// a container spawn immediately under parent_task_id, in pending (issue
// #206/#209). A container that is itself ready opens in the same call: it
// binds each child's input_mapping from its own scope and promotes the ones
// whose dependency edges are satisfied (090). Inputs bind at spawn for root
// tasks and at open for children, both against the parent's variable scope.
func SpawnTaskInstance(ctx context.Context, q scriptQuerier, definitionID, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
	// A spawn now opens the containers it creates, which is several writes.
	// When the caller hands us the pool rather than a transaction, give the
	// whole tree one so a failure half way cannot leave a request with
	// children that were spawned but never opened.
	var taskID string
	err := inSpawnTransaction(ctx, q, func(querier scriptQuerier) error {
		def, err := getTaskDefinition(ctx, querier, definitionID)
		if err != nil {
			return err
		}
		taskID, err = spawnTaskForDefinition(ctx, querier, def, parentTaskID, requestedBy, inputs)
		return err
	})
	return taskID, err
}

// inSpawnTransaction runs fn inside a transaction when the caller passed the
// pool, and directly when it already passed a transaction.
func inSpawnTransaction(ctx context.Context, q scriptQuerier, fn func(scriptQuerier) error) error {
	pool, ok := q.(*pgxpool.Pool)
	if !ok {
		return fn(q)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func spawnTaskForDefinition(ctx context.Context, q scriptQuerier, def *TaskDefinition, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
	return spawnTaskWithRoute(ctx, q, def, parentTaskID, requestedBy, inputs, nil)
}

// taskRoute overrides where a spawned instance lands. A non-nil route is
// explicit: a nil GroupID or AssigneeID clears that field rather than
// inheriting the definition's default, which is what a "pool" spawn rule
// means. Ready marks an event-driven spawn (a spawn rule fired by a closure)
// that has no template position to wait in — see spawnFollowOn.
type taskRoute struct {
	GroupID    *string
	AssigneeID *string
	Override   bool // apply GroupID/AssigneeID instead of the definition defaults
	Ready      bool
	// State, when set, is the state the instance is inserted in. The control
	// runtime uses it for a skipped branch; empty keeps the engine default.
	State string
	// ClosureReason is written with State, so a block that must not run is
	// inserted closed with closure_reason 'skipped' (104).
	ClosureReason string
	// Skip marks a task that must not run: a branch whose `if` was false.
	Skip bool
	// BlockMeta and Scope stamp the block identity and the $item/$index/$parent
	// values a control scope passes down (Design Vision §4).
	BlockMeta map[string]any
	Scope     map[string]any
	// Assignment overrides from a block (098). A nil pointer means the block
	// did not set that column, so the definition's value is inherited.
	AssignGroupOverride *string
	AssignStrategy      string
	AssignUserOverride  *string
	// AssignInputName is the user-typed input a from_input block reads, and
	// AssignLabel names the block in refusals and logs.
	AssignInputName string
	AssignLabel     string
	// AssignInputs is the run's inputs. A block spawns with empty inputs of
	// its own, so from_input reads the container's request instead.
	AssignInputs map[string]any
}

// spawnTaskWithRoute inserts one instance of def.
//
// Routing: the definition carries a default group and a default assignee, and
// the instance gets both, so a spawned task lands on the desk of the person
// who does that kind of work instead of waiting for a dispatcher. An assignee
// is only stamped when it is a member of the group; 085 rejects the write
// otherwise, and a misconfigured definition must not fail a whole spawn.
func spawnTaskWithRoute(ctx context.Context, q scriptQuerier, def *TaskDefinition, parentTaskID, requestedBy string, inputs map[string]any, route *taskRoute) (string, error) {
	if !def.IsActive {
		return "", fmt.Errorf("task definition %s is not active", def.Slug)
	}

	inputs = nonNilInputs(inputs)
	// A control tree can be refused before its first row exists: an
	// unreadable collection or one over the v1 cap must not leave a
	// half-spawned run behind (AC-34, AC-38).
	if def.IsContainer {
		if err := prevalidateControlBlocks(ctx, q, def.ID, inputs); err != nil {
			return "", err
		}
	}
	vars := map[string]any{"inputs": inputs, "locals": map[string]any{}, "outputs": map[string]any{}}
	if route != nil && route.BlockMeta != nil {
		vars["block"] = route.BlockMeta
	}
	if route != nil {
		mergeScope(vars, route.Scope)
	}
	var err error
	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}

	groupID := def.DefaultGroupID
	assigneeID := def.DefaultAssigneeID
	if route != nil && route.Override {
		groupID, assigneeID = "", ""
		if route.GroupID != nil {
			groupID = *route.GroupID
		}
		if route.AssigneeID != nil {
			assigneeID = *route.AssigneeID
		}
	}
	// Assignment (Plan T5). The definition names the group and the strategy;
	// a block may override the group, the strategy, or the named user it
	// reads. Resolved once, here, inside the spawn transaction.
	spec := assignmentSpec{
		GroupID:     groupID,
		Strategy:    def.AssignmentStrategy,
		NamedUserID: assigneeID,
		Label:       def.Slug,
	}
	if route != nil && !route.Override {
		if route.AssignGroupOverride != nil {
			spec.GroupID = *route.AssignGroupOverride
		}
		if route.AssignStrategy != "" {
			spec.Strategy = route.AssignStrategy
		}
		if route.AssignUserOverride != nil {
			spec.NamedUserID = *route.AssignUserOverride
		}
		if route.AssignInputName != "" {
			spec.InputName = route.AssignInputName
		}
		if route.AssignLabel != "" {
			spec.Label = route.AssignLabel
		}
	}
	if route != nil && route.Override {
		// An explicit route (a spawn rule's "pool") is already the answer:
		// the strategy must not pick somebody the rule did not name.
		spec.Strategy = ""
	}
	inputForAssignment := inputs
	if route != nil && route.AssignInputs != nil {
		inputForAssignment = route.AssignInputs
	}
	groupID, assigneeID, err = resolveAssignment(ctx, q, spec, requestedBy, inputForAssignment)
	if err != nil {
		return "", err
	}
	if route != nil && route.Skip {
		// Skips do not consume a round-robin position: the branch did not run.
		assigneeID = ""
	}

	state := spawnStateFor(def, parentTaskID)
	if route != nil && route.Ready {
		state = taskStateReady
	}
	if route != nil && route.State != "" {
		state = route.State
	}

	var taskID string
	var taskNumber string
	title := instanceTitle(ctx, q, def, parentTaskID)
	// A root may be named by its request (#226): a task spawned from an
	// external record is only findable by the caller's own handle for it
	// ("GH-226 …"), and the definition's display name cannot carry that.
	// Children are deliberately excluded — their title states the stage, and
	// inheriting the request title would name every stage after the issue.
	if strings.TrimSpace(parentTaskID) == "" {
		if requestedTitle := strings.TrimSpace(inputStringValue(inputs, "title")); requestedTitle != "" {
			title = requestedTitle
		}
	}
	// Instance fields: the definition's own description and the request's
	// priority/planned start are known at spawn time, so the doer's form is
	// populated from the first render instead of being blank until someone
	// types into it.
	description := nullable(instanceDescription(def, inputs))
	priority := instancePriority(inputs)
	plannedStart := instancePlannedStart(inputs)
	ask := InstanceAsk(def, inputs)
	err = q.QueryRow(ctx, `
		INSERT INTO base_task (number, title, work_type, state, priority, parent_task_id,
			assignment_group_id, assigned_user_id, requested_by_user_id, definition_id,
			description, planned_start_at, ask)
		VALUES (
			'', -- assigned by the autonumber trigger (051); base_task_number_seq was dropped there
			$1, 'TASK',
			$2,
			$3, $4, $5, $6, $7, $8,
			$9, $10, $11)
		RETURNING _id, number`,
		title, state, priority, nullable(parentTaskID), nullable(groupID),
		nullable(assigneeID), nullable(requestedBy), def.ID,
		description, plannedStart, ask).Scan(&taskID, &taskNumber)
	if err != nil {
		return "", fmt.Errorf("spawn task: %w", err)
	}
	// The scope is rows, not a column (#385): a task is inserted without one,
	// then its variables are written, in the same transaction.
	if err := WriteTaskVariables(ctx, q, taskID, vars); err != nil {
		return "", err
	}

	// Identity (#272): every run of a definition used to carry the same
	// title, so a board of 28 "Feature Request" cards was unreadable. The
	// number is assigned by the before-write trigger, so it is appended
	// after the insert rather than composed into the value above.
	composedTitle := composeInstanceTitle(title, taskNumber)
	if _, err := q.Exec(ctx, `UPDATE base_task SET title = $2 WHERE _id = $1`,
		taskID, composedTitle); err != nil {
		return "", fmt.Errorf("spawn task title: %w", err)
	}
	if route != nil && route.ClosureReason != "" {
		if _, err := q.Exec(ctx, `UPDATE base_task SET closure_reason = $2 WHERE _id = $1`,
			taskID, route.ClosureReason); err != nil {
			return "", fmt.Errorf("spawn task closure reason: %w", err)
		}
	}

	// Issue #332: the engine writes base_task directly, so the notification
	// emitted by HandleSave for a form save never ran here and a spawned task
	// landed on someone's desk silently. Best-effort: a notification is not
	// worth failing a spawn over.
	if err := EmitSpawnedTaskNotification(ctx, q, "base_task", taskID, requestedBy, strings.TrimSpace(parentTaskID) == "", map[string]any{
		"number":              taskNumber,
		"title":               composedTitle,
		"assigned_user_id":    assigneeID,
		"assignment_group_id": groupID,
	}); err != nil {
		log.Printf("failed to notify assignee of spawned task id=%s err=%v", taskID, err)
	}

	if def.IsContainer {
		// Children spawn with empty inputs; the container opening binds them
		// from its scope via input_mapping. A control tree's blocks are
		// spawned the same way, as rows the promotion pass then opens.
		skip := route != nil && route.Skip
		if err := spawnDefinitionScope(ctx, q, def.ID, taskID, requestedBy, inputs, nil, skip); err != nil {
			return "", err
		}
		if state == taskStateReady {
			if err := openContainer(ctx, q, taskID, requestedBy, 0); err != nil {
				return "", err
			}
		}
	}
	return taskID, nil
}

// spawnTaskWithRouteByID loads a definition and spawns one routed instance.
func spawnTaskWithRouteByID(ctx context.Context, q scriptQuerier, defID, parentTaskID, requestedBy string, inputs map[string]any, route *taskRoute) (string, error) {
	def, err := getTaskDefinition(ctx, q, defID)
	if err != nil {
		return "", err
	}
	return spawnTaskWithRoute(ctx, q, def, parentTaskID, requestedBy, inputs, route)
}

// openContainer starts a plan, not a job. A container holds no work of its
// own: its work is its children. Waiting for a person to press Start on each
// wrapper meant a three-level pipeline needed three clicks before the first
// real task appeared, and in practice nobody made them — the live Feature
// Request runs had a started root, a ready pipeline, and seven stages that
// stayed pending for ever.
//
// So a container that is itself ready opens its children in the same pass:
// bind their input mappings from the container's scope, then promote the ones
// whose dependency edges are satisfied. The container stays ready and closes
// by roll-up when its children finish; its state is the aggregate, not a
// person's claim on the wrapper.
func openContainer(ctx context.Context, q scriptQuerier, taskID, actorUserID string, depth int) error {
	if depth > maxContainerDepth {
		return fmt.Errorf("container nesting exceeded %d levels at task %s", maxContainerDepth, taskID)
	}
	defID, err := getDefinitionIDByTaskID(ctx, q, taskID)
	if err != nil || defID == "" {
		return err
	}
	def, err := getTaskDefinition(ctx, q, defID)
	if err != nil {
		return err
	}
	if !def.IsContainer {
		return nil
	}
	if err := bindChildInputs(ctx, q, taskID, actorUserID); err != nil {
		return err
	}
	return promoteChildrenDepth(ctx, q, taskID, actorUserID, depth+1)
}

// maxContainerDepth bounds container recursion.
const maxContainerDepth = 16

// spawnStateFor: root tasks spawn ready; container children spawn pending
// and are promoted by their container opening or by a dependency edge
// closing (issues #209, 090).
func spawnStateFor(def *TaskDefinition, parentTaskID string) string {
	if parentTaskID == "" {
		return taskStateReady
	}
	return taskStatePending
}

// StartTask advances a ready task to in_progress and runs engine hooks:
// container input_mapping binding to children (issue #208), run_if
// evaluation (false -> skipped), then the definition script. The
// Automation principal is the audit principal for script writes.
func StartTask(ctx context.Context, taskID, actorUserID string) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := transitionTaskInTx(ctx, tx, taskID, taskStateInProgress); err != nil {
		return err
	}
	if err := runStartHooksInTx(ctx, tx, taskID, actorUserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RunEngineHooksAfterStart runs the engine hooks for a task that has already
// entered in_progress through a write path that does not go through StartTask
// (issue #233: the record form sets the state column directly). It binds
// container child inputs, promotes/spawns children, and runs run_if and the
// definition script — everything StartTask does after the transition.
// No-op for tasks without a definition.
func RunEngineHooksAfterStart(ctx context.Context, taskID, actorUserID string) error {
	var defID *string
	if err := Pool.QueryRow(ctx, `SELECT definition_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&defID); err != nil {
		return err
	}
	if defID == nil || *defID == "" {
		return nil // not an engine task; nothing to run
	}
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := runStartHooksInTx(ctx, tx, taskID, actorUserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// runStartHooksInTx is the post-transition half of StartTask, shared with
// RunEngineHooksAfterStart.
func runStartHooksInTx(ctx context.Context, tx pgx.Tx, taskID, actorUserID string) error {
	if err := bindChildInputs(ctx, tx, taskID, actorUserID); err != nil {
		return err
	}
	if err := promoteChildren(ctx, tx, taskID, actorUserID); err != nil {
		return err
	}
	return runTaskHooks(ctx, tx, taskID, actorUserID)
}

// bindChildInputs applies each container child's input_mapping against the
// parent's variable scope and merges the result into the child's
// variables.inputs. Children spawn with empty inputs (spawnTaskForDefinition);
// this is where they receive the parent's context. Mapped values overwrite
// spawn-time inputs for the same key. Unresolvable $refs are skipped, not
// fatal — the child starts without that input and its run_if decides.
func bindChildInputs(ctx context.Context, tx scriptQuerier, parentTaskID, actorUserID string) error {
	defID, _, err := scopeDefinitionID(ctx, tx, parentTaskID)
	if err != nil || defID == "" {
		return err
	}
	parentVars, err := readTaskVariables(ctx, tx, parentTaskID)
	if err != nil {
		return err
	}
	blocks, err := getDefinitionBlocks(ctx, tx, defID)
	if err != nil {
		return err
	}
	if len(blocks) == 0 {
		return nil
	}

	// Instance children by block id, with definition id as the fallback for a
	// row the engine did not create from a block.
	rows, err := tx.Query(ctx, `
		SELECT _id::text, COALESCE(definition_id::text, '')
		FROM base_task
		WHERE parent_task_id = $1 AND _deleted_at IS NULL`, parentTaskID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type childRow struct {
		id, defID string
	}
	var children []childRow
	for rows.Next() {
		var child childRow
		if err := rows.Scan(&child.id, &child.defID); err != nil {
			return err
		}
		children = append(children, child)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	childIDs := make([]string, 0, len(children))
	for _, child := range children {
		childIDs = append(childIDs, child.id)
	}
	childVars, err := loadTaskVarsFor(ctx, tx, childIDs)
	if err != nil {
		return err
	}
	childTasks := map[string]string{}
	for _, child := range children {
		key := child.defID
		if blockID, _, _, _, _, _, ok := blockFromVars(childVars[child.id]); ok {
			key = "block:" + blockID
		}
		childTasks[key] = child.id
	}

	for _, b := range parentScopeBlocks(blocks, parentVars) {
		taskID, ok := childTasks["block:"+b.ID]
		if !ok {
			taskID, ok = childTasks[b.ChildDefinitionID]
			if !ok {
				continue
			}
		}
		inputs, _ := childVars[taskID]["inputs"].(map[string]any)
		if inputs == nil {
			inputs = map[string]any{}
		}
		// A for_each element's scope carries $item/$index; a mapping may name
		// them, so they are part of what this child's inputs resolve against.
		scope := map[string]any{}
		for key, value := range parentVars {
			scope[key] = value
		}
		for _, key := range []string{"item", "index"} {
			if value, present := childVars[taskID][key]; present {
				scope[key] = value
			}
		}
		// A control block binds no inputs of its own: a run_task's
		// {from, value} rows are resolved when the block executes (AC-105),
		// not as plain values here.
		if !b.IsControl() {
			for childInput, source := range b.InputMapping {
				val, ok := resolveScopeRef(scope, source)
				if !ok {
					log.Printf("task engine: input_mapping %q -> %v unresolvable against parent scope, omitted (task %s)", childInput, source, taskID)
					continue
				}
				inputs[childInput] = val
			}
		}
		// Write only the names that changed: two bindings running at once
		// must not overwrite each other (#385).
		if err := UpdateTaskVariables(ctx, tx, taskID, func(childScope map[string]any) error {
			childScope["inputs"] = inputs
			// $parent.* walks up one level from any spawned task (AC-31).
			childScope["parent"] = parentVars
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// promoteChildren advances a container's pending children whose dependency
// edges are all satisfied: to ready, or to skipped when the child's run_if
// evaluates false. Called from StartTask (parent start: dependency-free
// children go ready) and from the close path (a sibling closed/skipped may
// satisfy the next wave). Loops until no change, so a skipped chain frees
// its dependents in the same pass.
//
// Satisfaction rules (issue #209):
//   - a skipped sibling always satisfies (one run_if:false must not deadlock)
//   - a closed sibling satisfies when the edge's depends_on_reason is ”
//     and its closure_reason is completed or cancelled (success), or when
//     the reason names that closure_reason exactly (rejection loops wait
//     for the matching verdict)
func promoteChildren(ctx context.Context, tx scriptQuerier, parentTaskID, actorUserID string) error {
	return promoteChildrenDepth(ctx, tx, parentTaskID, actorUserID, 0)
}

// promoteChildrenDepth is promoteChildren with the container depth carried
// through, so a definition that (accidentally) contains itself cannot
// recurse without bound.
func promoteChildrenDepth(ctx context.Context, tx scriptQuerier, parentTaskID, actorUserID string, depth int) error {
	for {
		promoted, err := promoteChildrenOnce(ctx, tx, parentTaskID, actorUserID, depth)
		if err != nil {
			return err
		}
		if !promoted {
			return nil
		}
	}
}

type childInstanceState struct {
	taskID string
	defID  string
	state  string
	reason *string
	vars   map[string]any
}

func promoteChildrenOnce(ctx context.Context, tx scriptQuerier, parentTaskID, actorUserID string, depth int) (bool, error) {
	// Sibling instances: def id -> (state, closure reason), and block id ->
	// instances. A run row carries its block identity in variables.block; a
	// row without one (a spawn-rule follow-on, or a child the v1 editor
	// created) keeps the definition-keyed path.
	rows, err := tx.Query(ctx, `
		SELECT _id::text, COALESCE(definition_id::text, ''), state, closure_reason
		FROM base_task
		WHERE parent_task_id = $1 AND _deleted_at IS NULL`, parentTaskID)
	if err != nil {
		return false, err
	}
	var pending []childInstanceState
	var siblingIDs []string
	for rows.Next() {
		var st childInstanceState
		if err := rows.Scan(&st.taskID, &st.defID, &st.state, &st.reason); err != nil {
			rows.Close()
			return false, err
		}
		pending = append(pending, st)
		siblingIDs = append(siblingIDs, st.taskID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	// The sibling scopes come from the variables table in one query: block
	// identity lives in `block.id`, and promotion needs it for every sibling.
	siblingScopes, err := loadTaskVarsFor(ctx, tx, siblingIDs)
	if err != nil {
		return false, err
	}
	siblings := map[string][]childInstanceState{}
	siblingsByBlock := map[string][]childInstanceState{}
	var unmarked []childInstanceState
	for _, st := range pending {
		st.vars = siblingScopes[st.taskID]
		siblings[st.defID] = append(siblings[st.defID], st)
		if blockID, _, _, _, _, _, ok := blockFromVars(st.vars); ok {
			siblingsByBlock[blockID] = append(siblingsByBlock[blockID], st)
		} else {
			unmarked = append(unmarked, st)
		}
	}

	// Template edges for this container's definition. A control task has no
	// definition of its own; scopeDefinitionID reads the definition it was
	// authored in from its block metadata. The blocks that matter are the
	// parent's scope: a control block's children, not the definition's root
	// scope.
	defID, parentVars, err := scopeDefinitionID(ctx, tx, parentTaskID)
	if err != nil || defID == "" {
		return false, err
	}
	blocks, err := getDefinitionBlocks(ctx, tx, defID)
	if err != nil {
		return false, err
	}

	changed := false
	scope := parentScopeBlocks(blocks, parentVars)
	for index, b := range scope {
		// AC-106: a run_task block is synchronous composition — the parent
		// does not advance past it until the spawned child is terminal. A
		// later sibling's row is created with the scope as a pending
		// placeholder, but it is not opened while an earlier run_task is
		// still open. When that block closes, its roll-up re-runs this pass
		// and the sibling is promoted then.
		if runTaskBarrier(scope[:index], siblingsByBlock) {
			continue
		}
		instances := siblingsByBlock[b.ID]
		if b.IsControl() && b.Type == "end" {
			// AC-37: `end` is a control task that closes when the siblings it
			// scopes are closed, through this promotion pass rather than a
			// second scheduler.
			if len(instances) == 0 || !earlierSiblingsTerminal(scope[:index], siblingsByBlock) {
				continue
			}
			for _, inst := range instances {
				if inst.state != taskStatePending && inst.state != taskStateReady {
					continue
				}
				if inst.state == taskStatePending {
					if err := transitionTaskInTx(ctx, tx, inst.taskID, taskStateReady); err != nil {
						return false, err
					}
				}
				if err := transitionTaskInTx(ctx, tx, inst.taskID, taskStateDone); err != nil {
					return false, err
				}
				if _, err := tx.Exec(ctx, `UPDATE base_task SET closure_reason = 'completed' WHERE _id = $1`,
					inst.taskID); err != nil {
					return false, err
				}
				if err := onTaskClosed(ctx, tx, inst.taskID, "completed", actorUserID); err != nil {
					return false, err
				}
				if err := rollUpAncestors(ctx, tx, inst.taskID, actorUserID); err != nil {
					return false, err
				}
				changed = true
			}
			continue
		}
		if len(instances) == 0 {
			continue
		}
		for _, inst := range instances {
			if inst.state != taskStatePending {
				continue
			}
			if !blockDepsSatisfied(b, siblingsByBlock) {
				continue
			}
			if err := promoteBlockInstance(ctx, tx, parentTaskID, inst, b, actorUserID, depth); err != nil {
				return false, err
			}
			changed = true
		}
	}

	// Legacy path: instances with no block identity, matched to a block by
	// their child definition id, exactly as before 098.
	if len(unmarked) > 0 {
		specByChild := map[string]childSpec{}
		for _, b := range scope {
			if b.Kind != "task" || b.ChildDefinitionID == "" {
				continue
			}
			specByChild[b.ChildDefinitionID] = childSpec{
				ChildDefinitionID: b.ChildDefinitionID,
				InputMapping:      b.InputMapping,
				DependsOn:         b.DependsOn,
				DependsOnReason:   b.DependsOnReason,
			}
		}
		legacySiblings := map[string][]childInstanceState{}
		for _, inst := range unmarked {
			legacySiblings[inst.defID] = append(legacySiblings[inst.defID], inst)
		}
		for cid, instances := range legacySiblings {
			spec, hasSpec := specByChild[cid]
			if !hasSpec {
				continue
			}
			if len(spec.DependsOn) == 0 {
				for _, inst := range instances {
					if inst.state != taskStatePending {
						continue
					}
					if err := promoteInstance(ctx, tx, parentTaskID, inst, spec, actorUserID, depth); err != nil {
						return false, err
					}
					changed = true
				}
				continue
			}
			for _, inst := range instances {
				if inst.state != taskStatePending {
					continue
				}
				if depsSatisfied(spec.DependsOn, spec.DependsOnReason, legacySiblings) {
					if err := promoteInstance(ctx, tx, parentTaskID, inst, spec, actorUserID, depth); err != nil {
						return false, err
					}
					changed = true
				}
			}
		}
	}
	return changed, nil
}

// runTaskBarrier reports whether an earlier run_task block in this scope is
// still open. It is closed by its spawned child reaching a terminal state and
// the roll-up that follows (AC-105, AC-106).
func runTaskBarrier(earlier []DefinitionBlock, instances map[string][]childInstanceState) bool {
	for _, b := range earlier {
		if !b.IsControl() || b.Type != "run_task" {
			continue
		}
		for _, inst := range instances[b.ID] {
			if !isTerminalTaskState(inst.state) {
				return true
			}
		}
	}
	return false
}

// earlierSiblingsTerminal reports whether every instance of every block
// before this one has reached a terminal state. An `end` waits for those.
func earlierSiblingsTerminal(earlier []DefinitionBlock, instances map[string][]childInstanceState) bool {
	for _, b := range earlier {
		list := instances[b.ID]
		if len(list) == 0 {
			continue
		}
		for _, inst := range list {
			if !isTerminalTaskState(inst.state) {
				return false
			}
		}
	}
	return true
}

// blockDepsSatisfied reports whether every block this one waits for has a
// terminal sibling whose outcome satisfies the edge. Block edges win over
// the legacy definition-id edges; a pre-098 row has only the latter, which
// is mapped onto this scope's blocks by child definition id.
func blockDepsSatisfied(b DefinitionBlock, instances map[string][]childInstanceState) bool {
	deps := b.DependsOnBlockIDs
	if len(deps) == 0 && len(b.DependsOn) > 0 {
		for _, defID := range b.DependsOn {
			for blockID, list := range instances {
				if len(list) > 0 && list[0].defID == defID {
					deps = append(deps, blockID)
					break
				}
			}
		}
	}
	if len(deps) == 0 {
		return true
	}
	for _, dep := range deps {
		list := instances[dep]
		if len(list) == 0 {
			return false
		}
		satisfied := false
		for _, inst := range list {
			if instanceSatisfies(inst, b.DependsOnReason) {
				satisfied = true
				break
			}
		}
		if !satisfied {
			return false
		}
	}
	return true
}

// promoteBlockInstance opens one block's run row: bind its mapping from the
// parent scope, then either promote a work task or evaluate a control block.
func promoteBlockInstance(ctx context.Context, tx scriptQuerier, parentTaskID string, inst childInstanceState, b DefinitionBlock, actorUserID string, depth int) error {
	parentVars, err := readTaskVariables(ctx, tx, parentTaskID)
	if err != nil {
		return err
	}
	inputs, _ := inst.vars["inputs"].(map[string]any)
	if inputs == nil {
		inputs = map[string]any{}
	}
	// A mapping resolves against the enclosing task's scope, plus this
	// instance's own $item/$index when it is one iteration of a for_each
	// (AC-30). $parent.* is already part of the parent's variables.
	mappingScope := map[string]any{}
	for key, value := range parentVars {
		mappingScope[key] = value
	}
	for _, key := range []string{"item", "index"} {
		if value, present := inst.vars[key]; present {
			mappingScope[key] = value
		}
	}
	// A control block has no input mapping of its own: its scope is the
	// enclosing task's scope (promoteControlInstance binds that). A run_task
	// block's rows are {from, value} pairs resolved by runRunTaskBlock at
	// execution time (AC-99, AC-105), not plain values to bind here.
	if !b.IsControl() {
		for childInput, source := range b.InputMapping {
			val, ok := resolveScopeRef(mappingScope, source)
			if !ok {
				log.Printf("task engine: input_mapping %q -> %v unresolvable promoting block %q of task %s, omitted",
					childInput, source, b.Label, parentTaskID)
				continue
			}
			inputs[childInput] = val
		}
	}
	inst.vars["inputs"] = inputs
	// The binding is the only part of the scope this path changed: write that
	// scope, not the whole map the sibling scan produced.
	if err := SetTaskVariableScope(ctx, tx, inst.taskID, VarScopeInput, inputs); err != nil {
		return err
	}

	if b.IsControl() {
		return promoteControlInstance(ctx, tx, parentTaskID, inst, b, bConfigContainerID(inst.vars), actorUserID, depth)
	}

	defID := b.ChildDefinitionID
	if defID == "" {
		return fmt.Errorf("block %q has no definition to spawn", b.Label)
	}
	def, err := getTaskDefinition(ctx, tx, defID)
	if err != nil {
		return err
	}

	if strings.TrimSpace(def.RunIfScript) != "" {
		allowed, evalErr := evalTaskCondition(ctx, tx, inst.taskID, def.RunIfScript, inst.vars, actorUserID)
		if evalErr != nil {
			return fmt.Errorf("run_if evaluation failed for task %s: %w", inst.taskID, evalErr)
		}
		if !allowed {
			return skipTaskInTx(ctx, tx, inst.taskID, actorUserID)
		}
	}
	if err := transitionTaskInTx(ctx, tx, inst.taskID, taskStateReady); err != nil {
		return err
	}
	var currentDescription string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(description, '') FROM base_task WHERE _id = $1`,
		inst.taskID).Scan(&currentDescription); err != nil {
		return fmt.Errorf("load task description: %w", err)
	}
	description := instanceDescription(def, inputs)
	refreshDescription := description != "" && descriptionIsEngineAuthored(currentDescription, def)
	if _, err := tx.Exec(ctx, `
		UPDATE base_task
		SET description = CASE WHEN $2 THEN $3 ELSE description END
		WHERE _id = $1`, inst.taskID, refreshDescription, description); err != nil {
		return err
	}
	if def.IsContainer {
		return openContainer(ctx, tx, inst.taskID, actorUserID, depth)
	}
	return nil
}

// bConfigContainerID reads the definition a control task was authored in.
func bConfigContainerID(vars map[string]any) string {
	_, containerDefID, _, _, _, _, _ := blockFromVars(vars)
	return containerDefID
}

// depsSatisfied reports whether every dependency definition has a sibling
// instance whose outcome satisfies the edge.
func depsSatisfied(deps []string, reason string, siblings map[string][]childInstanceState) bool {
	for _, dep := range deps {
		instances := siblings[dep]
		if len(instances) == 0 {
			return false
		}
		ok := false
		for _, inst := range instances {
			if instanceSatisfies(inst, reason) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func instanceSatisfies(inst childInstanceState, reason string) bool {
	if inst.state != taskStateDone {
		return false
	}
	r := ""
	if inst.reason != nil {
		r = *inst.reason
	}
	// A skipped sibling always satisfies: one run_if:false must not deadlock
	// the block that waits on it (issue #209).
	if r == taskClosureSkipped {
		return true
	}
	if reason == "" {
		return r == "completed" || r == "cancelled"
	}
	return r == reason
}

// promoteInstance moves one pending child to ready (or closes it skipped when
// its run_if is false) and re-binds its input_mapping against the parent scope,
// so inputs referencing $outputs.<predecessor> resolve now that the
// predecessor has published.
func promoteInstance(ctx context.Context, tx scriptQuerier, parentTaskID string, inst childInstanceState, spec childSpec, actorUserID string, depth int) error {
	def, err := getTaskDefinition(ctx, tx, inst.defID)
	if err != nil {
		return err
	}

	// Re-bind inputs from the current parent scope.
	parentVars, err := readTaskVariables(ctx, tx, parentTaskID)
	if err != nil {
		return err
	}
	inputs, _ := inst.vars["inputs"].(map[string]any)
	if inputs == nil {
		inputs = map[string]any{}
	}
	for childInput, source := range spec.InputMapping {
		val, ok := resolveScopeRef(parentVars, source)
		if !ok {
			// Tolerated here because the parent is still running and later
			// siblings may publish; but never silent (#217).
			log.Printf("task engine: input_mapping %q -> %v unresolvable promoting child %s of task %s, omitted",
				childInput, source, inst.taskID, parentTaskID)
			continue
		}
		inputs[childInput] = val
	}
	inst.vars["inputs"] = inputs
	if err := SetTaskVariableScope(ctx, tx, inst.taskID, VarScopeInput, inputs); err != nil {
		return err
	}

	// run_if gate at promotion, matching the engine's start semantics.
	if strings.TrimSpace(def.RunIfScript) != "" {
		allowed, evalErr := evalTaskCondition(ctx, tx, inst.taskID, def.RunIfScript, inst.vars, actorUserID)
		if evalErr != nil {
			return fmt.Errorf("run_if evaluation failed for task %s: %w", inst.taskID, evalErr)
		}
		if !allowed {
			return skipTaskInTx(ctx, tx, inst.taskID, actorUserID)
		}
	}

	if err := transitionTaskInTx(ctx, tx, inst.taskID, taskStateReady); err != nil {
		return err
	}
	// The body that makes the task legible is only bound now: a child spawns
	// with empty inputs and learns the requester's words when its
	// input_mapping resolves. Refresh the description with the bound inputs,
	// but never overwrite text a human has rewritten.
	var currentDescription string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(description, '') FROM base_task WHERE _id = $1`,
		inst.taskID).Scan(&currentDescription); err != nil {
		return fmt.Errorf("load task description: %w", err)
	}
	description := instanceDescription(def, inputs)
	refreshDescription := description != "" && descriptionIsEngineAuthored(currentDescription, def)
	_, err = tx.Exec(ctx, `
		UPDATE base_task
		SET description = CASE WHEN $2 THEN $3 ELSE description END
		WHERE _id = $1`, inst.taskID, refreshDescription, description)
	if err != nil {
		return err
	}
	// A stage that has just become ready opens itself: its children are the
	// work, and nobody should have to claim the wrapper first.
	if def.IsContainer {
		return openContainer(ctx, tx, inst.taskID, actorUserID, depth)
	}
	return nil
}

// transitionTaskInTx performs one validated state transition in the
// caller's transaction.
func transitionTaskInTx(ctx context.Context, tx scriptQuerier, taskID, toStateCode string) error {
	var fromState string
	err := tx.QueryRow(ctx, `
		SELECT state FROM base_task
		WHERE _id = $1 AND _deleted_at IS NULL
		FOR UPDATE`, taskID).Scan(&fromState)
	if err != nil {
		return fmt.Errorf("load task: %w", err)
	}

	if !allowedTaskTransition(fromState, toStateCode) {
		return fmt.Errorf("invalid task transition %s -> %s", fromState, toStateCode)
	}

	_, err = tx.Exec(ctx, `
		UPDATE base_task SET state = $2 WHERE _id = $1`, taskID, toStateCode)
	return err
}

// skipTaskInTx closes a task that must not run. Since 104 that is an
// ordinary close with closure_reason 'skipped': terminal, publishes nothing,
// and the same roll-up path as any other close, so a scope of skipped blocks
// still collapses to its container.
func skipTaskInTx(ctx context.Context, tx scriptQuerier, taskID, actorUserID string) error {
	if err := transitionTaskInTx(ctx, tx, taskID, taskStateDone); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE base_task SET closure_reason = $2 WHERE _id = $1`,
		taskID, taskClosureSkipped); err != nil {
		return err
	}
	if err := onTaskClosed(ctx, tx, taskID, taskClosureSkipped, actorUserID); err != nil {
		return err
	}
	return rollUpAncestors(ctx, tx, taskID, actorUserID)
}

// runTaskHooks: on entering in_progress — evaluate run_if (false -> skip),
// then run the definition script for script tasks.
func runTaskHooks(ctx context.Context, tx pgx.Tx, taskID, actorUserID string) error {
	defID, err := getDefinitionIDByTaskID(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if defID == "" {
		return nil // hand-made task: no engine hooks
	}
	def, err := getTaskDefinition(ctx, tx, defID)
	if err != nil {
		return err
	}
	vars, err := readTaskVariables(ctx, tx, taskID)
	if err != nil {
		return err
	}

	if strings.TrimSpace(def.RunIfScript) != "" {
		allowed, evalErr := evalTaskCondition(ctx, tx, taskID, def.RunIfScript, vars, actorUserID)
		if evalErr != nil {
			return fmt.Errorf("run_if evaluation failed for task %s: %w", taskID, evalErr)
		}
		if !allowed {
			return skipTaskInTx(ctx, tx, taskID, actorUserID)
		}
	}

	if strings.TrimSpace(def.ScriptText) != "" {
		if err := runTaskScript(ctx, tx, taskID, def.ScriptText, vars, actorUserID); err != nil {
			return fmt.Errorf("task script failed for task %s: %w", taskID, err)
		}
	}
	return nil
}

func evalTaskCondition(ctx context.Context, tx scriptQuerier, taskID, code string, vars map[string]any, actorUserID string) (bool, error) {
	result, err := executeTaskGoja(ctx, tx, taskID, code, vars, actorUserID)
	if err != nil {
		return false, err
	}
	b, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf("run_if script must return a boolean, got %T", result)
	}
	return b, nil
}

func runTaskScript(ctx context.Context, tx scriptQuerier, taskID, code string, vars map[string]any, actorUserID string) error {
	result, err := executeTaskGoja(ctx, tx, taskID, code, vars, actorUserID)
	if err != nil {
		return err
	}
	return publishScriptOutputs(ctx, tx, taskID, result)
}

// publishScriptOutputs stores what a task script returned.
//
// Every definition's script ends in `return { outputs: {...} }`, and the whole
// variable chain — a stage's declared outputs, a sibling's input_mapping,
// the gate that judges an artefact — reads $outputs.<task>.<name>. Nothing
// wrote the returned object anywhere: the value was discarded, so a script
// task published an empty output map and every $outputs reference downstream
// resolved to nothing, silently, because an unresolvable reference is
// skipped rather than fatal. `intake_validation` returning route, and
// `deployment_scheduling` returning the window, both went nowhere.
func publishScriptOutputs(ctx context.Context, q scriptQuerier, taskID string, result any) error {
	if result == nil {
		return nil
	}
	returned, ok := result.(map[string]any)
	if !ok {
		return nil // a script returning a bare value has no outputs to publish
	}
	returnedOutputs, ok := returned["outputs"].(map[string]any)
	if !ok || len(returnedOutputs) == 0 {
		return nil
	}

	return UpdateTaskVariables(ctx, q, taskID, func(vars map[string]any) error {
		outputs, _ := vars["outputs"].(map[string]any)
		if outputs == nil {
			outputs = map[string]any{}
		}
		for name, value := range returnedOutputs {
			outputs[name] = value
		}
		vars["outputs"] = outputs
		return nil
	})
}

// executeTaskGoja runs Goja code with the task scope in Input. Writes the
// script performs via ctx.records are audited to actorUserID.
func executeTaskGoja(ctx context.Context, tx scriptQuerier, taskID, code string, vars map[string]any, actorUserID string) (any, error) {
	opts := ScriptExecutionOptions{
		Code:        code,
		TableName:   "base_task",
		TriggerType: "task_engine",
		EventName:   "task_run",
		UserID:      actorUserID,
		// A task run with no actor was started by the engine, not by a
		// person: a scheduled job, or a definition spawned by another task.
		// It runs as the system, with actorUserID still stamped for audit
		// when one was supplied.
		System: strings.TrimSpace(actorUserID) == "",
		Input: map[string]any{
			"task_id":   taskID,
			"variables": vars,
		},
	}
	result, err := executeJavaScriptWithQuerier(ctx, tx, opts)
	if err != nil {
		return nil, err
	}
	return result.Result, nil
}

// CompleteTask closes a task (state closed, closure_reason completed),
// publishing its declared outputs to the parent scope, firing the
// definition's spawn rules, and rolling up any ancestor container whose
// children are now all terminal.
func CompleteTask(ctx context.Context, taskID, actorUserID string) error {
	return CloseTaskWithReason(ctx, taskID, actorUserID, "completed")
}

// CloseTaskWithReason is the engine-side close for every surface: it sets
// state and reason, then runs the same post-close hooks a form save runs.
func CloseTaskWithReason(ctx context.Context, taskID, actorUserID, closureReason string) error {
	reason := normalizeClosureReason(closureReason)
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// A script task that is closed without ever being started has not done its
	// work: the script is the work, and it runs on in_progress. Closing it
	// from the form would otherwise publish an empty output map and hand a
	// gate an artefact that was never produced. Run the start hooks first so
	// the script (and its run_if) happen exactly once.
	if err := runStartHooksBeforeClose(ctx, tx, taskID, actorUserID); err != nil {
		return err
	}
	// run_if may have decided the task should be skipped instead: it is
	// terminal already, so there is nothing left to close.
	var stateAfterHooks string
	if err := tx.QueryRow(ctx, `SELECT state FROM base_task WHERE _id = $1`, taskID).Scan(&stateAfterHooks); err != nil {
		return err
	}
	if stateAfterHooks == taskStateDone {
		// run_if may have closed it as skipped; terminal, nothing to close.
		return tx.Commit(ctx)
	}

	if err := transitionTaskInTx(ctx, tx, taskID, taskStateDone); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE base_task SET closure_reason = $2 WHERE _id = $1`, taskID, reason); err != nil {
		return err
	}
	if err := onTaskClosed(ctx, tx, taskID, reason, actorUserID); err != nil {
		return err
	}
	if err := rollUpAncestors(ctx, tx, taskID, actorUserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// onTaskClosed publishes the task's outputs into the parent's variables
// (namespaced by child slug), promotes dependents whose edges this closure
// satisfies, and fires spawn rules for the closure reason.
func onTaskClosed(ctx context.Context, tx scriptQuerier, taskID, closureReason, actorUserID string) error {
	// A child of a retry block is not an ordinary sibling: its closure decides
	// what the attempt did, and the retry block owns when its own row closes
	// (Requirements slice 5). Handled here, before promotion, so a failed
	// attempt does not first promote the work it is about to discard.
	if handled, err := retryChildClosed(ctx, tx, taskID, actorUserID); err != nil {
		return err
	} else if handled {
		return nil
	}
	var parentID *string
	err := tx.QueryRow(ctx,
		`SELECT parent_task_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&parentID)
	if err != nil {
		return err
	}

	vars, err := readTaskVariables(ctx, tx, taskID)
	if err != nil {
		return err
	}
	outputs, _ := vars["outputs"].(map[string]any)

	// A container's declared outputs are answered by its children: fill them
	// before publishing upward so a sibling stage's mapping can bind.
	if containerYAML, isContainer, err := containerVariablesYAML(ctx, tx, taskID); err == nil && isContainer {
		if merged, mergeErr := mergeContainerOutputs(ctx, tx, taskID, vars, containerYAML); mergeErr == nil {
			outputs = merged
		}
	}

	// A skipped close publishes nothing (AC-28): the block did not run, so it
	// has no output to hand a successor. Promotion and roll-up still run.
	if closureReason == taskClosureSkipped {
		outputs = nil
	}

	// Publish up: parent's variables.outputs.<child slug> = this outputs.
	if parentID != nil && *parentID != "" && outputs != nil {
		var defID *string
		_ = tx.QueryRow(ctx, `SELECT definition_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&defID)
		if defID != nil && *defID != "" {
			var slug string
			if err := tx.QueryRow(ctx, `SELECT slug FROM base_task_definition WHERE _id = $1`, *defID).Scan(&slug); err == nil {
				// One child's output is one name in the parent's outputs: a
				// sibling closing at the same moment writes its own name.
				_ = UpdateTaskVariables(ctx, tx, *parentID, func(parentVars map[string]any) error {
					parentOutputs, _ := parentVars["outputs"].(map[string]any)
					if parentOutputs == nil {
						parentOutputs = map[string]any{}
					}
					parentOutputs[slug] = outputs
					parentVars["outputs"] = parentOutputs
					return nil
				})
			}
		}
	}

	// A root task has no parent to promote: passing an empty id to the
	// sibling query would be an invalid uuid cast, so short-circuit.
	if parent := parentIDOrEmpty(parentID); parent != "" {
		if err := promoteChildren(ctx, tx, parent, actorUserID); err != nil {
			return err
		}
	}

	return fireSpawnRules(ctx, tx, taskID, closureReason, actorUserID)
}

func parentIDOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// fireSpawnRules spawns follow-on tasks per the definition's spawn_rules
// for the given closure reason. assign "same" copies this task's group;
// "pool" leaves the follow-on ungrouped for manual routing.
func fireSpawnRules(ctx context.Context, tx scriptQuerier, taskID, closureReason, actorUserID string) error {
	defID, err := getDefinitionIDByTaskID(ctx, tx, taskID)
	if err != nil || defID == "" {
		return err
	}
	def, err := getTaskDefinition(ctx, tx, defID)
	if err != nil {
		return err
	}

	var groupID *string
	var assigneeID *string
	var parentID *string
	_ = tx.QueryRow(ctx,
		`SELECT assignment_group_id::text, assigned_user_id::text, parent_task_id::text FROM base_task WHERE _id = $1`,
		taskID).Scan(&groupID, &assigneeID, &parentID)

	vars, err := readTaskVariables(ctx, tx, taskID)
	if err != nil {
		return err
	}
	// The closing task is part of the scope a spawn rule resolves against, so
	// a rule can name a target it cannot know at design time: a reviewer
	// records which definition produced the artefact it judged and rejects
	// back to that definition. One gate definition then serves every stage
	// instead of one gate per loop.
	vars["task"] = map[string]any{"id": taskID, "definition_slug": def.Slug}

	for _, rule := range def.SpawnRules {
		if rule.On != closureReason {
			continue
		}
		slug := rule.DefinitionSlug
		if strings.HasPrefix(strings.TrimSpace(slug), "$") {
			val, ok := resolveScopeRef(vars, slug)
			resolved, isString := val.(string)
			if !ok || !isString || strings.TrimSpace(resolved) == "" {
				log.Printf("task engine: spawn rule target %q unresolvable on task %s; rule skipped", slug, taskID)
				continue
			}
			slug = resolved
		}
		// A spawn rule fires because the closing task just published its
		// scope, so by now every cross-task reference it can make has
		// resolved. A mapping that still resolves to nothing there is a
		// template bug, not an ordering accident: fail the close transaction
		// rather than spawn a child that silently runs without the input it
		// was promised (#217).
		//
		// The one exception is the closing task's OWN output namespace. A
		// human may reject a gate without writing a comment, or complete a
		// task whose script returned only some of its declared outputs; the
		// definition is then fine and the value is simply absent. Losing the
		// whole rejection over an unwritten optional field is worse than the
		// old silent drop, so that input alone is omitted and logged.
		//
		// The two are distinguishable by shape: "$outputs.<slug>.<field>"
		// (three or more segments) walks into a published child scope and
		// must bind; "$outputs.<name>" (two segments) names an output of the
		// closing task itself. Only the latter is lenient.
		inputs := map[string]any{}
		for childInput, source := range rule.InputMapping {
			val, err := resolveScopeRefStrict(vars, source,
				fmt.Sprintf("spawn rule for %q on task %s", slug, taskID))
			if err != nil {
				if name, own := ownOutputRefName(source); own {
					log.Printf("task engine: spawn rule for %q on task %s (definition %q) omitted input %q: own output %q was not published; spawn continues",
						slug, taskID, def.Slug, childInput, name)
					continue
				}
				return err
			}
			inputs[childInput] = val
		}
		// A follow-on fires because something closed: its slot in the plan
		// has arrived, so it spawns ready rather than waiting for a
		// promotion that will never come (promotion only walks the
		// container's static child list, which a rule target need not be in).
		route := &taskRoute{Ready: true, Override: true, GroupID: groupID, AssigneeID: assigneeID}
		switch rule.Assign {
		case "pool":
			route.GroupID, route.AssigneeID = nil, nil
		case "definition":
			route = &taskRoute{Ready: true}
		}
		if err := spawnFollowOn(ctx, tx, slug, route, parentID, actorUserID, inputs); err != nil {
			return err
		}
	}
	return nil
}

// spawnFollowOn spawns a spawn-rule follow-on. It inherits the closing
// task's parent_task_id unless the rule says otherwise, so chained tasks
// stay inside the container and their outputs publish upward (issue #209).
func spawnFollowOn(ctx context.Context, tx scriptQuerier, slug string, route *taskRoute, parentTaskID *string, requestedBy string, inputs map[string]any) error {
	var defID string
	err := tx.QueryRow(ctx,
		`SELECT _id::text FROM base_task_definition WHERE slug = $1 AND is_active AND _deleted_at IS NULL`, slug).Scan(&defID)
	if err != nil {
		return fmt.Errorf("spawn rule references unknown definition %q: %w", slug, err)
	}
	def, err := getTaskDefinition(ctx, tx, defID)
	if err != nil {
		return err
	}
	parent := ""
	if parentTaskID != nil {
		parent = *parentTaskID
	}
	_, err = spawnTaskWithRoute(ctx, tx, def, parent, requestedBy, inputs, route)
	return err
}

// validatedAssignee returns assigneeID only when it is a member of groupID.
// 085 rejects any other pairing, and a definition with a stale assignee must
// not fail the spawn that carries it — the task lands unassigned instead,
// which is visible and recoverable.
// resolveSpawnAssignee decides who a spawned task belongs to. An explicit
// default assignee wins, provided it is really a member of the group (085
// refuses to start a task assigned to a non-member, so an invalid default
// would produce work nobody can begin). Otherwise the group's sole member is
// used: a one-member group has exactly one possible answer, so this invents no
// routing policy, while an ambiguous group stays unassigned for a dispatcher
// (#276). Before this, 42 of 45 spawned tasks arrived unassigned and needed a
// click each before any work could start.
func resolveSpawnAssignee(ctx context.Context, q scriptQuerier, groupID, assigneeID string) string {
	if groupID == "" {
		return ""
	}
	if assigneeID != "" {
		return validatedAssignee(ctx, q, groupID, assigneeID)
	}
	var count int64
	var sole *string
	if err := q.QueryRow(ctx, `
		SELECT COUNT(*), MIN(user_id)
		FROM _group_membership WHERE group_id = $1`, groupID).Scan(&count, &sole); err != nil {
		return ""
	}
	if count != 1 || sole == nil || strings.TrimSpace(*sole) == "" {
		return ""
	}
	return strings.TrimSpace(*sole)
}

func validatedAssignee(ctx context.Context, q scriptQuerier, groupID, assigneeID string) string {
	if groupID == "" || assigneeID == "" {
		return ""
	}
	var member bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM _group_membership
			WHERE group_id = $1 AND user_id = $2::text
		)`, groupID, assigneeID).Scan(&member); err != nil {
		return ""
	}
	if !member {
		log.Printf("task engine: default assignee %s is not a member of group %s; instance left unassigned", assigneeID, groupID)
		return ""
	}
	return assigneeID
}

// readTaskVariables returns a task's scope as the map the engine evaluates
// against. Storage is one row per name (#385); this is the only shape the rest
// of the engine sees.
func readTaskVariables(ctx context.Context, q scriptQuerier, taskID string) (map[string]any, error) {
	vars, err := LoadTaskVars(ctx, q, taskID)
	if err != nil {
		return nil, err
	}
	return vars.Values, nil
}

func getDefinitionIDByTaskID(ctx context.Context, q scriptQuerier, taskID string) (string, error) {
	var defID *string
	if err := q.QueryRow(ctx, `SELECT definition_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&defID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if defID == nil {
		return "", nil
	}
	return *defID, nil
}

// resolveScopeRef resolves a scope reference against a variables map:
// "$inputs.x", "$locals.x", "$outputs.<slug>.x" (any depth of map walking),
// or a plain key lookup. Non-$ values are literals and pass through
// unchanged. Unresolvable refs return ok=false; callers whose scope is
// complete should use resolveScopeRefStrict instead.
func resolveScopeRef(scope map[string]any, ref any) (any, bool) {
	s, ok := ref.(string)
	if !ok {
		return ref, true
	}
	if !strings.HasPrefix(s, "$") {
		return ref, true
	}
	parts := strings.Split(strings.TrimPrefix(s, "$"), ".")
	// A single segment is a value in scope, not a path into one: a for_each
	// element may itself be a scalar, and $item is then the whole pill.
	if len(parts) < 1 || parts[0] == "" {
		return nil, false
	}
	var cur any = scope
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// ownOutputRefName reports whether ref addresses the closing task's own
// output namespace, "$outputs.<name>", rather than a child scope
// ("$outputs.<slug>.<field>"). Used to tell an optional output a worker left
// blank from a cross-task reference the engine failed to publish.
func ownOutputRefName(ref any) (string, bool) {
	s, ok := ref.(string)
	if !ok || !strings.HasPrefix(s, "$") {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(s, "$"), ".")
	if len(parts) != 2 || parts[0] != "outputs" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

// resolveScopeRefStrict resolves ref like resolveScopeRef but turns a
// failure into an error naming the reference and the consumer, so a mapping
// that can never bind is visible instead of dropping the input silently.
func resolveScopeRefStrict(scope map[string]any, ref any, consumer string) (any, error) {
	v, ok := resolveScopeRef(scope, ref)
	if ok {
		return v, nil
	}
	s, _ := ref.(string)
	return nil, fmt.Errorf("unresolved scope reference %q for %s", s, consumer)
}

// SpawnTaskInstanceBySlug resolves a definition by slug and spawns it.
// HTTP-facing entry point (issue #224): callers address definitions by
// slug, not opaque id.
func SpawnTaskInstanceBySlug(ctx context.Context, q scriptQuerier, slug, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
	var id string
	err := q.QueryRow(ctx, `
		SELECT _id::text FROM base_task_definition
		WHERE slug = $1 AND _deleted_at IS NULL`, slug).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("resolve task definition by slug %q: %w", slug, err)
	}
	return SpawnTaskInstance(ctx, q, id, parentTaskID, requestedBy, inputs)
}

// TaskInstanceSummary describes a spawned task for API responses.
type TaskInstanceSummary struct {
	ID        string `json:"task_id"`
	Number    string `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	WorkType  string `json:"work_type"`
	BlockType string `json:"block_type,omitempty"`
}

// DescribeTaskInstance returns the spawned task plus its direct children,
// so callers can see the whole instance graph that came into existence.
func DescribeTaskInstance(ctx context.Context, q scriptQuerier, taskID string) (*TaskInstanceSummary, []TaskInstanceSummary, error) {
	var root TaskInstanceSummary
	err := q.QueryRow(ctx, `
		SELECT _id::text, number, title, state, COALESCE(work_type, 'TASK') FROM base_task
		WHERE _id = $1 AND _deleted_at IS NULL`, taskID).Scan(
		&root.ID, &root.Number, &root.Title, &root.State, &root.WorkType)
	if err != nil {
		return nil, nil, fmt.Errorf("load spawned task: %w", err)
	}
	rows, err := q.Query(ctx, `
		SELECT _id::text, number, title, state, COALESCE(work_type, 'TASK') FROM base_task
		WHERE parent_task_id = $1 AND _deleted_at IS NULL
		ORDER BY number`, taskID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	children := []TaskInstanceSummary{}
	for rows.Next() {
		var c TaskInstanceSummary
		if err := rows.Scan(&c.ID, &c.Number, &c.Title, &c.State, &c.WorkType); err != nil {
			return nil, nil, err
		}
		children = append(children, c)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	// The block type is the only thing these summaries read from the scope,
	// so it is loaded once for the whole instance rather than per row.
	ids := make([]string, 0, len(children)+1)
	ids = append(ids, root.ID)
	for _, child := range children {
		ids = append(ids, child.ID)
	}
	scopes, err := loadTaskVarsFor(ctx, q, ids)
	if err != nil {
		return nil, nil, err
	}
	root.BlockType = summaryBlockType(scopes[root.ID])
	for index := range children {
		children[index].BlockType = summaryBlockType(scopes[children[index].ID])
	}
	return &root, children, nil
}

// summaryBlockType reports the control shape a run row was spawned from, so
// a caller can tell structure from work (AC-43).
func summaryBlockType(vars map[string]any) string {
	if len(vars) == 0 {
		return ""
	}
	_, _, _, typ, _, _, ok := blockFromVars(vars)
	if !ok {
		return ""
	}
	return typ
}

// runStartHooksBeforeClose runs the start half of the engine for a task that
// is being closed from ready or pending — the case where nobody pressed Start.
//
// Only script work needs this: a person's task can legitimately be closed
// without being started (they did the work elsewhere), but a script task's
// output IS its returned object, and skipping the script publishes nothing.
func runStartHooksBeforeClose(ctx context.Context, tx pgx.Tx, taskID, actorUserID string) error {
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM base_task WHERE _id = $1 AND _deleted_at IS NULL`, taskID).
		Scan(&state); err != nil {
		return fmt.Errorf("load task: %w", err)
	}
	if state != taskStateReady && state != taskStatePending {
		return nil
	}
	defID, err := getDefinitionIDByTaskID(ctx, tx, taskID)
	if err != nil || defID == "" {
		return err
	}
	def, err := getTaskDefinition(ctx, tx, defID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(def.ScriptText) == "" {
		return nil // work task: nothing to run
	}
	return runTaskHooks(ctx, tx, taskID, actorUserID)
}
