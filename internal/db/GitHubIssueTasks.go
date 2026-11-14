package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Recreating the flat GitHub-issue tasks (#226).
//
// On 2026-11-08, 70 tasks were created through the web form, one per then-open
// GitHub issue. The form has no definition_id field, so those rows are flat:
// no definition, no child pipeline, no variables. Now that a root spawn
// honours inputs["title"] and the API/keypress path can spawn by slug, each
// open issue can be a real instance of a definition instead.
//
// The flat rows are closed, never deleted: they are the audit trail of what
// was worked on before the engine existed. Each carries "Superseded by
// TASK-…" in its description, because closure_reason only accepts the
// engine's four values and cannot hold a pointer (085).

// GitHubIssue is the minimum an issue needs to become a task instance:
// enough to title the root, store the URL and the body, and match the flat
// twin by number.
type GitHubIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

// GitHubIssueTaskResult reports what one issue's re-creation did, so the
// operator's run is auditable line by line rather than a bare exit status.
type GitHubIssueTaskResult struct {
	Number            int      `json:"number"`
	IssueTitle        string   `json:"issue_title"`
	ReplacementID     string   `json:"replacement_id"`
	ReplacementNumber string   `json:"replacement_number"`
	Created           bool     `json:"created"`
	ClosedTwinIDs     []string `json:"closed_twin_ids"`
}

// GitHubIssueTaskTitle is the title key shared by the flat rows and their
// replacements: "GH-<n> <issue title>". It is what makes the two matchable
// across runs.
func GitHubIssueTaskTitle(issueNumber int, issueTitle string) string {
	return fmt.Sprintf("GH-%d %s", issueNumber, stripTitleNumber(issueTitle))
}

// RecreateGitHubIssueTasks makes every issue a definition instance and
// retires its flat twin. It is re-runnable: an issue that already has a
// replacement is not spawned again, and a twin that is already closed is
// left alone.
//
// No single transaction spans the spawn and the close — SpawnTaskInstance
// and CloseTaskWithReason each open their own — so a crash between them
// leaves both rows live. Both steps therefore check current state first and
// the next run finishes the job.
func RecreateGitHubIssueTasks(ctx context.Context, issues []GitHubIssue, slug, requestedBy string) ([]GitHubIssueTaskResult, error) {
	results := make([]GitHubIssueTaskResult, 0, len(issues))
	for _, issue := range issues {
		title := GitHubIssueTaskTitle(issue.Number, issue.Title)
		result := GitHubIssueTaskResult{Number: issue.Number, IssueTitle: issue.Title}
		if strings.TrimSpace(title) == "" {
			return results, fmt.Errorf("issue %d has no usable title", issue.Number)
		}

		replacementID, replacementNumber, err := findGitHubIssueReplacement(ctx, issue.Number)
		if err != nil {
			return results, err
		}
		if replacementID == "" {
			replacementID, err = SpawnTaskInstanceBySlug(ctx, Pool, slug, "", requestedBy, map[string]any{
				"title":      title,
				"github_url": issue.URL,
				"body":       issue.Body,
			})
			if err != nil {
				return results, fmt.Errorf("spawn issue %d: %w", issue.Number, err)
			}
			result.Created = true
			if err := Pool.QueryRow(ctx, `SELECT number FROM base_task WHERE _id = $1`, replacementID).
				Scan(&replacementNumber); err != nil {
				return results, fmt.Errorf("read replacement number for issue %d: %w", issue.Number, err)
			}
		}
		result.ReplacementID = replacementID
		result.ReplacementNumber = replacementNumber

		twins, err := openFlatGitHubIssueTwins(ctx, issue.Number)
		if err != nil {
			return results, err
		}
		for _, twinID := range twins {
			// The pointer lives in the description: closure_reason is a
			// constrained enum (085) and the twin keeps its audit content.
			if _, err := Pool.Exec(ctx, `
				UPDATE base_task
				SET description = COALESCE(NULLIF(description, '') || '

', '') || $2
				WHERE _id = $1`, twinID, "Superseded by "+replacementNumber); err != nil {
				return results, fmt.Errorf("annotate flat twin of issue %d: %w", issue.Number, err)
			}
			// CloseTaskWithReason, never a bare UPDATE: the close hooks and
			// ancestor roll-up are the whole point of the engine.
			if err := CloseTaskWithReason(ctx, twinID, requestedBy, "completed"); err != nil {
				return results, fmt.Errorf("close flat twin of issue %d: %w", issue.Number, err)
			}
			result.ClosedTwinIDs = append(result.ClosedTwinIDs, twinID)
		}
		results = append(results, result)
	}
	return results, nil
}

// findGitHubIssueReplacement returns an existing non-flat instance titled
// after the issue, if one was already spawned. Children are excluded by
// parent_task_id: their title is composed from the definition, but a
// renamed root must never make a descendant look like the replacement.
func findGitHubIssueReplacement(ctx context.Context, issueNumber int) (string, string, error) {
	var id, number string
	err := Pool.QueryRow(ctx, `
		SELECT _id::text, number FROM base_task
		WHERE definition_id IS NOT NULL
			AND parent_task_id IS NULL
			AND _deleted_at IS NULL
			AND title LIKE $1
		ORDER BY _created_at
		LIMIT 1`, fmt.Sprintf("GH-%d %%", issueNumber)).Scan(&id, &number)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("find replacement for issue %d: %w", issueNumber, err)
	}
	return id, number, nil
}

// openFlatGitHubIssueTwins lists the still-open flat rows for an issue:
// definition_id NULL, the GH-<n> title the form produced, not deleted, not
// already terminal.
func openFlatGitHubIssueTwins(ctx context.Context, issueNumber int) ([]string, error) {
	rows, err := Pool.Query(ctx, `
		SELECT _id::text FROM base_task
		WHERE definition_id IS NULL
			AND parent_task_id IS NULL
			AND _deleted_at IS NULL
			AND state NOT IN ('closed', 'skipped')
			AND title LIKE $1
		ORDER BY number`, fmt.Sprintf("GH-%d %%", issueNumber))
	if err != nil {
		return nil, fmt.Errorf("find flat twins for issue %d: %w", issueNumber, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
