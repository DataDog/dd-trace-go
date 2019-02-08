package tracer

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

	"github.com/stretchr/testify/assert"
)

func TestStart(t *testing.T) {
	assert := assert.New(t)
	ot := NewOpenTracer()
	dd, ok := internal.GetGlobalTracer().(ddtrace.Tracer)
	assert.True(ok)
	ott, ok := ot.(*opentracer)
	assert.True(ok)
	assert.Equal(ott.Tracer, dd)
}
