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

// State codes on the real model (085): pending/ready/in_progress/closed,
// stored in the text `state` column and guarded by chk_base_task_state.
// 'skipped' is an engine-internal terminal for run_if evaluating false; it
// is not offered in the UI. The design doc (rev 3) has children spawn in
// PENDING; the engine promotes to ready (same run_if gate:
// ready/pending -> in_progress, false -> skipped).
const (
	taskStatePending    = "pending"
	taskStateReady      = "ready"
	taskStateInProgress = "in_progress"
	taskStateSkipped    = "skipped"
	taskStateDone       = "closed"
)

// taskTransitions is the engine's allowed-edge set, validated in Go
// against the text state column (077 removed base_task_transition).
var taskTransitions = map[string]map[string]bool{
	"ready":       {"ready": true, "in_progress": true, "closed": true, "skipped": true},
	"in_progress": {"in_progress": true, "pending": true, "skipped": true, "closed": true},
	"pending":     {"pending": true, "ready": true, "in_progress": true, "skipped": true},
	"closed":      {},
	"skipped":     {},
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
	SpawnRules        []TaskSpawnRule
	IsActive          bool
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
			COALESCE(default_assignee_id::text, '')
		FROM base_task_definition WHERE _id = $1 AND _deleted_at IS NULL`, id).Scan(
		&d.ID, &d.Slug, &d.DisplayName, &d.Description, &d.IsContainer, &d.ScriptText, &d.RunIfScript,
		&groupID, &spawnRulesJSON, &d.IsActive, &d.DefaultAssigneeID)
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

func getTaskDefinitionChildren(ctx context.Context, q scriptQuerier, containerID string) ([]childSpec, error) {
	rows, err := q.Query(ctx, `
		SELECT child_definition_id::text, input_mapping, depends_on, depends_on_reason
		FROM base_task_definition_child
		WHERE container_definition_id = $1
		ORDER BY sort_order, _created_at`, containerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var specs []childSpec
	for rows.Next() {
		var spec childSpec
		var deps []string
		if err := rows.Scan(&spec.ChildDefinitionID, &spec.InputMapping, &deps, &spec.DependsOnReason); err != nil {
			return nil, err
		}
		spec.DependsOn = deps
		specs = append(specs, spec)
	}
	return specs, rows.Err()
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
	vars := map[string]any{"inputs": inputs, "locals": map[string]any{}, "outputs": map[string]any{}}
	varsJSON, err := json.Marshal(vars)
	if err != nil {
		return "", err
	}
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
	assigneeID = resolveSpawnAssignee(ctx, q, groupID, assigneeID)

	state := spawnStateFor(def, parentTaskID)
	if route != nil && route.Ready {
		state = taskStateReady
	}

	var taskID string
	var taskNumber string
	title := instanceTitle(ctx, q, def, parentTaskID)
	// Instance fields: the definition's own description and the request's
	// priority/planned start are known at spawn time, so the doer's form is
	// populated from the first render instead of being blank until someone
	// types into it.
	description := nullable(instanceDescription(def, inputs))
	priority := instancePriority(inputs)
	plannedStart := instancePlannedStart(inputs)
	err = q.QueryRow(ctx, `
		INSERT INTO base_task (number, title, work_type, state, priority, parent_task_id,
			assignment_group_id, assigned_user_id, requested_by_user_id, definition_id, variables,
			description, planned_start_at)
		VALUES (
			'', -- assigned by the autonumber trigger (051); base_task_number_seq was dropped there
			$1, 'TASK',
			$2,
			$3, $4, $5, $6, $7, $8, $9::jsonb,
			$10, $11)
		RETURNING _id, number`,
		title, state, priority, nullable(parentTaskID), nullable(groupID),
		nullable(assigneeID), nullable(requestedBy), def.ID, string(varsJSON),
		description, plannedStart).Scan(&taskID, &taskNumber)
	if err != nil {
		return "", fmt.Errorf("spawn task: %w", err)
	}

	// Identity (#272): every run of a definition used to carry the same
	// title, so a board of 28 "Feature Request" cards was unreadable. The
	// number is assigned by the before-write trigger, so it is appended
	// after the insert rather than composed into the value above.
	if _, err := q.Exec(ctx, `UPDATE base_task SET title = $2 WHERE _id = $1`,
		taskID, composeInstanceTitle(title, taskNumber)); err != nil {
		return "", fmt.Errorf("spawn task title: %w", err)
	}

	if def.IsContainer {
		children, err := getTaskDefinitionChildren(ctx, q, def.ID)
		if err != nil {
			return "", err
		}
		for _, child := range children {
			// Children spawn with empty inputs; the container opening binds
			// them from its scope via input_mapping.
			if _, err := spawnTaskForDefinitionByID(ctx, q, child.ChildDefinitionID, taskID, requestedBy, map[string]any{}); err != nil {
				return "", err
			}
		}
		if state == taskStateReady {
			if err := openContainer(ctx, q, taskID, requestedBy, 0); err != nil {
				return "", err
			}
		}
	}
	return taskID, nil
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

func spawnTaskForDefinitionByID(ctx context.Context, q scriptQuerier, defID, parentTaskID, requestedBy string, inputs map[string]any) (string, error) {
	def, err := getTaskDefinition(ctx, q, defID)
	if err != nil {
		return "", err
	}
	return spawnTaskForDefinition(ctx, q, def, parentTaskID, requestedBy, inputs)
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
	defID, err := getDefinitionIDByTaskID(ctx, tx, parentTaskID)
	if err != nil || defID == "" {
		return err
	}
	children, err := getTaskDefinitionChildren(ctx, tx, defID)
	if err != nil {
		return err
	}
	if len(children) == 0 {
		return nil
	}
	parentVars, err := readTaskVariables(ctx, tx, parentTaskID)
	if err != nil {
		return err
	}

	// Instance children by definition id. One definition may appear twice
	// in a container only via distinct rows, which the unique constraint
	// forbids — so the mapping is 1:1 in practice.
	rows, err := tx.Query(ctx, `
		SELECT _id::text, definition_id::text, variables
		FROM base_task
		WHERE parent_task_id = $1 AND _deleted_at IS NULL`, parentTaskID)
	if err != nil {
		return err
	}
	defer rows.Close()
	childTasks := map[string]string{} // definition id -> task id
	childVars := map[string]map[string]any{}
	for rows.Next() {
		var id, cid string
		var vj []byte
		if err := rows.Scan(&id, &cid, &vj); err != nil {
			return err
		}
		vars := map[string]any{}
		_ = json.Unmarshal(vj, &vars)
		childTasks[cid] = id
		childVars[id] = vars
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, spec := range children {
		taskID, ok := childTasks[spec.ChildDefinitionID]
		if !ok {
			continue
		}
		inputs, _ := childVars[taskID]["inputs"].(map[string]any)
		if inputs == nil {
			inputs = map[string]any{}
		}
		for childInput, source := range spec.InputMapping {
			val, ok := resolveScopeRef(parentVars, source)
			if !ok {
				log.Printf("task engine: input_mapping %q -> %v unresolvable against parent scope, omitted (task %s)", childInput, source, taskID)
				continue
			}
			inputs[childInput] = val
		}
		childVars[taskID]["inputs"] = inputs
		updated, err := json.Marshal(childVars[taskID])
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, taskID, string(updated)); err != nil {
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
	// Sibling instances: def id -> (state, closure reason).
	rows, err := tx.Query(ctx, `
		SELECT _id::text, definition_id::text, state, closure_reason, variables
		FROM base_task
		WHERE parent_task_id = $1 AND _deleted_at IS NULL`, parentTaskID)
	if err != nil {
		return false, err
	}
	siblings := map[string][]childInstanceState{}
	for rows.Next() {
		var st childInstanceState
		var vj []byte
		if err := rows.Scan(&st.taskID, &st.defID, &st.state, &st.reason, &vj); err != nil {
			rows.Close()
			return false, err
		}
		st.vars = map[string]any{}
		_ = json.Unmarshal(vj, &st.vars)
		siblings[st.defID] = append(siblings[st.defID], st)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()

	// Template edges for this container's definition.
	defID, err := getDefinitionIDByTaskID(ctx, tx, parentTaskID)
	if err != nil || defID == "" {
		return false, err
	}
	specs, err := getTaskDefinitionChildren(ctx, tx, defID)
	if err != nil {
		return false, err
	}
	specByChild := map[string]childSpec{}
	for _, spec := range specs {
		specByChild[spec.ChildDefinitionID] = spec
	}

	changed := false
	for cid, instances := range siblings {
		spec, hasSpec := specByChild[cid]
		if !hasSpec {
			continue
		}
		if len(spec.DependsOn) == 0 {
			// Dependency-free: promote at parent start (pending -> ready).
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
			if depsSatisfied(spec.DependsOn, spec.DependsOnReason, siblings) {
				if err := promoteInstance(ctx, tx, parentTaskID, inst, spec, actorUserID, depth); err != nil {
					return false, err
				}
				changed = true
			}
		}
	}
	return changed, nil
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
	switch inst.state {
	case taskStateSkipped:
		return true
	case taskStateDone:
		r := ""
		if inst.reason != nil {
			r = *inst.reason
		}
		if reason == "" {
			return r == "completed" || r == "cancelled"
		}
		return r == reason
	default:
		return false
	}
}

// promoteInstance moves one pending child to ready (or skipped when its
// run_if is false) and re-binds its input_mapping against the parent scope,
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

	// run_if gate at promotion, matching the engine's start semantics.
	if strings.TrimSpace(def.RunIfScript) != "" {
		allowed, evalErr := evalTaskCondition(ctx, tx, inst.taskID, def.RunIfScript, inst.vars, actorUserID)
		if evalErr != nil {
			return fmt.Errorf("run_if evaluation failed for task %s: %w", inst.taskID, evalErr)
		}
		if !allowed {
			return transitionTaskInTx(ctx, tx, inst.taskID, taskStateSkipped)
		}
	}

	if err := transitionTaskInTx(ctx, tx, inst.taskID, taskStateReady); err != nil {
		return err
	}
	updated, err := json.Marshal(inst.vars)
	if err != nil {
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
		SET variables = $2::jsonb,
			description = CASE WHEN $3 THEN $4 ELSE description END
		WHERE _id = $1`, inst.taskID, string(updated), refreshDescription, description)
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
			return transitionTaskInTx(ctx, tx, taskID, taskStateSkipped)
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

	vars, err := readTaskVariables(ctx, q, taskID)
	if err != nil {
		return err
	}
	outputs, _ := vars["outputs"].(map[string]any)
	if outputs == nil {
		outputs = map[string]any{}
	}
	for name, value := range returnedOutputs {
		outputs[name] = value
	}
	vars["outputs"] = outputs
	updated, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, taskID, string(updated))
	return err
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
	if stateAfterHooks == taskStateSkipped || stateAfterHooks == taskStateDone {
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
func onTaskClosed(ctx context.Context, tx pgx.Tx, taskID, closureReason, actorUserID string) error {
	var parentID *string
	var varsJSON []byte
	err := tx.QueryRow(ctx,
		`SELECT parent_task_id::text, variables FROM base_task WHERE _id = $1`, taskID).Scan(&parentID, &varsJSON)
	if err != nil {
		return err
	}

	vars := map[string]any{}
	if len(varsJSON) > 0 {
		_ = json.Unmarshal(varsJSON, &vars)
	}
	outputs, _ := vars["outputs"].(map[string]any)

	// A container's declared outputs are answered by its children: fill them
	// before publishing upward so a sibling stage's mapping can bind.
	if containerYAML, isContainer, err := containerVariablesYAML(ctx, tx, taskID); err == nil && isContainer {
		if merged, mergeErr := mergeContainerOutputs(ctx, tx, taskID, vars, containerYAML); mergeErr == nil {
			outputs = merged
		}
	}

	// Publish up: parent's variables.outputs.<child slug> = this outputs.
	if parentID != nil && *parentID != "" && outputs != nil {
		var defID *string
		_ = tx.QueryRow(ctx, `SELECT definition_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&defID)
		if defID != nil && *defID != "" {
			var slug string
			if err := tx.QueryRow(ctx, `SELECT slug FROM base_task_definition WHERE _id = $1`, *defID).Scan(&slug); err == nil {
				var parentVars map[string]any
				var parentJSON []byte
				if err := tx.QueryRow(ctx, `SELECT variables FROM base_task WHERE _id = $1`, *parentID).Scan(&parentJSON); err == nil {
					parentVars = map[string]any{}
					_ = json.Unmarshal(parentJSON, &parentVars)
					parentOutputs, _ := parentVars["outputs"].(map[string]any)
					if parentOutputs == nil {
						parentOutputs = map[string]any{}
					}
					parentOutputs[slug] = outputs
					parentVars["outputs"] = parentOutputs
					updated, _ := json.Marshal(parentVars)
					_, _ = tx.Exec(ctx, `UPDATE base_task SET variables = $2::jsonb WHERE _id = $1`, *parentID, string(updated))
				}
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
func fireSpawnRules(ctx context.Context, tx pgx.Tx, taskID, closureReason, actorUserID string) error {
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
		// scope, so by now every reference it can make has resolved. A
		// mapping that still resolves to nothing is a template bug, not an
		// ordering accident: fail the close transaction rather than spawn a
		// child that silently runs without the input it was promised (#217).
		inputs := map[string]any{}
		for childInput, source := range rule.InputMapping {
			val, err := resolveScopeRefStrict(vars, source,
				fmt.Sprintf("spawn rule for %q on task %s", slug, taskID))
			if err != nil {
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
func spawnFollowOn(ctx context.Context, tx pgx.Tx, slug string, route *taskRoute, parentTaskID *string, requestedBy string, inputs map[string]any) error {
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

func readTaskVariables(ctx context.Context, q scriptQuerier, taskID string) (map[string]any, error) {
	var varsJSON []byte
	if err := q.QueryRow(ctx, `SELECT variables FROM base_task WHERE _id = $1`, taskID).Scan(&varsJSON); err != nil {
		return nil, err
	}
	vars := map[string]any{}
	if len(varsJSON) > 0 {
		_ = json.Unmarshal(varsJSON, &vars)
	}
	return vars, nil
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
	if len(parts) < 2 {
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
	ID     string `json:"task_id"`
	Number string `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
}

// DescribeTaskInstance returns the spawned task plus its direct children,
// so callers can see the whole instance graph that came into existence.
func DescribeTaskInstance(ctx context.Context, q scriptQuerier, taskID string) (*TaskInstanceSummary, []TaskInstanceSummary, error) {
	var root TaskInstanceSummary
	err := q.QueryRow(ctx, `
		SELECT _id::text, number, title, state FROM base_task
		WHERE _id = $1 AND _deleted_at IS NULL`, taskID).Scan(
		&root.ID, &root.Number, &root.Title, &root.State)
	if err != nil {
		return nil, nil, fmt.Errorf("load spawned task: %w", err)
	}
	rows, err := q.Query(ctx, `
		SELECT _id::text, number, title, state FROM base_task
		WHERE parent_task_id = $1 AND _deleted_at IS NULL
		ORDER BY number`, taskID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	children := []TaskInstanceSummary{}
	for rows.Next() {
		var c TaskInstanceSummary
		if err := rows.Scan(&c.ID, &c.Number, &c.Title, &c.State); err != nil {
			return nil, nil, err
		}
		children = append(children, c)
	}
	return &root, children, rows.Err()
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
