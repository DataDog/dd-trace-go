package pgx

import (
	"context"
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/jackc/pgx/v5"
)

// tracer contains configs to tracing and would implement QueryTracer, BatchTracer,
// CopyFromTracer, PrepareTracer and ConnectTracer from pgx.
type tracer struct {
	serviceName   string
	tags          map[string]interface{}
	analyticsRate float64
	traceArgs     bool
	traceStatus   bool
}

// TraceQueryStart marks the start of a query, implementing pgx.QueryTracer
func (t *tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	opts := []ddtrace.StartSpanOption{
		ddtracer.ServiceName(t.serviceName),
		ddtracer.SpanType(ext.SpanTypeSQL),
		ddtracer.StartTime(time.Now()),
		ddtracer.Tag("sql.query_type", "Query"),
		ddtracer.Tag(ext.ResourceName, data.SQL),
	}
	if t.traceArgs {
		opts = append(opts, ddtracer.Tag("sql.args", data.Args))
	}
	for key, tag := range t.tags {
		opts = append(opts, ddtracer.Tag(key, tag))
	}
	if !math.IsNaN(t.analyticsRate) {
		opts = append(opts, ddtracer.Tag(ext.EventSampleRate, t.analyticsRate))
	}
	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.query", opts...)

	return ctx
}

// TraceQueryEnd traces the end of the query, implementing pgx.QueryTracer
func (t tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, exists := ddtracer.SpanFromContext(ctx)
	if !exists {
		return
	}

	if t.traceStatus {
		span.SetTag("pq.status", data.CommandTag.String())
	}

	if data.Err != nil {
		span.SetTag(ext.Error, data.Err)
	}
	span.Finish()
}
