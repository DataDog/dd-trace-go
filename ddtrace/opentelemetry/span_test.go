package opentelemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestSpanMethods(t *testing.T) {
	assert := assert.New(t)
	otel.SetTracerProvider(NewTracerProvider(tracer.WithEnv("wrapper_env")))
	tr := otel.Tracer("ot",
		oteltrace.WithInstrumentationVersion("0.1"))
	ctx, sp := tr.Start(context.Background(), "otel.test")
	got, ok := tracer.SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(got, sp.(*span).Span)
}
