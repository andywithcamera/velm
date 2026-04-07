package main

import (
	"context"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"velm/internal/db"
)

type formTimelineItem struct {
	Kind        string
	Timestamp   string
	Actor       string
	ActorMeta   string
	Summary     string
	CommentBody string
	Changes     []formTimelineChange
	createdAt   time.Time
}

type formTimelineChange struct {
	Label    string
	OldValue string
	NewValue string
}

func loadFormTimeline(ctx context.Context, tableName, recordID string, columns []db.Column) []formTimelineItem {
	changeEntries, err := db.ListRecordChangeEntries(ctx, tableName, recordID, 80)
	if err != nil {
		log.Printf("failed to load record change timeline table=%s id=%s err=%v", tableName, recordID, err)
	}

	commentEntries, err := db.ListRecordCommentEntries(ctx, tableName, recordID, 80)
	if err != nil {
		log.Printf("failed to load record comments table=%s id=%s err=%v", tableName, recordID, err)
	}

	return buildFormTimelineItems(changeEntries, commentEntries, timelineColumnLabels(columns))
}

func buildFormTimelineItems(changeEntries []db.RecordChangeEntry, commentEntries []db.RecordCommentEntry, columnLabels map[string]string) []formTimelineItem {
	items := make([]formTimelineItem, 0, len(changeEntries)+len(commentEntries))

	for _, entry := range changeEntries {
		changes := make([]formTimelineChange, 0, len(entry.FieldDiff))
		for fieldName, diff := range entry.FieldDiff {
			label := strings.TrimSpace(columnLabels[fieldName])
			if label == "" {
				label = humanizeTimelineIdentifier(fieldName)
			}
			changes = append(changes, formTimelineChange{
				Label:    label,
				OldValue: summarizeTimelineValue(diff.Old),
				NewValue: summarizeTimelineValue(diff.New),
			})
		}
		sort.Slice(changes, func(i, j int) bool {
			return changes[i].Label < changes[j].Label
		})

		items = append(items, formTimelineItem{
			Kind:      "update",
			Timestamp: formatTimelineTimestamp(entry.CreatedAt),
			Actor:     timelineActor(entry.UserName, entry.UserEmail, entry.UserID),
			ActorMeta: timelineActorMeta(entry.UserName, entry.UserEmail, entry.UserID),
			Summary:   timelineOperationSummary(entry.Operation, len(changes)),
			Changes:   changes,
			createdAt: entry.CreatedAt,
		})
	}

	for _, entry := range commentEntries {
		items = append(items, formTimelineItem{
			Kind:        "comment",
			Timestamp:   formatTimelineTimestamp(entry.CreatedAt),
			Actor:       timelineActor(entry.UserName, entry.UserEmail, entry.UserID),
			ActorMeta:   timelineActorMeta(entry.UserName, entry.UserEmail, entry.UserID),
			Summary:     "commented on this record",
			CommentBody: strings.TrimSpace(entry.Body),
			createdAt:   entry.CreatedAt,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].createdAt.After(items[j].createdAt)
	})

	return items
}

func timelineColumnLabels(columns []db.Column) map[string]string {
	labels := make(map[string]string, len(columns))
	for _, column := range columns {
		if strings.TrimSpace(column.NAME) == "" || strings.HasPrefix(column.NAME, "_") {
			continue
		}
		label := strings.TrimSpace(column.LABEL)
		if label == "" {
			label = humanizeTimelineIdentifier(column.NAME)
		}
		labels[column.NAME] = label
	}
	return labels
}

func timelineOperationSummary(operation string, changeCount int) string {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "insert":
		return "created this record"
	case "delete":
		return "deleted this record"
	case "restore":
		return "restored this record"
	case "update":
		if changeCount == 1 {
			return "updated 1 field"
		}
		if changeCount > 1 {
			return "updated " + strconv.Itoa(changeCount) + " fields"
		}
		return "updated this record"
	default:
		if changeCount == 1 {
			return "changed 1 field"
		}
		if changeCount > 1 {
			return "changed " + strconv.Itoa(changeCount) + " fields"
		}
		return "updated this record"
	}
}

func timelineActor(name, email, userID string) string {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	userID = strings.TrimSpace(userID)
	switch {
	case name != "":
		return name
	case email != "":
		return email
	case userID != "":
		return userID
	default:
		return "System"
	}
}

func timelineActorMeta(name, email, userID string) string {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	userID = strings.TrimSpace(userID)
	switch {
	case name != "" && email != "":
		return email
	case email != "":
		return ""
	case userID != "":
		return userID
	default:
		return ""
	}
}

func summarizeTimelineValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Empty"
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\n", " ")
	if len(value) > 120 {
		return value[:117] + "..."
	}
	return value
}

func formatTimelineTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("2006-01-02 15:04:05 MST")
}

func humanizeTimelineIdentifier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "_")
	for index, part := range parts {
		if part == "" {
			continue
		}
		parts[index] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}
