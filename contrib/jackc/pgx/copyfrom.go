package pgx

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// TraceCopyFromStart marks the start of a CopyFrom query, implementing pgx.CopyFromTracer
func (t *tracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	opts := []ddtrace.StartSpanOption{
		ddtracer.ServiceName(t.serviceName),
		ddtracer.SpanType(ext.SpanTypeSQL),
		ddtracer.StartTime(time.Now()),
		ddtracer.Tag("sql.query_type", "Query"),
		ddtracer.Tag(ext.ResourceName, "pgx.copyfrom"),
		ddtracer.Tag("pgx.table_name", data.TableName),
		ddtracer.Tag("pgx.columns", data.ColumnNames),
	}
	for key, tag := range t.tags {
		opts = append(opts, ddtracer.Tag(key, tag))
	}
	if !math.IsNaN(t.analyticsRate) {
		opts = append(opts, ddtracer.Tag(ext.EventSampleRate, t.analyticsRate))
	}

	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.copyfrom", opts...)

	return ctx
}

// TraceCopyFromEnd marks the end of a CopyFrom query, implementing pgx.CopyFromTracer
func (t *tracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	span, exists := ddtracer.SpanFromContext(ctx)
	if !exists {
		return
	}

	if t.traceStatus {
		span.SetTag("pgx.status", data.CommandTag.String())
	}

	if data.Err != nil {
		span.SetTag(ext.Error, data.Err)
	}
	span.Finish()
}
