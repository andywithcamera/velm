package realtime

import (
	"testing"
	"time"
)

func TestHubPublishesRecordChangesToMatchingSubscribers(t *testing.T) {
	hub := newHub(func() time.Time {
		return time.Date(2026, 3, 16, 17, 0, 0, 0, time.UTC)
	}, 35*time.Second)

	tableCh, unsubscribeTable := hub.Subscribe("_work", "")
	defer unsubscribeTable()
	recordCh, unsubscribeRecord := hub.Subscribe("_work", "abc")
	defer unsubscribeRecord()
	otherCh, unsubscribeOther := hub.Subscribe("_item", "")
	defer unsubscribeOther()

	hub.PublishRecordChange("_work", "abc", "save", "u-1", "client-1")

	select {
	case event := <-tableCh:
		if event.Type != EventTypeRecordChanged || event.RecordID != "abc" || event.Kind != "save" || event.ActorClientID != "client-1" {
			t.Fatalf("unexpected table event: %#v", event)
		}
	default:
		t.Fatal("expected table subscriber to receive change event")
	}

	select {
	case event := <-recordCh:
		if event.Type != EventTypeRecordChanged || event.RecordID != "abc" || event.ActorClientID != "client-1" {
			t.Fatalf("unexpected record event: %#v", event)
		}
	default:
		t.Fatal("expected record subscriber to receive change event")
	}

	select {
	case event := <-otherCh:
		t.Fatalf("unexpected event for unrelated subscriber: %#v", event)
	default:
	}
}

func TestHubPresenceUpdatesAndPrunes(t *testing.T) {
	now := time.Date(2026, 3, 16, 17, 5, 0, 0, time.UTC)
	hub := newHub(func() time.Time { return now }, 30*time.Second)

	recordCh, unsubscribe := hub.Subscribe("_work", "abc")
	defer unsubscribe()

	hub.UpsertPresence("_work", "abc", "client-1", "u-1", "Andy Doyle", "editing")

	select {
	case event := <-recordCh:
		if event.Type != EventTypePresenceUpdate {
			t.Fatalf("event type = %q, want %q", event.Type, EventTypePresenceUpdate)
		}
		if len(event.Presence) != 1 || event.Presence[0].UserName != "Andy Doyle" || event.Presence[0].Status != "editing" {
			t.Fatalf("unexpected presence payload: %#v", event.Presence)
		}
	default:
		t.Fatal("expected initial presence event")
	}

	snapshot := hub.PresenceSnapshot("_work", "abc")
	if len(snapshot) != 1 || snapshot[0].UserID != "u-1" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}

	now = now.Add(31 * time.Second)
	hub.cleanupStalePresence()

	select {
	case event := <-recordCh:
		if len(event.Presence) != 0 {
			t.Fatalf("expected empty presence after prune, got %#v", event.Presence)
		}
	default:
		t.Fatal("expected prune event")
	}

	if snapshot := hub.PresenceSnapshot("_work", "abc"); len(snapshot) != 0 {
		t.Fatalf("expected no active presence after prune, got %#v", snapshot)
	}
}

func TestHubPresenceAggregatesMultipleClientsForSameUser(t *testing.T) {
	now := time.Date(2026, 3, 16, 17, 10, 0, 0, time.UTC)
	hub := newHub(func() time.Time { return now }, 30*time.Second)

	hub.UpsertPresence("_work", "abc", "client-1", "u-1", "Andy Doyle", "viewing")
	hub.UpsertPresence("_work", "abc", "client-2", "u-1", "Andy Doyle", "editing")

	snapshot := hub.PresenceSnapshot("_work", "abc")
	if len(snapshot) != 1 {
		t.Fatalf("expected one aggregated presence entry, got %#v", snapshot)
	}
	if snapshot[0].UserID != "u-1" || snapshot[0].UserName != "Andy Doyle" || snapshot[0].Status != "editing" {
		t.Fatalf("unexpected aggregated presence entry: %#v", snapshot[0])
	}
}
