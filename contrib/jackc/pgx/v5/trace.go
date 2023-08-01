package pgx

import (
	"context"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/jackc/pgx/v5"
)

const (
	componentName      = "jackc/pgx/v5"
	defaultServiceName = "postgres.db"
)

type tracer struct {
	serviceName   string
	traceBatch    bool
	traceCopyFrom bool
	tracePrepare  bool
	traceConnect  bool
}

//nolint:revive
func New(opts ...Option) *tracer {
	t := &tracer{
		serviceName: defaultServiceName,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

func (t *tracer) defaultSpanOptions() []ddtrace.StartSpanOption {
	return []ddtrace.StartSpanOption{
		ddtracer.ServiceName(t.serviceName),
		ddtracer.SpanType(ext.SpanTypeSQL),
		ddtracer.Tag(ext.DBSystem, ext.DBSystemPostgreSQL),
		ddtracer.Tag(ext.Component, componentName),
		ddtracer.Tag(ext.SpanKind, ext.SpanKindClient),
	}
}

func (t *tracer) spanOptions(opts ...ddtrace.StartSpanOption) []ddtrace.StartSpanOption {
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

func (t *tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	opts := t.spanOptions(
		ddtracer.StartTime(time.Now()),
		ddtracer.ResourceName(data.SQL),
	)

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.query", opts...)

	return ctx
}

func (t *tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	finish(ctx, data.Err)
}

func (t *tracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	if !t.traceBatch {
		return ctx
	}

	opts := t.spanOptions(ddtracer.StartTime(time.Now()))

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.batch", opts...)

	return ctx
}

func (t *tracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	if !t.traceBatch {
		return
	}

	opts := t.spanOptions(
		ddtracer.StartTime(time.Now()),
		ddtracer.ResourceName(data.SQL),
	)

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.batch.query", opts...)

	finish(ctx, data.Err)
}

func (t *tracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	if !t.traceBatch {
		return
	}

	finish(ctx, data.Err)
}

func (t *tracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	if !t.traceCopyFrom {
		return ctx
	}

	opts := t.spanOptions(
		ddtracer.StartTime(time.Now()),
		ddtracer.Tag("tables", data.TableName),
		ddtracer.Tag("columns", data.ColumnNames),
	)

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.copyfrom", opts...)

	return ctx
}

func (t *tracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	if !t.traceCopyFrom {
		return
	}

	finish(ctx, data.Err)
}

func (t *tracer) TracePrepareStart(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	if !t.tracePrepare {
		return ctx
	}

	opts := t.spanOptions(
		ddtracer.StartTime(time.Now()),
		ddtracer.ResourceName(data.SQL),
	)

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.prepare", opts...)

	return ctx
}

func (t *tracer) TracePrepareEnd(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareEndData) {
	if !t.tracePrepare {
		return
	}

	finish(ctx, data.Err)
}

func (t *tracer) TraceConnectStart(ctx context.Context, _ pgx.TraceConnectStartData) context.Context {
	if !t.traceConnect {
		return ctx
	}

	opts := t.spanOptions(
		ddtracer.StartTime(time.Now()),
	)

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.connect", opts...)

	return ctx
}

func (t *tracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	if !t.traceConnect {
		return
	}

	finish(ctx, data.Err)
}
