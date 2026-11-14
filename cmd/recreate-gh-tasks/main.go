package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"velm/internal/db"
)

// Re-creates the flat GitHub-issue tasks as definition instances (#226) and
// closes the flat twins with a pointer. Run once per population; re-running
// is safe and creates nothing twice.
//
// Usage:
//
//	recreate-gh-tasks -slug feature_request -requested-by you@example.com issues.json
//
// issues.json is a GitHub issues array: [{"number":226,"title":"…","body":"…","url":"…"}].
func main() {
	slug := flag.String("slug", "", "task definition slug to spawn instances of (e.g. feature_request or defect)")
	requestedBy := flag.String("requested-by", "", "user id (uuid) recorded as the requester of every spawned instance")
	flag.Parse()

	if *slug == "" || *requestedBy == "" || flag.NArg() != 1 {
		log.Fatal("usage: recreate-gh-tasks -slug <definition-slug> -requested-by <user uuid> issues.json")
	}

	raw, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	var issues []db.GitHubIssue
	if err := json.Unmarshal(raw, &issues); err != nil {
		log.Fatalf("decode %s: %v", flag.Arg(0), err)
	}

	if err := db.ConnectToDB(); err != nil {
		log.Fatal(err)
	}
	defer db.CloseDB()
	ctx := context.Background()

	results, err := db.RecreateGitHubIssueTasks(ctx, issues, *slug, *requestedBy)
	if err != nil {
		log.Fatalf("recreate: %v", err)
	}

	unassigned := 0
	for _, r := range results {
		log.Printf("GH-%d %q -> %s (created: %t, closed twins: %v)",
			r.Number, r.IssueTitle, r.ReplacementNumber, r.Created, r.ClosedTwinIDs)
		// Assignment is silent in the engine: a definition with no default
		// assignee and a multi-member group yields no assignee and the spawn
		// still succeeds. Report it rather than trust the exit status.
		_, children, err := db.DescribeTaskInstance(ctx, db.Pool, r.ReplacementID)
		if err != nil {
			log.Fatalf("describe GH-%d: %v", r.Number, err)
		}
		for _, c := range children {
			var assignee string
			if err := db.Pool.QueryRow(ctx,
				`SELECT COALESCE(assigned_user_id::text, '') FROM base_task WHERE _id = $1`, c.ID).Scan(&assignee); err != nil {
				log.Fatalf("read assignee of %s: %v", c.Number, err)
			}
			log.Printf("  child %s %q assignee=%q", c.Number, c.Title, assignee)
			if assignee == "" {
				unassigned++
			}
		}
	}
	log.Printf("%d issue(s), %d child task(s) with no assignee", len(results), unassigned)
}
