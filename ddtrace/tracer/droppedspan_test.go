package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDroppedSpan(t *testing.T) {
	assert := assert.New(t)
	ds := &droppedSpan{traceID: 1}
	assert.Nil(ds.sctx)
	ctx := ds.Context().(*droppedSpanContext)
	assert.NotNil(ds.sctx)
	assert.EqualValues(1, ctx.traceID)
	assert.EqualValues(1, ctx.spanID)
	assert.EqualValues(-1, *ctx.trace.priority)
}
