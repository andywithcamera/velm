package db

import (
	"context"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// derivedObservabilityQueueSize bounds how many emit jobs may be pending.
	// When the queue is full, jobs are dropped on purpose: derived
	// observability is best-effort data and must never push back on the
	// request path.
	derivedObservabilityQueueSize = 256

	// derivedObservabilityMaxBatch caps how many queued jobs one worker pass
	// coalesces into a single emit, so one base-app resolve serves a batch.
	derivedObservabilityMaxBatch = 32
)

type derivedObservabilityJob struct {
	label  string
	events []derivedObservabilityEvent
}

// derivedObservabilityQueue runs derived-observability emits off the request
// path.
//
// The emit executes the base app's ingest service in goja, which costs
// hundreds of milliseconds even when it succeeds. Running it inline made every
// HTTP response wait for it, including health checks. Jobs are queued and
// processed by a single worker; enqueue never blocks, and a full queue drops.
type derivedObservabilityQueue struct {
	jobs     chan derivedObservabilityJob
	maxBatch int
	timeout  time.Duration
	emit     func(context.Context, []derivedObservabilityEvent) error

	// mu guards sending on jobs against stop closing it.
	mu       sync.RWMutex
	stopping bool

	startOnce sync.Once
	stopOnce  sync.Once

	dropped   atomic.Int64
	processed atomic.Int64
	failed    atomic.Int64
}

func newDerivedObservabilityQueue(size, maxBatch int, timeout time.Duration, emit func(context.Context, []derivedObservabilityEvent) error) *derivedObservabilityQueue {
	if size < 1 {
		size = 1
	}
	if maxBatch < 1 {
		maxBatch = 1
	}
	if emit == nil {
		emit = emitDerivedObservability
	}
	return &derivedObservabilityQueue{
		jobs:     make(chan derivedObservabilityJob, size),
		maxBatch: maxBatch,
		timeout:  timeout,
		emit:     emit,
	}
}

// start launches the worker exactly once. Nothing runs until the first enqueue,
// so a process that never emits derived observability never starts a goroutine.
func (q *derivedObservabilityQueue) start() {
	q.startOnce.Do(func() {
		go q.run()
	})
}

func (q *derivedObservabilityQueue) stop() {
	q.stopOnce.Do(func() {
		q.mu.Lock()
		q.stopping = true
		close(q.jobs)
		q.mu.Unlock()
	})
}

// enqueue schedules an emit and reports whether it was accepted. It never
// blocks: when the queue is full the job is dropped and counted.
func (q *derivedObservabilityQueue) enqueue(label string, events []derivedObservabilityEvent) bool {
	if len(events) == 0 {
		return true
	}

	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.stopping {
		return false
	}

	select {
	case q.jobs <- derivedObservabilityJob{label: label, events: events}:
		return true
	default:
		dropped := q.dropped.Add(1)
		observabilityFailureLog("queue", "full", dropped)
		return false
	}
}

func (q *derivedObservabilityQueue) run() {
	for job := range q.jobs {
		q.emitBatch(q.coalesce(job))
	}
}

// coalesce folds jobs already waiting in the queue into the current batch.
func (q *derivedObservabilityQueue) coalesce(first derivedObservabilityJob) []derivedObservabilityEvent {
	batch := first.events
	for jobs := 1; jobs < q.maxBatch; jobs++ {
		select {
		case next, ok := <-q.jobs:
			if !ok {
				return batch
			}
			batch = append(batch, next.events...)
		default:
			return batch
		}
	}
	return batch
}

func (q *derivedObservabilityQueue) emitBatch(events []derivedObservabilityEvent) {
	if len(events) == 0 {
		return
	}

	// One metadata cache per batch so a batch resolves the base app once
	// instead of reloading and re-parsing every app definition per event.
	ctx, cancel := context.WithTimeout(WithRequestMetadataCache(context.Background()), q.timeout)
	defer cancel()

	if err := q.emit(ctx, events); err != nil {
		q.failed.Add(int64(len(events)))
		observabilityFailureLog("emit", err.Error(), q.failed.Load())
		return
	}
	q.processed.Add(int64(len(events)))
}

var defaultDerivedObservabilityQueue = newDerivedObservabilityQueue(
	derivedObservabilityQueueSize,
	derivedObservabilityMaxBatch,
	derivedObservabilityTimeout,
	emitDerivedObservability,
)

// enqueueDerivedObservability is the request-path entry point: it returns
// immediately and the emit happens on the worker.
func enqueueDerivedObservability(label string, events []derivedObservabilityEvent) {
	if len(events) == 0 {
		return
	}
	defaultDerivedObservabilityQueue.start()
	defaultDerivedObservabilityQueue.enqueue(label, events)
}

var (
	observabilityLogMu      sync.Mutex
	observabilityLastLog    time.Time
	observabilityLogBacklog int
)

// observabilityFailureLog rate-limits emit failures to one line per 30s and
// reports how many were suppressed.
func observabilityFailureLog(kind, detail string, count int64) {
	observabilityLogMu.Lock()
	now := time.Now()
	if now.Sub(observabilityLastLog) < 30*time.Second {
		observabilityLogBacklog++
		observabilityLogMu.Unlock()
		return
	}
	backlog := observabilityLogBacklog
	observabilityLogBacklog = 0
	observabilityLastLog = now
	observabilityLogMu.Unlock()

	message := "derived observability " + kind + ": " + detail
	if count > 1 {
		message += " (x" + strconv.FormatInt(count, 10) + " total)"
	}
	if backlog > 0 {
		message += " (" + strconv.FormatInt(int64(backlog), 10) + " suppressed in last 30s)"
	}
	log.Print(message)
}
