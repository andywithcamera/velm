package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"velm/internal/push"
)

// The push outcome columns exist so "subscribed but the phone is silent" is
// answerable from /api/push/status instead of only from server logs (#344).
// This proves the migration applied and that a recorded rejection is kept.
func TestPushDeliveryOutcomeIsRecorded(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping real-Postgres integration test")
	}
	t.Setenv("DATABASE_URL", url)
	os.Setenv("DATABASE_URL", url)
	if err := ConnectToDB(); err != nil {
		t.Fatalf("ConnectToDB: %v", err)
	}
	t.Cleanup(CloseDB)

	ctx := context.Background()
	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	var userID string
	if err := Pool.QueryRow(ctx, `
		INSERT INTO _user (name, email, password_hash)
		VALUES ($1, $2, 'x') RETURNING _id`,
		"push test", suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = Pool.Exec(context.Background(), `DELETE FROM _user WHERE _id = $1`, userID)
	})

	endpoint := "https://web.push.apple.com/" + suffix
	if err := UpsertPushSubscription(ctx, PushSubscriptionUpsert{
		UserID: userID, Endpoint: endpoint, P256DH: "k", Auth: "a", UserAgent: "test",
	}); err != nil {
		t.Fatalf("UpsertPushSubscription: %v", err)
	}

	if err := RecordPushDeliveries(ctx, []push.Delivery{
		{Endpoint: endpoint, Status: 403, Err: "VapidPkHashMismatch"},
	}); err != nil {
		t.Fatalf("RecordPushDeliveries: %v", err)
	}

	states, err := ListPushSubscriptionStates(ctx, userID)
	if err != nil {
		t.Fatalf("ListPushSubscriptionStates: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(states))
	}
	s := states[0]
	if s.LastStatus == nil || *s.LastStatus != 403 {
		t.Fatalf("LastStatus = %v, want 403", s.LastStatus)
	}
	if s.LastError != "VapidPkHashMismatch" {
		t.Fatalf("LastError = %q", s.LastError)
	}
	if s.FailureCount != 1 {
		t.Fatalf("FailureCount = %d, want 1", s.FailureCount)
	}
	if s.LastFailure == nil {
		t.Fatal("LastFailure not recorded")
	}

	// A later accepted send must clear the error and refresh last_success.
	if err := RecordPushDeliveries(ctx, []push.Delivery{{Endpoint: endpoint, Status: 201}}); err != nil {
		t.Fatalf("RecordPushDeliveries: %v", err)
	}
	states, err = ListPushSubscriptionStates(ctx, userID)
	if err != nil {
		t.Fatalf("ListPushSubscriptionStates: %v", err)
	}
	s = states[0]
	if s.LastStatus == nil || *s.LastStatus != 201 {
		t.Fatalf("LastStatus after success = %v, want 201", s.LastStatus)
	}
	if s.LastError != "" {
		t.Fatalf("LastError after success = %q, want empty", s.LastError)
	}
	if s.FailureCount != 1 {
		t.Fatalf("FailureCount = %d, want the history kept at 1", s.FailureCount)
	}
	if time.Since(s.LastSuccess) > time.Minute {
		t.Fatalf("LastSuccess not refreshed: %v", s.LastSuccess)
	}
}
