package main

import (
	"errors"
	"testing"

	"velm/internal/db"
)

// Issue 9: the destination lives on _ai_provider, shared by every connection
// attached to it, so one edit there would otherwise redirect the key of every
// connection on that provider.
func TestSyncDestinationFollowsTheKeyNotTheProviderRow(t *testing.T) {
	conn := &db.AIConnection{ID: "c1", ProviderID: "p1", PinnedBaseURL: "https://api.example.com"}
	edited := &db.AIProvider{ID: "p1", BaseURL: "https://attacker.example"}

	got, err := resolveSyncDestination(conn, edited)
	if !errors.Is(err, errConnectionDestinationChanged) {
		t.Fatalf("err = %v, want errConnectionDestinationChanged", err)
	}
	if got != "" {
		t.Fatalf("destination = %q, want no destination: the key must not be sent anywhere", got)
	}
}

func TestSyncDestinationUsesPinnedURL(t *testing.T) {
	conn := &db.AIConnection{ID: "c1", ProviderID: "p1", PinnedBaseURL: "https://api.example.com"}
	provider := &db.AIProvider{ID: "p1", BaseURL: "https://api.example.com"}

	got, err := resolveSyncDestination(conn, provider)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.example.com" {
		t.Fatalf("destination = %q, want the pinned URL", got)
	}
}

// A row written before pinning existed must not fall back to the live provider
// URL: that fallback is the hole. Refusing asks for the key again instead.
func TestSyncDestinationRefusesUnpinnedConnection(t *testing.T) {
	conn := &db.AIConnection{ID: "c1", ProviderID: "p1", PinnedBaseURL: ""}
	provider := &db.AIProvider{ID: "p1", BaseURL: "https://api.example.com"}

	if _, err := resolveSyncDestination(conn, provider); !errors.Is(err, errConnectionDestinationChanged) {
		t.Fatalf("err = %v, want errConnectionDestinationChanged", err)
	}
}

func TestDestinationChangedMessageTellsTheAdminWhatToDo(t *testing.T) {
	msg := syncFailureMessage(errConnectionDestinationChanged)
	if msg != "the provider endpoint changed since this key was entered; re-enter the key to authorise the new destination" {
		t.Fatalf("unexpected message: %q", msg)
	}
}
