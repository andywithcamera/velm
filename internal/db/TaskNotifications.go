package db

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"velm/internal/utils"
)

var baseTaskNotificationIgnoredFields = map[string]bool{
	"assigned_user_id": true,
	"closed_at":        true,
	"started_at":       true,
	"state_changed_at": true,
}

func EmitBaseTaskNotifications(ctx context.Context, tableName, recordID, operation, actorUserID string, oldSnapshot, newSnapshot map[string]any) error {
	return EmitTaskNotifications(ctx, tableName, recordID, operation, actorUserID, oldSnapshot, newSnapshot)
}

func EmitTaskNotifications(ctx context.Context, tableName, recordID, operation, actorUserID string, oldSnapshot, newSnapshot map[string]any) error {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if TaskTypeValueForTable(ctx, tableName) == "" {
		return nil
	}
	if Pool == nil {
		return nil
	}

	oldValues := stringifyValueMap(oldSnapshot)
	newValues := stringifyValueMap(newSnapshot)
	if len(newValues) == 0 {
		return nil
	}

	groupMembers := []string(nil)
	if baseTaskShouldNotifyGroupQueue(strings.TrimSpace(operation), oldValues, newValues) {
		var err error
		groupMembers, err = listBaseTaskGroupMemberIDs(ctx, Pool, newValues["assignment_group_id"])
		if err != nil {
			return err
		}
	}

	inputs := planTaskNotifications(tableName, recordID, operation, actorUserID, oldValues, newValues, groupMembers)
	if len(inputs) == 0 {
		return nil
	}

	_, err := createUserNotificationsWithQuerier(ctx, Pool, inputs)
	return err
}

func planBaseTaskNotifications(recordID, operation, actorUserID string, oldValues, newValues map[string]string, groupMemberIDs []string) []UserNotificationCreateInput {
	return planTaskNotifications("base_task", recordID, operation, actorUserID, oldValues, newValues, groupMemberIDs)
}

func planTaskNotifications(tableName, recordID, operation, actorUserID string, oldValues, newValues map[string]string, groupMemberIDs []string) []UserNotificationCreateInput {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	recordID = strings.TrimSpace(recordID)
	operation = strings.ToLower(strings.TrimSpace(operation))
	actorUserID = strings.TrimSpace(actorUserID)

	inputs := make([]UserNotificationCreateInput, 0, 4)
	seen := make(map[string]bool, 4)
	appendInput := func(userID, title, body, level string) {
		userID = strings.TrimSpace(userID)
		if !utils.IsValidUUID(userID) || userID == actorUserID || seen[userID] {
			return
		}
		seen[userID] = true
		inputs = append(inputs, UserNotificationCreateInput{
			UserID:    userID,
			Title:     title,
			Body:      body,
			Href:      "/f/" + tableName + "/" + recordID,
			Level:     level,
			CreatedBy: actorUserID,
		})
	}

	taskRef := baseTaskNotificationReference(newValues, oldValues)
	newAssignedUserID := strings.TrimSpace(newValues["assigned_user_id"])
	oldAssignedUserID := strings.TrimSpace(oldValues["assigned_user_id"])

	if newAssignedUserID != "" && newAssignedUserID != oldAssignedUserID {
		appendInput(
			newAssignedUserID,
			"Task assigned to you",
			taskRef,
			"info",
		)
		return inputs
	}

	if baseTaskShouldNotifyGroupQueue(operation, oldValues, newValues) {
		for _, userID := range groupMemberIDs {
			appendInput(
				userID,
				"Unassigned task in your group",
				taskRef,
				"info",
			)
		}
		return inputs
	}

	if operation != "insert" && newAssignedUserID != "" && newAssignedUserID == oldAssignedUserID {
		if summary := summarizeBaseTaskNotificationChanges(oldValues, newValues); summary != "" {
			appendInput(
				newAssignedUserID,
				"Task updated",
				taskRef+" changed: "+summary,
				"info",
			)
		}
	}

	return inputs
}

func baseTaskShouldNotifyGroupQueue(operation string, oldValues, newValues map[string]string) bool {
	newGroupID := strings.TrimSpace(newValues["assignment_group_id"])
	newAssignedUserID := strings.TrimSpace(newValues["assigned_user_id"])
	oldGroupID := strings.TrimSpace(oldValues["assignment_group_id"])
	oldAssignedUserID := strings.TrimSpace(oldValues["assigned_user_id"])

	if newGroupID == "" || newAssignedUserID != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(operation), "insert") {
		return true
	}
	return newGroupID != oldGroupID || oldAssignedUserID != ""
}

func summarizeBaseTaskNotificationChanges(oldValues, newValues map[string]string) string {
	diff := makeFieldDiff(oldValues, newValues)
	fields := make([]string, 0, len(diff))
	for fieldName := range diff {
		if baseTaskNotificationIgnoredFields[fieldName] {
			continue
		}
		fields = append(fields, baseTaskNotificationFieldLabel(fieldName))
	}
	if len(fields) == 0 {
		return ""
	}
	sort.Strings(fields)
	if len(fields) > 3 {
		fields = fields[:3]
	}
	return strings.Join(fields, ", ")
}

func baseTaskNotificationFieldLabel(fieldName string) string {
	switch strings.TrimSpace(fieldName) {
	case "assignment_group_id":
		return "group"
	case "closure_reason":
		return "resolution"
	case "due_at":
		return "due date"
	case "priority":
		return "priority"
	case "state":
		return "state"
	case "substate":
		return "substate"
	case "title":
		return "title"
	default:
		return humanizeBaseTaskNotificationIdentifier(fieldName)
	}
}

func baseTaskNotificationReference(newValues, oldValues map[string]string) string {
	number := strings.TrimSpace(newValues["number"])
	if number == "" {
		number = strings.TrimSpace(oldValues["number"])
	}
	title := strings.TrimSpace(newValues["title"])
	if title == "" {
		title = strings.TrimSpace(oldValues["title"])
	}
	switch {
	case number != "" && title != "":
		return fmt.Sprintf("%s %s", number, title)
	case number != "":
		return number
	case title != "":
		return title
	default:
		return "Task"
	}
}

func listBaseTaskGroupMemberIDs(ctx context.Context, querier scriptQuerier, groupID string) ([]string, error) {
	groupID = strings.TrimSpace(groupID)
	if querier == nil || !utils.IsValidUUID(groupID) {
		return nil, nil
	}

	rows, err := querier.Query(ctx, `
		SELECT gm.user_id::text
		FROM _group_membership gm
		WHERE gm.group_id::text = $1
		ORDER BY gm.user_id::text
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	userIDs := make([]string, 0, 8)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, strings.TrimSpace(userID))
	}
	return userIDs, rows.Err()
}

func humanizeBaseTaskNotificationIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(value))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	return strings.Join(parts, " ")
}
