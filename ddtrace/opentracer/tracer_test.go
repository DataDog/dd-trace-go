package opentracer

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/internal"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/assert"
)

func TestStart(t *testing.T) {
	assert := assert.New(t)
	Start()
	dd, ok := internal.GlobalTracer.(ddtrace.Tracer)
	assert.True(ok)
	ot, ok := opentracing.GlobalTracer().(*opentracer)
	assert.True(ok)
	assert.Equal(ot.Tracer, dd)
}
