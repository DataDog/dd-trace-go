// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx_v5

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/jackc/pgx/v5"
)

const (
	componentName      = "jackc/pgx.v5"
	defaultServiceName = "postgres.db"
)

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/jackc/pgx/v5")
}

type trace struct {
	serviceName   string
	traceBatch    bool
	traceCopyFrom bool
	tracePrepare  bool
	traceConnect  bool
}

//nolint:revive
func New(opts ...Option) *trace {
	t := &trace{
		serviceName: defaultServiceName,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

func (t *trace) defaultSpanOptions() []ddtrace.StartSpanOption {
	return []ddtrace.StartSpanOption{
		ddtracer.ServiceName(t.serviceName),
		ddtracer.SpanType(ext.SpanTypeSQL),
		ddtracer.Tag(ext.DBSystem, ext.DBSystemPostgreSQL),
		ddtracer.Tag(ext.Component, componentName),
		ddtracer.Tag(ext.SpanKind, ext.SpanKindClient),
	}
}

func (t *trace) spanOptions(opts ...ddtrace.StartSpanOption) []ddtrace.StartSpanOption {
	return append(t.defaultSpanOptions(), opts...)
}

func finish(ctx context.Context, err error) {
	span, exists := ddtracer.SpanFromContext(ctx)
	if !exists {
		return
	}

	if err != nil {
		span.SetTag(ext.Error, err)
	}

	span.Finish()
}

func (t *trace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	opts := t.spanOptions(
		ddtracer.ResourceName(data.SQL),
	)

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.v5.query", opts...)

	return ctx
}

func (t *trace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	finish(ctx, data.Err)
}

func (t *trace) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	if !t.traceBatch {
		return ctx
	}

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.v5.batch", t.spanOptions()...)

	return ctx
}

func (t *trace) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	if !t.traceBatch {
		return
	}

	opts := t.spanOptions(
		ddtracer.ResourceName(data.SQL),
	)

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.v5.batch.query", opts...)

	finish(ctx, data.Err)
}

func (t *trace) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	if !t.traceBatch {
		return
	}

	finish(ctx, data.Err)
}

func (t *trace) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	if !t.traceCopyFrom {
		return ctx
	}

	opts := t.spanOptions(
		ddtracer.Tag("tables", data.TableName),
		ddtracer.Tag("columns", data.ColumnNames),
	)

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.v5.copyfrom", opts...)

	return ctx
}

func (t *trace) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	if !t.traceCopyFrom {
		return
	}

	finish(ctx, data.Err)
}

func (t *trace) TracePrepareStart(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	if !t.tracePrepare {
		return ctx
	}

	opts := t.spanOptions(
		ddtracer.ResourceName(data.SQL),
	)

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.v5.prepare", opts...)

	return ctx
}

func (t *trace) TracePrepareEnd(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareEndData) {
	if !t.tracePrepare {
		return
	}

	finish(ctx, data.Err)
}

func (t *trace) TraceConnectStart(ctx context.Context, _ pgx.TraceConnectStartData) context.Context {
	if !t.traceConnect {
		return ctx
	}

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.v5.connect", t.spanOptions()...)

	return ctx
}

func (t *trace) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	if !t.traceConnect {
		return
	}

	finish(ctx, data.Err)
}
