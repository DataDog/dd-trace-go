package tracer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSpanStart(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")

	// a new span sets the Start after the initialization
	assert.NotEqual(span.Start, int64(0))
}

func TestSpanString(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")
	// don't bother checking the contents, just make sure it works.
	assert.NotEqual("", span.String())
	span.Finish()
	assert.NotEqual("", span.String())
}

func TestSpanSetMeta(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")

	// check the map is properly initialized
	span.SetMeta("status.code", "200")
	assert.Equal(len(span.Meta), 1)
	assert.Equal(span.Meta["status.code"], "200")

	// operating on a finished span is a no-op
	span.finished = true
	span.SetMeta("finished.test", "true")
	assert.Equal(len(span.Meta), 1)
	assert.Equal(span.Meta["finished.test"], "")
}

func TestSpanSetMetric(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")

	// check the map is properly initialized
	span.SetMetric("bytes", 1024.42)
	assert.Equal(len(span.Metrics), 1)
	assert.Equal(span.Metrics["bytes"], 1024.42)

	// operating on a finished span is a no-op
	span.finished = true
	span.SetMetric("finished.test", 1337)
	assert.Equal(len(span.Metrics), 1)
	assert.Equal(span.Metrics["finished.test"], 0.0)
}

func TestSpanError(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err := errors.New("Something wrong")
	span.SetError(err)
	assert.Equal(span.Error, int32(1))
	assert.Equal(len(span.Meta), 3)
	assert.Equal(span.Meta["error.msg"], "Something wrong")
	assert.Equal(span.Meta["error.type"], "*errors.errorString")

	// operating on a finished span is a no-op
	span = tracer.NewRootSpan("flask.request", "flask", "/")
	span.finished = true
	span.SetError(err)
	assert.Equal(span.Error, int32(0))
	assert.Equal(len(span.Meta), 0)
	assert.Equal(span.Meta["error.msg"], "")
	assert.Equal(span.Meta["error.type"], "")
}

func TestSpanError_Typed(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err := &boomError{}
	span.SetError(err)
	assert.Equal(span.Error, int32(1))
	assert.Equal(len(span.Meta), 3)
	assert.Equal(span.Meta["error.msg"], "boom")
	assert.Equal(span.Meta["error.type"], "*tracer.boomError")
}

func TestEmptySpan(t *testing.T) {
	// ensure the empty span won't crash the app
	var span Span
	span.SetMeta("a", "b")
	span.SetError(nil)
	span.Finish()

	var s *Span
	s.SetMeta("a", "b")
	s.SetError(nil)
	s.Finish()
}

func TestSpanErrorNil(t *testing.T) {
	assert := assert.New(t)
	tracer := NewTracer()
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")

	// don't set the error if it's nil
	span.SetError(nil)
	assert.Equal(span.Error, int32(0))
	assert.Equal(len(span.Meta), 0)
}

func TestSpanFinish(t *testing.T) {
	assert := assert.New(t)
	wait := time.Millisecond * 2
	tracer := NewTracer()
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")

	// the finish should set finished and the duration
	time.Sleep(wait)
	span.Finish()
	assert.True(span.Duration > int64(wait))
	assert.True(span.finished)
}

func TestSpanFinishTwice(t *testing.T) {
	assert := assert.New(t)
	wait := time.Millisecond * 2

	tracer, _ := getTestTracer()

	assert.Equal(tracer.buffer.Len(), 0)

	// the finish must be idempotent
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")
	time.Sleep(wait)
	span.Finish()
	assert.Equal(tracer.buffer.Len(), 1)

	previousDuration := span.Duration
	time.Sleep(wait)
	span.Finish()
	assert.Equal(span.Duration, previousDuration)
	assert.Equal(tracer.buffer.Len(), 1)
}

func TestSpanContext(t *testing.T) {
	ctx := context.Background()
	_, ok := SpanFromContext(ctx)
	assert.False(t, ok)

	tracer := NewTracer()
	span := tracer.NewRootSpan("pylons.request", "pylons", "/")

	ctx = span.Context(ctx)
	s2, ok := SpanFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, s2.SpanID, span.SpanID)

}

// Prior to a bug fix, this failed when running `go test -race`
func TestSpanModifyWhileFlushing(t *testing.T) {
	tracer, _ := getTestTracer()

	done := make(chan struct{})
	go func() {
		span := tracer.NewRootSpan("pylons.request", "pylons", "/")
		span.Finish()
		// It doesn't make much sense to update the span after it's been finished,
		// but an error in a user's code could lead to this.
		span.SetMeta("race_test", "true")
		span.SetMetric("race_test2", 133.7)
		span.SetMetrics("race_test3", 133.7)
		span.SetError(errors.New("t"))
		done <- struct{}{}
	}()

	run := true
	for run {
		select {
		case <-done:
			run = false
		default:
			tracer.FlushTraces()
		}
	}
}

type boomError struct{}

func (e *boomError) Error() string { return "boom" }
