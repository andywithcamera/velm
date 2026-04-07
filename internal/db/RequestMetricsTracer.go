package db

import (
	"context"
	"time"

	"velm/internal/monitoring"

	"github.com/jackc/pgx/v5"
)

type requestMetricsTracer struct{}

type requestTraceStartContextKey string

const requestTraceStartKey requestTraceStartContextKey = "request_trace_start"

type requestTraceStart struct {
	startedAt time.Time
	sql       string
}

type batchTraceStart struct {
	startedAt  time.Time
	queryCount int
}

type batchTraceStartContextKey string

const batchTraceStartKey batchTraceStartContextKey = "request_trace_batch_start"

func (t *requestMetricsTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, requestTraceStartKey, requestTraceStart{
		startedAt: time.Now(),
		sql:       data.SQL,
	})
}

func (t *requestMetricsTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	start, _ := ctx.Value(requestTraceStartKey).(requestTraceStart)
	metrics := monitoring.RequestMetricsFromContext(ctx)
	if metrics == nil || start.startedAt.IsZero() {
		return
	}
	metrics.RecordDBQuery(time.Since(start.startedAt), start.sql)
}

func (t *requestMetricsTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	queryCount := 0
	if data.Batch != nil {
		queryCount = data.Batch.Len()
	}
	return context.WithValue(ctx, batchTraceStartKey, batchTraceStart{
		startedAt:  time.Now(),
		queryCount: queryCount,
	})
}

func (t *requestMetricsTracer) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}

func (t *requestMetricsTracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchEndData) {
	start, _ := ctx.Value(batchTraceStartKey).(batchTraceStart)
	metrics := monitoring.RequestMetricsFromContext(ctx)
	if metrics == nil || start.startedAt.IsZero() {
		return
	}
	duration := time.Since(start.startedAt)
	queryCount := start.queryCount
	if queryCount <= 0 {
		metrics.RecordDBQuery(duration, "batch")
		return
	}
	perQueryDuration := duration / time.Duration(queryCount)
	for i := 0; i < queryCount; i++ {
		metrics.RecordDBQuery(perQueryDuration, "batch")
	}
}
