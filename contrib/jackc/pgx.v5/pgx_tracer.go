// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

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
	span tracer.Span
	data pgx.TraceBatchQueryData
}

func (tb *tracedBatchQuery) finish() {
	tb.span.Finish(tracer.WithError(tb.data.Err))
}

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

type pgxTracer struct {
	cfg            *config
	prevBatchQuery *tracedBatchQuery
	wrapped        wrappedPgxTracer
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
	opts := t.spanOptions(conn.Config(), operationTypeQuery, data.SQL)
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
	finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TraceBatchStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	if !t.cfg.traceBatch {
		return ctx
	}
	if t.wrapped.batch != nil {
		ctx = t.wrapped.batch.TraceBatchStart(ctx, conn, data)
	}
	opts := t.spanOptions(conn.Config(), operationTypeBatch, "",
		tracer.Tag(tagBatchNumQueries, data.Batch.Len()),
	)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.batch", opts...)
	return ctx
}

func (t *pgxTracer) TraceBatchQuery(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchQueryData) {
	if !t.cfg.traceBatch {
		return
	}
	if t.wrapped.batch != nil {
		t.wrapped.batch.TraceBatchQuery(ctx, conn, data)
	}
	// Finish the previous batch query span before starting the next one, since pgx doesn't provide hooks or timestamp
	// information about when the actual operation started or finished.
	// pgx.Batch* types don't support concurrency. This function doesn't support it either.
	if t.prevBatchQuery != nil {
		t.prevBatchQuery.finish()
	}
	opts := t.spanOptions(conn.Config(), operationTypeQuery, data.SQL,
		tracer.Tag(tagRowsAffected, data.CommandTag.RowsAffected()),
	)
	span, _ := tracer.StartSpanFromContext(ctx, "pgx.batch.query", opts...)
	t.prevBatchQuery = &tracedBatchQuery{
		span: span,
		data: data,
	}
}

func (t *pgxTracer) TraceBatchEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchEndData) {
	if !t.cfg.traceBatch {
		return
	}
	if t.wrapped.batch != nil {
		t.wrapped.batch.TraceBatchEnd(ctx, conn, data)
	}
	if t.prevBatchQuery != nil {
		t.prevBatchQuery.finish()
		t.prevBatchQuery = nil
	}
	finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TraceCopyFromStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	if !t.cfg.traceCopyFrom {
		return ctx
	}
	if t.wrapped.copyFrom != nil {
		ctx = t.wrapped.copyFrom.TraceCopyFromStart(ctx, conn, data)
	}
	opts := t.spanOptions(conn.Config(), operationTypeCopyFrom, "",
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
	finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TracePrepareStart(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	if !t.cfg.tracePrepare {
		return ctx
	}
	if t.wrapped.prepare != nil {
		ctx = t.wrapped.prepare.TracePrepareStart(ctx, conn, data)
	}
	opts := t.spanOptions(conn.Config(), operationTypePrepare, data.SQL)
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
	finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TraceConnectStart(ctx context.Context, data pgx.TraceConnectStartData) context.Context {
	if !t.cfg.traceConnect {
		return ctx
	}
	if t.wrapped.connect != nil {
		ctx = t.wrapped.connect.TraceConnectStart(ctx, data)
	}
	opts := t.spanOptions(data.ConnConfig, operationTypeConnect, "")
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
	finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TraceAcquireStart(ctx context.Context, pool *pgxpool.Pool, data pgxpool.TraceAcquireStartData) context.Context {
	if !t.cfg.traceAcquire {
		return ctx
	}
	if t.wrapped.poolAcquire != nil {
		ctx = t.wrapped.poolAcquire.TraceAcquireStart(ctx, pool, data)
	}
	opts := t.spanOptions(pool.Config().ConnConfig, operationTypeAcquire, "")
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
	finishSpan(ctx, data.Err)
}

func (t *pgxTracer) spanOptions(connConfig *pgx.ConnConfig, op operationType, sqlStatement string, extraOpts ...ddtrace.StartSpanOption) []ddtrace.StartSpanOption {
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(t.cfg.serviceName),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.Tag(ext.DBSystem, ext.DBSystemPostgreSQL),
		tracer.Tag(ext.Component, componentName),
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
	if host := connConfig.Host; host != "" {
		opts = append(opts, tracer.Tag(ext.NetworkDestinationName, host))
	}
	if port := connConfig.Port; port != 0 {
		opts = append(opts, tracer.Tag(ext.NetworkDestinationPort, int(port)))
	}
	if db := connConfig.Database; db != "" {
		opts = append(opts, tracer.Tag(ext.DBName, db))
	}
	if user := connConfig.User; user != "" {
		opts = append(opts, tracer.Tag(ext.DBUser, user))
	}
	return opts
}

func finishSpan(ctx context.Context, err error) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	span.Finish(tracer.WithError(err))
}
