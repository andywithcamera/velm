package monitoring

import (
	"context"
	"strings"
	"sync"
	"time"
)

type requestMetricsContextKey string

const requestMetricsKey requestMetricsContextKey = "request_metrics"

type RequestMetrics struct {
	RequestID string
	StartedAt time.Time

	mu             sync.Mutex
	dbQueryCount   int
	dbDuration     time.Duration
	dbSlowest      time.Duration
	dbSlowestQuery string
}

type Snapshot struct {
	RequestID      string
	StartedAt      time.Time
	DBQueryCount   int
	DBDuration     time.Duration
	DBSlowest      time.Duration
	DBSlowestQuery string
}

func NewRequestMetrics(requestID string, startedAt time.Time) *RequestMetrics {
	return &RequestMetrics{
		RequestID: strings.TrimSpace(requestID),
		StartedAt: startedAt,
	}
}

func WithRequestMetrics(ctx context.Context, metrics *RequestMetrics) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if metrics == nil {
		return ctx
	}
	return context.WithValue(ctx, requestMetricsKey, metrics)
}

func RequestMetricsFromContext(ctx context.Context) *RequestMetrics {
	if ctx == nil {
		return nil
	}
	metrics, _ := ctx.Value(requestMetricsKey).(*RequestMetrics)
	return metrics
}

func (m *RequestMetrics) RecordDBQuery(duration time.Duration, sql string) {
	if m == nil {
		return
	}

	sql = normalizeSQL(sql)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.dbQueryCount++
	m.dbDuration += duration
	if duration > m.dbSlowest {
		m.dbSlowest = duration
		m.dbSlowestQuery = sql
	}
}

func (m *RequestMetrics) Snapshot() Snapshot {
	if m == nil {
		return Snapshot{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return Snapshot{
		RequestID:      m.RequestID,
		StartedAt:      m.StartedAt,
		DBQueryCount:   m.dbQueryCount,
		DBDuration:     m.dbDuration,
		DBSlowest:      m.dbSlowest,
		DBSlowestQuery: m.dbSlowestQuery,
	}
}

func normalizeSQL(sql string) string {
	sql = strings.Join(strings.Fields(strings.TrimSpace(sql)), " ")
	if len(sql) > 240 {
		return sql[:237] + "..."
	}
	return sql
}
