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
)

type operationType string

const (
	operationTypeConnect  operationType = "Connect"
	operationTypeQuery                  = "Query"
	operationTypePrepare                = "Prepare"
	operationTypeBatch                  = "Batch"
	operationTypeCopyFrom               = "Copy From"
)

type tracedBatchQuery struct {
	span tracer.Span
	data pgx.TraceBatchQueryData
}

func (tb *tracedBatchQuery) finish() {
	tb.span.Finish(tracer.WithError(tb.data.Err))
}

type pgxTracer struct {
	cfg            *config
	prevBatchQuery *tracedBatchQuery
}

var (
	_ pgx.QueryTracer    = (*pgxTracer)(nil)
	_ pgx.BatchTracer    = (*pgxTracer)(nil)
	_ pgx.ConnectTracer  = (*pgxTracer)(nil)
	_ pgx.PrepareTracer  = (*pgxTracer)(nil)
	_ pgx.CopyFromTracer = (*pgxTracer)(nil)
)

func newPgxTracer(opts ...Option) *pgxTracer {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	return &pgxTracer{cfg: cfg}
}

func (t *pgxTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !t.cfg.traceQuery {
		return ctx
	}
	opts := t.spanOptions(conn.Config(), operationTypeQuery, data.SQL)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.query", opts...)
	return ctx
}

func (t *pgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if !t.cfg.traceQuery {
		return
	}
	span, ok := tracer.SpanFromContext(ctx)
	if ok {
		span.SetTag("db.query.rows_affected", data.CommandTag.RowsAffected())
	}
	finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TraceBatchStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	if !t.cfg.traceBatch {
		return ctx
	}
	opts := t.spanOptions(conn.Config(), operationTypeBatch, "",
		tracer.Tag("db.batch.num_queries", data.Batch.Len()),
	)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.batch", opts...)
	return ctx
}

func (t *pgxTracer) TraceBatchQuery(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchQueryData) {
	if !t.cfg.traceBatch {
		return
	}
	// Finish the previous batch query span before starting the next one, since pgx doesn't provide hooks or timestamp
	// information about when the actual operation started or finished.
	if t.prevBatchQuery != nil {
		t.prevBatchQuery.finish()
	}
	opts := t.spanOptions(conn.Config(), operationTypeQuery, data.SQL,
		tracer.Tag("db.query.rows_affected", data.CommandTag.RowsAffected()),
	)
	span, _ := tracer.StartSpanFromContext(ctx, "pgx.batch.query", opts...)
	t.prevBatchQuery = &tracedBatchQuery{
		span: span,
		data: data,
	}
}

func (t *pgxTracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	if !t.cfg.traceBatch {
		return
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
	opts := t.spanOptions(conn.Config(), operationTypeCopyFrom, "",
		tracer.Tag("db.copy_from.tables", data.TableName),
		tracer.Tag("db.copy_from.columns", data.ColumnNames),
	)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.copy_from", opts...)
	return ctx
}

func (t *pgxTracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	if !t.cfg.traceCopyFrom {
		return
	}
	finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TracePrepareStart(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	if !t.cfg.tracePrepare {
		return ctx
	}
	opts := t.spanOptions(conn.Config(), operationTypePrepare, data.SQL)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.prepare", opts...)
	return ctx
}

func (t *pgxTracer) TracePrepareEnd(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareEndData) {
	if !t.cfg.tracePrepare {
		return
	}
	finishSpan(ctx, data.Err)
}

func (t *pgxTracer) TraceConnectStart(ctx context.Context, data pgx.TraceConnectStartData) context.Context {
	if !t.cfg.traceConnect {
		return ctx
	}
	opts := t.spanOptions(data.ConnConfig, operationTypeConnect, "")
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.connect", opts...)
	return ctx
}

func (t *pgxTracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	if !t.cfg.traceConnect {
		return
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
		tracer.Tag("db.operation", string(op)),
	}
	opts = append(opts, extraOpts...)
	if sqlStatement != "" {
		opts = append(opts, tracer.Tag(ext.DBStatement, sqlStatement))
		opts = append(opts, tracer.ResourceName(sqlStatement))
	} else {
		opts = append(opts, tracer.ResourceName(string(op)))
	}
	if host := connConfig.Host; host != "" {
		opts = append(opts, tracer.Tag(ext.TargetHost, host))
	}
	if port := connConfig.Port; port != 0 {
		opts = append(opts, tracer.Tag(ext.TargetPort, int(port)))
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
