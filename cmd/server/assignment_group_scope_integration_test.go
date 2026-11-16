package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"velm/internal/db"
)

// Plan T2 (Requirements AC-93): a task's Assigned To picker offers the members
// of that task's assignment group and nobody else. The picker is the reference
// lookup endpoint, so this drives the real handler rather than the query helper,
// and asserts the id set the browser would receive.
func TestAssignedToOptionsScopeToTaskGroup(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}
	t.Setenv("DATABASE_URL", databaseURL)
	os.Setenv("DATABASE_URL", databaseURL)
	previousPool := db.Pool
	if previousPool == nil {
		if err := db.ConnectToDB(); err != nil {
			t.Fatalf("ConnectToDB: %v", err)
		}
		// Restore the package's pre-test state: a pool left open here would
		// change the behaviour of later tests that read db.Pool and skip the
		// database when it is nil. Pool is global, so both halves matter.
		t.Cleanup(func() {
			db.CloseDB()
			db.Pool = nil
		})
	}

	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	// Every fixture user carries the same searchable prefix, so an unscoped
	// lookup returns all seven and only the group scope can narrow it to three.
	prefix := "assign-scope-" + suffix

	var groupID string
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO _group (name, description, is_system)
		VALUES ($1, 'assigned-to scope test', FALSE)
		RETURNING _id::text`, "assign-scope-group-"+suffix).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	newUser := func(label string) string {
		var id string
		if err := db.Pool.QueryRow(ctx, `
			INSERT INTO _user (name, email, password_hash)
			VALUES ($1, $2, '$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy')
			RETURNING _id::text`, label, label+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", label, err)
		}
		return id
	}

	members := map[string]bool{}
	for i := 0; i < 3; i++ {
		label := fmt.Sprintf("%s-member-%d", prefix, i)
		id := newUser(label)
		if _, err := db.Pool.Exec(ctx, `INSERT INTO _group_membership (user_id, group_id) VALUES ($1, $2)`, id, groupID); err != nil {
			t.Fatalf("seed membership: %v", err)
		}
		members[id] = true
	}

	outsiders := map[string]bool{}
	for i := 0; i < 4; i++ {
		outsiders[newUser(fmt.Sprintf("%s-outsider-%d", prefix, i))] = true
	}

	t.Cleanup(func() {
		bg := context.Background()
		for id := range members {
			_, _ = db.Pool.Exec(bg, `DELETE FROM _group_membership WHERE user_id = $1`, id)
		}
		for id := range members {
			_, _ = db.Pool.Exec(bg, `DELETE FROM _user WHERE _id = $1`, id)
		}
		for id := range outsiders {
			_, _ = db.Pool.Exec(bg, `DELETE FROM _user WHERE _id = $1`, id)
		}
		_, _ = db.Pool.Exec(bg, `DELETE FROM _group WHERE _id = $1`, groupID)
	})

	// The scope comes from a real task, not from the group id directly: the
	// picker is handed the task's own assignment_group_id, so the test reads
	// it back from the row the way the form does.
	var taskID string
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO base_task (title, state, assignment_group_id)
		VALUES ('Assigned To scope probe', 'pending', $1)
		RETURNING _id::text`, groupID).Scan(&taskID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	var taskGroupID string
	if err := db.Pool.QueryRow(ctx, `
		SELECT assignment_group_id::text FROM base_task WHERE _id = $1`, taskID).Scan(&taskGroupID); err != nil {
		t.Fatalf("read task group: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM base_task WHERE _id = $1`, taskID)
	})

	lookup := func(rawQuery string) []formReferenceOption {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/reference-options?"+rawQuery, nil)
		rec := httptest.NewRecorder()
		handleReferenceLookup(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("reference options status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var payload struct {
			Options []formReferenceOption `json:"options"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatalf("decode reference options: %v", err)
		}
		return payload.Options
	}

	// Control: without the scope the search reaches all seven users, so the
	// narrowed result below is the group's doing rather than the query string's.
	unscoped := lookup(url.Values{
		"table": {"_user"},
		"q":     {prefix},
		"limit": {"200"},
	}.Encode())
	if len(unscoped) != 7 {
		t.Fatalf("unscoped lookup returned %d options, want 7: %#v", len(unscoped), unscoped)
	}

	scoped := lookup(url.Values{
		"table":    {"_user"},
		"q":        {prefix},
		"group_id": {taskGroupID},
		"limit":    {"200"},
	}.Encode())

	if len(scoped) != 3 {
		t.Fatalf("scoped lookup returned %d options, want the group's 3 members: %#v", len(scoped), scoped)
	}
	seen := map[string]bool{}
	for _, option := range scoped {
		if !members[option.Value] {
			t.Fatalf("option %q is not a member of the task's group", option.Value)
		}
		if outsiders[option.Value] {
			t.Fatalf("option %q is a non-member and must not be offered", option.Value)
		}
		if seen[option.Value] {
			t.Fatalf("option %q returned twice", option.Value)
		}
		seen[option.Value] = true
	}
	for id := range members {
		if !seen[id] {
			t.Fatalf("group member %s missing from the Assigned To options", id)
		}
	}

	// Every returned id must resolve to a membership row for this group, read
	// from the table the scope claims to use.
	for id := range seen {
		var isMember bool
		if err := db.Pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM _group_membership WHERE group_id = $1 AND user_id = $2)`,
			groupID, id).Scan(&isMember); err != nil {
			t.Fatalf("verify membership of %s: %v", id, err)
		}
		if !isMember {
			t.Fatalf("%s is not in _group_membership for the task's group", id)
		}
	}
}
