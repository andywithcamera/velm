package db

// Assignment strategies, resolved once at spawn (Process Designer, Plan T5 /
// Requirements §3).
//
// A definition names the group its work lands in and the strategy that picks a
// person from it; a block may override either (Design Vision §7). Resolution
// happens here, inside the spawn transaction, and the result is written into
// base_task.assigned_user_id. Nothing re-resolves afterwards: a dispatcher who
// reassigns a task changes that task, not the policy (AC-64).

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
)

// The six strategies of Design Vision §7, plus NULL meaning "today's
// behaviour" (AC-65). 098's CHECK constraints pin this vocabulary on both
// base_task_definition and base_task_definition_child (AC-46).
const (
	AssignmentUnassigned   = "unassigned"
	AssignmentNamedUser    = "named_user"
	AssignmentRequester    = "requester"
	AssignmentFromInput    = "from_input"
	AssignmentRandomMember = "random_member"
	AssignmentRoundRobin   = "round_robin"
)

// AssignmentStrategies is the vocabulary in the order the editor offers it.
var AssignmentStrategies = []string{
	AssignmentUnassigned,
	AssignmentNamedUser,
	AssignmentRequester,
	AssignmentFromInput,
	AssignmentRandomMember,
	AssignmentRoundRobin,
}

// IsAssignmentStrategy reports whether value is one of the six strategies.
func IsAssignmentStrategy(value string) bool {
	for _, s := range AssignmentStrategies {
		if s == value {
			return true
		}
	}
	return false
}

// assignmentSpec is everything resolution needs, already merged from the
// definition and the block override.
type assignmentSpec struct {
	GroupID     string
	Strategy    string
	NamedUserID string
	// InputName is the user-typed input a from_input strategy reads. It comes
	// from the block's config (`user_input`), the only place the document can
	// name it.
	InputName string
	// Label names the block in log lines and refusals. The definition's slug
	// is used when there is no block.
	Label string
}

// resolveAssignment returns the group and assignee a spawned task gets. An
// empty strategy keeps the pre-strategy behaviour exactly (AC-65).
func resolveAssignment(ctx context.Context, q scriptQuerier, spec assignmentSpec, requestedBy string, inputs map[string]any) (string, string, error) {
	groupID := strings.TrimSpace(spec.GroupID)
	if groupID == "" {
		// Nothing to resolve against. A strategy without a group is not an
		// error: the task belongs nowhere, which is what the row will say.
		return "", "", nil
	}
	strategy := strings.TrimSpace(spec.Strategy)
	if strategy == "" {
		return groupID, resolveSpawnAssignee(ctx, q, groupID, spec.NamedUserID), nil
	}

	label := strings.TrimSpace(spec.Label)
	if label == "" {
		label = "task"
	}

	switch strategy {
	case AssignmentUnassigned:
		// The group's queue at Ready, nobody's desk. Deliberately bypasses
		// the legacy sole-member rule (AC-50).
		return groupID, "", nil

	case AssignmentNamedUser:
		return groupID, validatedAssignee(ctx, q, groupID, spec.NamedUserID), nil

	case AssignmentRequester:
		if strings.TrimSpace(requestedBy) == "" {
			log.Printf("task engine: block %q: requester strategy but the spawn names no requester; instance left unassigned", label)
			return groupID, "", nil
		}
		if id := validatedAssignee(ctx, q, groupID, requestedBy); id != "" {
			return groupID, id, nil
		}
		log.Printf("task engine: block %q: requester %s is not a member of group %s; instance left unassigned", label, requestedBy, groupID)
		return groupID, "", nil

	case AssignmentFromInput:
		name := strings.TrimSpace(spec.InputName)
		if name == "" {
			return "", "", fmt.Errorf("block %q: from_input needs the name of a user input (config.user_input)", label)
		}
		value := inputUserID(inputs, name)
		if value == "" {
			return "", "", fmt.Errorf("block %q: from_input input %q is not set to a user id", label, name)
		}
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM _user WHERE _id::text = $1)`, value).Scan(&exists); err != nil {
			return "", "", fmt.Errorf("block %q: from_input could not verify user %q: %w", label, name, err)
		}
		if !exists {
			return "", "", fmt.Errorf("block %q: from_input input %q = %q is not an existing user", label, name, value)
		}
		if id := validatedAssignee(ctx, q, groupID, value); id == "" {
			return "", "", fmt.Errorf("block %q: from_input input %q = %q is not a member of the assignment group", label, name, value)
		}
		return groupID, value, nil

	case AssignmentRandomMember:
		members, err := groupMemberIDs(ctx, q, groupID)
		if err != nil {
			return "", "", fmt.Errorf("block %q: %w", label, err)
		}
		switch len(members) {
		case 0:
			return groupID, "", nil
		case 1:
			return groupID, members[0], nil
		default:
			return groupID, members[rand.IntN(len(members))], nil
		}

	case AssignmentRoundRobin:
		members, err := groupMemberIDs(ctx, q, groupID)
		if err != nil {
			return "", "", fmt.Errorf("block %q: %w", label, err)
		}
		if len(members) == 0 {
			return groupID, "", nil
		}
		next, err := nextRoundRobinIndex(ctx, q, groupID)
		if err != nil {
			return "", "", fmt.Errorf("block %q: %w", label, err)
		}
		return groupID, members[int(next%int64(len(members)))], nil

	default:
		return "", "", fmt.Errorf("block %q: unknown assignment strategy %q", label, strategy)
	}
}

// groupMemberIDs returns the group's members in a stable order. Ordering by
// user id is what makes round_robin's sequence reproducible (AC-60).
func groupMemberIDs(ctx context.Context, q scriptQuerier, groupID string) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT user_id FROM _group_membership
		WHERE group_id = $1 AND BTRIM(user_id) <> ''
		ORDER BY user_id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group %s members: %w", groupID, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, strings.TrimSpace(id))
	}
	return out, rows.Err()
}

// nextRoundRobinIndex takes the cursor's current index and advances it by one,
// both inside the caller's transaction. The row lock is the serialisation
// point: concurrent spawns queue here, so no two read the same index (AC-61).
// The cursor is monotonic, not reset, so next_index % member_count is the
// member position (AC-63).
func nextRoundRobinIndex(ctx context.Context, q scriptQuerier, groupID string) (int64, error) {
	if _, err := q.Exec(ctx, `
		INSERT INTO base_assignment_cursor (group_id, next_index)
		VALUES ($1, 0)
		ON CONFLICT (group_id) DO NOTHING`, groupID); err != nil {
		return 0, fmt.Errorf("create assignment cursor for group %s: %w", groupID, err)
	}
	var next int64
	if err := q.QueryRow(ctx, `
		SELECT next_index FROM base_assignment_cursor
		WHERE group_id = $1 FOR UPDATE`, groupID).Scan(&next); err != nil {
		return 0, fmt.Errorf("read assignment cursor for group %s: %w", groupID, err)
	}
	if _, err := q.Exec(ctx, `
		UPDATE base_assignment_cursor
		SET next_index = next_index + 1, _updated_at = NOW()
		WHERE group_id = $1`, groupID); err != nil {
		return 0, fmt.Errorf("advance assignment cursor for group %s: %w", groupID, err)
	}
	return next, nil
}

// inputUserID reads a user id from the run's inputs. Only a string names a
// user; a number or object is a misconfigured input, not a user id.
func inputUserID(inputs map[string]any, name string) string {
	if inputs == nil {
		return ""
	}
	value, ok := inputs[name]
	if !ok {
		return ""
	}
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// GroupExists reports whether groupID names a real group. The editor uses it
// to refuse a hand-written group id rather than letting the FK produce a 500.
func GroupExists(ctx context.Context, groupID string) (bool, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return false, nil
	}
	var exists bool
	if err := Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM _group WHERE _id::text = $1)`, groupID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}
