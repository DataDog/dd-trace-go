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

type pgxTracer struct {
	cfg *config
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

func (t *pgxTracer) defaultSpanOptions() []ddtrace.StartSpanOption {
	return []ddtrace.StartSpanOption{
		tracer.ServiceName(t.cfg.serviceName),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.Tag(ext.DBSystem, ext.DBSystemPostgreSQL),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
	}
}

func (t *pgxTracer) spanOptions(opts ...ddtrace.StartSpanOption) []ddtrace.StartSpanOption {
	return append(t.defaultSpanOptions(), opts...)
}

func finish(ctx context.Context, err error) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	span.Finish(tracer.WithError(err))
}

func (t *pgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !t.cfg.traceQuery {
		return ctx
	}
	opts := t.spanOptions(
		tracer.ResourceName(data.SQL),
	)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.query", opts...)
	return ctx
}

func (t *pgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if !t.cfg.traceQuery {
		return
	}
	finish(ctx, data.Err)
}

func (t *pgxTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	if !t.cfg.traceBatch {
		return ctx
	}
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.batch", t.spanOptions()...)
	return ctx
}

func (t *pgxTracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	if !t.cfg.traceBatch {
		return
	}
	opts := t.spanOptions(
		tracer.ResourceName(data.SQL),
	)
	// TODO: this might be wrong
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.batch.query", opts...)
	finish(ctx, data.Err)
}

func (t *pgxTracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	if !t.cfg.traceBatch {
		return
	}
	finish(ctx, data.Err)
}

func (t *pgxTracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	if !t.cfg.traceCopyFrom {
		return ctx
	}
	opts := t.spanOptions(
		tracer.Tag("tables", data.TableName),
		tracer.Tag("columns", data.ColumnNames),
	)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.copyfrom", opts...)
	return ctx
}

func (t *pgxTracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	if !t.cfg.traceCopyFrom {
		return
	}
	finish(ctx, data.Err)
}

func (t *pgxTracer) TracePrepareStart(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	if !t.cfg.tracePrepare {
		return ctx
	}
	opts := t.spanOptions(
		tracer.ResourceName(data.SQL),
	)
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.prepare", opts...)
	return ctx
}

func (t *pgxTracer) TracePrepareEnd(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareEndData) {
	if !t.cfg.tracePrepare {
		return
	}
	finish(ctx, data.Err)
}

func (t *pgxTracer) TraceConnectStart(ctx context.Context, _ pgx.TraceConnectStartData) context.Context {
	if !t.cfg.traceConnect {
		return ctx
	}
	_, ctx = tracer.StartSpanFromContext(ctx, "pgx.connect", t.spanOptions()...)
	return ctx
}

func (t *pgxTracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	if !t.cfg.traceConnect {
		return
	}
	finish(ctx, data.Err)
}
