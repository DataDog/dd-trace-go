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

// TracePrepareStart marks the start of a pgx prepare operation, implementing pgx.PrepareTracer
func (t *tracer) TracePrepareStart(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	opts := []ddtrace.StartSpanOption{
		ddtracer.ServiceName(t.serviceName),
		ddtracer.SpanType(ext.SpanTypeSQL),
		ddtracer.StartTime(time.Now()),
		ddtracer.Tag("sql.query_type", "Prepare"),
		ddtracer.Tag("pgx.prepared_statement_name", data.Name),
		ddtracer.Tag(ext.ResourceName, data.SQL),
	}
	for key, tag := range t.tags {
		opts = append(opts, ddtracer.Tag(key, tag))
	}
	if !math.IsNaN(t.analyticsRate) {
		opts = append(opts, ddtracer.Tag(ext.EventSampleRate, t.analyticsRate))
	}
	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.prepare", opts...)

	return ctx
}

// TracePrepareEnd marks the end of a pgx prepare operation, implementing pgx.PrepareTracer
func (t *tracer) TracePrepareEnd(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareEndData) {
	span, exists := ddtracer.SpanFromContext(ctx)
	if !exists {
		return
	}

	span.SetTag("pgx.already_prepared", data.AlreadyPrepared)

	if data.Err != nil {
		span.SetTag(ext.Error, data.Err)
	}
	span.Finish()
}
