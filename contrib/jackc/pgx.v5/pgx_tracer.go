// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type operationType string

const (
	tagOperation       = "db.operation"
	tagRowsAffected    = "db.result.rows_affected"
	tagBatchNumQueries = "db.batch.num_queries"
	tagCopyFromTables  = "db.copy_from.tables"
	tagCopyFromColumns = "db.copy_from.columns"
)

const (
	operationTypeConnect  operationType = "Connect"
	operationTypeQuery                  = "Query"
	operationTypePrepare                = "Prepare"
	operationTypeBatch                  = "Batch"
	operationTypeCopyFrom               = "Copy From"
	operationTypeAcquire                = "Acquire"
)

type tracedBatchQuery struct {
	span *tracer.Span
	data pgx.TraceBatchQueryData
}

func (tb *tracedBatchQuery) finish() {
	tb.span.Finish(tracer.WithError(tb.data.Err))
}

// batchState holds per-batch mutable tracing state. It is stored in the context
// returned by TraceBatchStart so that concurrent batches on different pool
// connections each have isolated state, avoiding a race on shared pgxTracer fields.
type batchState struct {
	prevQuery *tracedBatchQuery
}

type contextKeyBatchState struct{}

type allPgxTracers interface {
	pgx.QueryTracer
	pgx.BatchTracer
	pgx.ConnectTracer
	pgx.PrepareTracer
	pgx.CopyFromTracer
	pgxpool.AcquireTracer
}

type wrappedPgxTracer struct {
	query       pgx.QueryTracer
	batch       pgx.BatchTracer
	connect     pgx.ConnectTracer
	prepare     pgx.PrepareTracer
	copyFrom    pgx.CopyFromTracer
	poolAcquire pgxpool.AcquireTracer
}

// connInfo holds the subset of connection config fields needed for span tags.
// Snapshotted once at pool/connection creation to avoid deep-copying the full
// pgx.ConnConfig (including TLS state) on every traced operation.
type connInfo struct {
	host string
	port uint16
	db   string
	user string
}

type pgxTracer struct {
	cfg      *config
	wrapped  wrappedPgxTracer
	connInfo connInfo
}

var (
	_ allPgxTracers = (*pgxTracer)(nil)
)

func wrapPgxTracer(prev pgx.QueryTracer, opts ...Option) *pgxTracer {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	cfg.checkStatsdRequired()
	tr := &pgxTracer{cfg: cfg}
	if prev != nil {
		tr.wrapped.query = prev
		if batchTr, ok := prev.(pgx.BatchTracer); ok {
			tr.wrapped.batch = batchTr
		}
		if connTr, ok := prev.(pgx.ConnectTracer); ok {
			tr.wrapped.connect = connTr
		}
		if prepareTr, ok := prev.(pgx.PrepareTracer); ok {
			tr.wrapped.prepare = prepareTr
		}
		if copyFromTr, ok := prev.(pgx.CopyFromTracer); ok {
			tr.wrapped.copyFrom = copyFromTr
		}
		if poolAcquireTr, ok := prev.(pgxpool.AcquireTracer); ok {
			tr.wrapped.poolAcquire = poolAcquireTr
		}
	}

	return tr
}

func (t *pgxTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !t.cfg.traceQuery {
		return ctx
	}
	if t.wrapped.query != nil {
		ctx = t.wrapped.query.TraceQueryStart(ctx, conn, data)
	}
	opts := t.spanOptions(operationTypeQuery, data.SQL)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.query", opts...)
	return ctx
}

func (t *pgxTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if !t.cfg.traceQuery {
		return
	}
	if t.wrapped.query != nil {
		t.wrapped.query.TraceQueryEnd(ctx, conn, data)
	}
	span, ok := tracer.SpanFromContext(ctx)
	if ok {
		span.SetTag(tagRowsAffected, data.CommandTag.RowsAffected())
	}
	t.finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TraceBatchStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	if !t.cfg.traceBatch {
		return ctx
	}
	if t.wrapped.batch != nil {
		ctx = t.wrapped.batch.TraceBatchStart(ctx, conn, data)
	}
	opts := t.spanOptions(operationTypeBatch, "",
		tracer.Tag(tagBatchNumQueries, data.Batch.Len()),
	)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.batch", opts...)
	ctx = context.WithValue(ctx, contextKeyBatchState{}, &batchState{})
	return ctx
}

func (t *pgxTracer) TraceBatchQuery(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchQueryData) {
	if !t.cfg.traceBatch {
		return
	}
	if t.wrapped.batch != nil {
		t.wrapped.batch.TraceBatchQuery(ctx, conn, data)
	}
	// Finish the previous batch query span before starting the next one, since pgx doesn't provide hooks or
	// timestamp information about when the actual operation started or finished.
	// batchState is stored per-batch in the context so concurrent batches on different pool connections
	// each track their own prevQuery without racing on shared tracer state.
	bs, _ := ctx.Value(contextKeyBatchState{}).(*batchState)
	if bs != nil && bs.prevQuery != nil {
		bs.prevQuery.finish()
	}
	opts := t.spanOptions(operationTypeQuery, data.SQL,
		tracer.Tag(tagRowsAffected, data.CommandTag.RowsAffected()),
	)
	span, _ := tracer.StartSpanFromContext(ctx, "pgx.batch.query", opts...)
	if bs != nil {
		bs.prevQuery = &tracedBatchQuery{
			span: span,
			data: data,
		}
	}
}

func (t *pgxTracer) TraceBatchEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchEndData) {
	if !t.cfg.traceBatch {
		return
	}
	if t.wrapped.batch != nil {
		t.wrapped.batch.TraceBatchEnd(ctx, conn, data)
	}
	if bs, _ := ctx.Value(contextKeyBatchState{}).(*batchState); bs != nil && bs.prevQuery != nil {
		bs.prevQuery.finish()
		bs.prevQuery = nil
	}
	t.finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TraceCopyFromStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	if !t.cfg.traceCopyFrom {
		return ctx
	}
	if t.wrapped.copyFrom != nil {
		ctx = t.wrapped.copyFrom.TraceCopyFromStart(ctx, conn, data)
	}
	opts := t.spanOptions(operationTypeCopyFrom, "",
		tracer.Tag(tagCopyFromTables, data.TableName),
		tracer.Tag(tagCopyFromColumns, data.ColumnNames),
	)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.copy_from", opts...)
	return ctx
}

func (t *pgxTracer) TraceCopyFromEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceCopyFromEndData) {
	if !t.cfg.traceCopyFrom {
		return
	}
	if t.wrapped.copyFrom != nil {
		t.wrapped.copyFrom.TraceCopyFromEnd(ctx, conn, data)
	}
	t.finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TracePrepareStart(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	if !t.cfg.tracePrepare {
		return ctx
	}
	if t.wrapped.prepare != nil {
		ctx = t.wrapped.prepare.TracePrepareStart(ctx, conn, data)
	}
	opts := t.spanOptions(operationTypePrepare, data.SQL)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.prepare", opts...)
	return ctx
}

func (t *pgxTracer) TracePrepareEnd(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareEndData) {
	if !t.cfg.tracePrepare {
		return
	}
	if t.wrapped.prepare != nil {
		t.wrapped.prepare.TracePrepareEnd(ctx, conn, data)
	}
	t.finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TraceConnectStart(ctx context.Context, data pgx.TraceConnectStartData) context.Context {
	if !t.cfg.traceConnect {
		return ctx
	}
	if t.wrapped.connect != nil {
		ctx = t.wrapped.connect.TraceConnectStart(ctx, data)
	}
	opts := t.spanOptions(operationTypeConnect, "")
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.connect", opts...)
	return ctx
}

func (t *pgxTracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	if !t.cfg.traceConnect {
		return
	}
	if t.wrapped.connect != nil {
		t.wrapped.connect.TraceConnectEnd(ctx, data)
	}
	t.finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TraceAcquireStart(ctx context.Context, pool *pgxpool.Pool, data pgxpool.TraceAcquireStartData) context.Context {
	if !t.cfg.traceAcquire {
		return ctx
	}
	if t.wrapped.poolAcquire != nil {
		ctx = t.wrapped.poolAcquire.TraceAcquireStart(ctx, pool, data)
	}
	opts := t.spanOptions(operationTypeAcquire, "")
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.pool.acquire", opts...)
	return ctx
}

func (t *pgxTracer) TraceAcquireEnd(ctx context.Context, pool *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	if !t.cfg.traceAcquire {
		return
	}
	if t.wrapped.poolAcquire != nil {
		t.wrapped.poolAcquire.TraceAcquireEnd(ctx, pool, data)
	}
	t.finishSpan(ctx, data.Err)
}

func (t *pgxTracer) spanOptions(op operationType, sqlStatement string, extraOpts ...tracer.StartSpanOption) []tracer.StartSpanOption {
	ci := &t.connInfo
	opts := []tracer.StartSpanOption{
		instrumentation.ServiceNameWithSource(t.cfg.serviceName, t.cfg.serviceSource),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.Tag(ext.DBSystem, ext.DBSystemPostgreSQL),
		tracer.Tag(ext.Component, instrumentation.PackageJackcPGXV5),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(tagOperation, string(op)),
	}
	opts = append(opts, extraOpts...)
	if sqlStatement != "" {
		opts = append(opts, tracer.Tag(ext.DBStatement, sqlStatement))
		opts = append(opts, tracer.ResourceName(sqlStatement))
	} else {
		opts = append(opts, tracer.ResourceName(string(op)))
	}
	if ci.host != "" {
		opts = append(opts, tracer.Tag(ext.NetworkDestinationName, ci.host))
	}
	if ci.port != 0 {
		opts = append(opts, tracer.Tag(ext.NetworkDestinationPort, int(ci.port)))
	}
	if ci.db != "" {
		opts = append(opts, tracer.Tag(ext.DBName, ci.db))
	}
	if ci.user != "" {
		opts = append(opts, tracer.Tag(ext.DBUser, ci.user))
	}
	return opts
}

func (t *pgxTracer) finishSpan(ctx context.Context, err error) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	if err != nil && (t.cfg.errCheck == nil || t.cfg.errCheck(err)) {
		span.SetTag(ext.Error, err)
	}
	span.Finish()
}
