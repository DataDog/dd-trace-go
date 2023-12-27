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

// TraceConnectStart marks the start of a pgx connect operation, implementing pgx.ConnectTracer
func (t *tracer) TraceConnectStart(ctx context.Context, _ pgx.TraceConnectStartData) context.Context {
	opts := []ddtrace.StartSpanOption{
		ddtracer.ServiceName(t.serviceName),
		ddtracer.SpanType(ext.SpanTypeSQL),
		ddtracer.StartTime(time.Now()),
		ddtracer.Tag("sql.query_type", "Connect"),
	}
	for key, tag := range t.tags {
		opts = append(opts, ddtracer.Tag(key, tag))
	}
	if !math.IsNaN(t.analyticsRate) {
		opts = append(opts, ddtracer.Tag(ext.EventSampleRate, t.analyticsRate))
	}
	_, ctx = ddtracer.StartSpanFromContext(ctx, "pgx.connect", opts...)

	return ctx
}

// TraceConnectEnd marks the end of a pgx connect operation, implementing pgx.ConnectTracer
func (t *tracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	span, exists := ddtracer.SpanFromContext(ctx)
	if !exists {
		return
	}

	if data.Err != nil {
		span.SetTag(ext.Error, data.Err)
	}
	span.Finish()
}
