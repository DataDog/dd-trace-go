package tracer

import (
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"testing"
)


func TestBinaryPropagatorInjectExtract(t *testing.T) {
	propagator := new(BinaryPropagator)

	tracer := newTracer(WithPropagator(propagator))
	root := tracer.StartSpan("web.request").(*span)
	root.SetTag(ext.SamplingPriority, -1)
	root.SetBaggageItem("item", "x")
	ctx := root.Context().(*spanContext)
	
	carrier := ""

	err := tracer.Inject(ctx, &carrier)

	assert := assert.New(t)
	assert.Nil(err)

	sctx, err := tracer.Extract(carrier)
	assert.Nil(err)

	xctx, ok := sctx.(*spanContext)
	assert.True(ok)
	assert.Equal(xctx.traceID, ctx.traceID)
	assert.Equal(xctx.spanID, ctx.spanID)
	assert.Equal(xctx.baggage, ctx.baggage)
	assert.Equal(xctx.priority, ctx.priority)
	assert.Equal(xctx.hasPriority, ctx.hasPriority)
}


