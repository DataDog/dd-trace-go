package tracer

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSpanStart(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")

	// a new span sets the Start after the initialization
	assert.NotEqual(span.Start, int64(0))
}

func TestSpanString(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")
	// don't bother checking the contents, just make sure it works.
	assert.NotEqual("", span.String())
	span.Finish()
	assert.NotEqual("", span.String())
}

func TestSpanSetMeta(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")

	// check the map is properly initialized
	span.SetMeta("status.code", "200")
	assert.Equal(len(span.Meta), 1)
	assert.Equal(span.Meta["status.code"], "200")
}

func TestSpanSetMetrics(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")

	// check the map is properly initialized
	span.SetMetrics("bytes", 1024.42)
	assert.Equal(len(span.Metrics), 1)
	assert.Equal(span.Metrics["bytes"], 1024.42)
}

func TestSpanError(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err := errors.New("Something wrong")
	span.SetError(err)
	assert.Equal(span.Error, int32(1))
	assert.Equal(len(span.Meta), 1)
	assert.Equal(span.Meta["error.msg"], "Something wrong")
}

func TestEmptySpan(t *testing.T) {
	// ensure the empty span won't crash the app
	var span Span
	span.SetMeta("a", "b")
	span.Finish()

	var s *Span
	s.SetMeta("a", "b")
	s.SetError(nil)
	s.Finish()
}

func TestSpanErrorNil(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")

	// don't set the error if it's nil
	span.SetError(nil)
	assert.Equal(span.Error, int32(0))
	assert.Equal(len(span.Meta), 0)
}

func TestSpanErrorMeta(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")

	// check the error is set (but not the Error field)
	// using a custom meta
	err := errors.New("Something wrong")
	span.SetErrorMeta("cache_get", err)
	assert.Equal(span.Error, int32(0))
	assert.Equal(len(span.Meta), 1)
	assert.Equal(span.Meta["cache_get"], "Something wrong")
}

func TestSpanErrorMetaNil(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")

	// don't set the error if it's nil
	span.SetErrorMeta("cache_get", nil)
	assert.Equal(span.Error, int32(0))
	assert.Equal(len(span.Meta), 0)
}

func TestSpanIsFinished(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")

	assert.False(span.IsFinished())
	// a span is finished if the duration is greater than 0
	span.Duration = 1
	assert.True(span.IsFinished())
}

func TestSpanFinish(t *testing.T) {
	assert := assert.New(t)
	wait := time.Millisecond * 2
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")

	// the finish should set the duration
	time.Sleep(wait)
	span.Finish()
	assert.True(span.IsFinished())
	assert.True(span.Duration > int64(wait))
}

func TestSpanFinishTwice(t *testing.T) {
	assert := assert.New(t)
	wait := time.Millisecond * 2
	tracer := NewTracer()
	span := tracer.NewSpan("pylons.request", "pylons", "/")

	// the finish must be idempotent
	time.Sleep(wait)
	span.Finish()
	previousDuration := span.Duration
	time.Sleep(wait)
	span.Finish()
	assert.Equal(span.Duration, previousDuration)
}
