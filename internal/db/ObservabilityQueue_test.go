package db

import (
	"context"
	"sync"
	"testing"
	"time"
)

func testObservabilityEvent(requestID string) derivedObservabilityEvent {
	return derivedObservabilityEvent{
		RequestID: requestID,
		Payload:   map[string]any{"node": requestID},
	}
}

// The whole point of the queue is that the request path never waits for the
// emit. If this regresses, every response pays for a goja service call again.
func TestDerivedObservabilityEnqueueDoesNotWaitForEmit(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)

	q := newDerivedObservabilityQueue(8, 1, time.Minute, func(context.Context, []derivedObservabilityEvent) error {
		entered <- struct{}{}
		<-release
		return nil
	})
	q.start()
	defer q.stop()

	enqueueReturned := make(chan struct{})
	go func() {
		q.enqueue("test", []derivedObservabilityEvent{testObservabilityEvent("req-1")})
		close(enqueueReturned)
	}()

	select {
	case <-enqueueReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue blocked on the emit")
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never started the emit")
	}
	close(release)
}

// A full queue must drop, never push back on the caller.
func TestDerivedObservabilityQueueDropsWhenFull(t *testing.T) {
	q := newDerivedObservabilityQueue(1, 1, time.Minute, func(context.Context, []derivedObservabilityEvent) error {
		return nil
	})
	// No worker started, so the single buffer slot stays occupied.

	if !q.enqueue("test", []derivedObservabilityEvent{testObservabilityEvent("req-1")}) {
		t.Fatal("first enqueue should be accepted")
	}
	if q.enqueue("test", []derivedObservabilityEvent{testObservabilityEvent("req-2")}) {
		t.Fatal("second enqueue should be dropped, not blocked")
	}
	if got := q.dropped.Load(); got != 1 {
		t.Fatalf("dropped counter = %d, want 1", got)
	}
}

// Coalescing exists so a burst of events shares one base-app resolve.
func TestDerivedObservabilityQueueCoalescesQueuedJobs(t *testing.T) {
	var mu sync.Mutex
	var batches [][]derivedObservabilityEvent
	done := make(chan struct{}, 1)

	q := newDerivedObservabilityQueue(8, 8, time.Minute, func(_ context.Context, events []derivedObservabilityEvent) error {
		mu.Lock()
		batches = append(batches, events)
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})

	for i := 0; i < 5; i++ {
		q.enqueue("test", []derivedObservabilityEvent{testObservabilityEvent("req-" + string(rune('a'+i)))})
	}
	q.start()
	defer q.stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never emitted")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("emit called %d times, want 1 (coalesced)", len(batches))
	}
	if len(batches[0]) != 5 {
		t.Fatalf("batch carried %d events, want 5", len(batches[0]))
	}
}

// The emit context must carry a metadata cache, otherwise every script model
// call reloads and re-parses every app definition.
func TestDerivedObservabilityEmitContextHasMetadataCache(t *testing.T) {
	checked := make(chan bool, 1)

	q := newDerivedObservabilityQueue(4, 1, time.Minute, func(ctx context.Context, _ []derivedObservabilityEvent) error {
		checked <- requestMetadataCacheFromContext(ctx) != nil
		return nil
	})
	q.start()
	defer q.stop()

	q.enqueue("test", []derivedObservabilityEvent{testObservabilityEvent("req-1")})

	select {
	case ok := <-checked:
		if !ok {
			t.Fatal("emit context is missing the request metadata cache")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker never emitted")
	}
}

func TestDerivedObservabilityQueueIgnoresEmptyEvents(t *testing.T) {
	emitted := make(chan struct{}, 1)
	q := newDerivedObservabilityQueue(4, 1, time.Minute, func(context.Context, []derivedObservabilityEvent) error {
		emitted <- struct{}{}
		return nil
	})
	q.start()
	defer q.stop()

	if !q.enqueue("test", nil) {
		t.Fatal("enqueue of empty events should be reported as accepted")
	}
	select {
	case <-emitted:
		t.Fatal("worker emitted for an empty event list")
	case <-time.After(100 * time.Millisecond):
	}
}
