package tracer

import (
	"errors"
	"testing"
	"time"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/tracer/ext"
)

// newOpenSpan is the OpenTracing Span constructor
func newOpenSpan(operationName string) *span {
	span := newSpan(operationName, "", "", 0, 0, 0, DefaultTracer)
	span.context = &spanContext{
		traceID:  span.TraceID,
		spanID:   span.SpanID,
		parentID: span.ParentID,
		sampled:  span.Sampled,
		span:     span,
	}
	return span
}

func TestOpenSpanBaggage(t *testing.T) {
	assert := assert.New(t)

	span := newOpenSpan("web.request")
	span.SetBaggageItem("key", "value")
	assert.Equal("value", span.BaggageItem("key"))
}

func TestOpenSpanContext(t *testing.T) {
	assert := assert.New(t)

	span := newOpenSpan("web.request")
	assert.NotNil(span.Context())
}

func TestOpenSpanOperationName(t *testing.T) {
	assert := assert.New(t)

	span := newOpenSpan("web.request")
	span.SetOperationName("http.request")
	assert.Equal("http.request", span.Name)
}

func TestOpenSpanFinish(t *testing.T) {
	assert := assert.New(t)

	span := newOpenSpan("web.request")
	span.Finish()

	assert.True(span.Duration > 0)
}

func TestOpenSpanFinishWithTime(t *testing.T) {
	assert := assert.New(t)

	finishTime := time.Now().Add(10 * time.Second)
	span := newOpenSpan("web.request")
	span.FinishWithOptions(opentracing.FinishOptions{FinishTime: finishTime})

	duration := finishTime.UnixNano() - span.Start
	assert.Equal(duration, span.Duration)
}

func TestOpenSpanSetTag(t *testing.T) {
	assert := assert.New(t)

	span := newOpenSpan("web.request")
	span.SetTag("component", "tracer")
	assert.Equal("tracer", span.Meta["component"])

	span.SetTag("tagInt", 1234)
	assert.Equal("1234", span.Meta["tagInt"])
}

func TestOpenSpanSetDatadogTags(t *testing.T) {
	assert := assert.New(t)

	span := newOpenSpan("web.request")
	span.SetTag("span.type", "http")
	span.SetTag("service.name", "db-cluster")
	span.SetTag("resource.name", "SELECT * FROM users;")

	assert.Equal("http", span.Type)
	assert.Equal("db-cluster", span.Service)
	assert.Equal("SELECT * FROM users;", span.Resource)
}

func TestSpanStart(t *testing.T) {
	assert := assert.New(t)
	tracer := New(WithTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// a new span sets the Start after the initialization
	assert.NotEqual(int64(0), span.Start)
}

func TestSpanString(t *testing.T) {
	assert := assert.New(t)
	tracer := New(WithTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")
	// don't bother checking the contents, just make sure it works.
	assert.NotEqual("", span.String())
	span.Finish()
	assert.NotEqual("", span.String())
}

func TestSpanSetTag(t *testing.T) {
	assert := assert.New(t)
	tracer := New(WithTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the map is properly initialized
	span.SetTag("status.code", "200")
	assert.Equal("200", span.Meta["status.code"])

	// operating on a finished span is a no-op
	nMeta := len(span.Meta)
	span.Finish()
	span.SetTag("finished.test", "true")
	assert.Equal(len(span.Meta), nMeta)
	assert.Equal(span.Meta["finished.test"], "")
}

func TestSpanSetMetric(t *testing.T) {
	assert := assert.New(t)
	tracer := New(WithTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the map is properly initialized
	span.setMetric("bytes", 1024.42)
	assert.Equal(1, len(span.Metrics))
	assert.Equal(1024.42, span.Metrics["bytes"])

	// operating on a finished span is a no-op
	span.Finish()
	span.setMetric("finished.test", 1337)
	assert.Equal(1, len(span.Metrics))
	assert.Equal(0.0, span.Metrics["finished.test"])
}

func TestSpanError(t *testing.T) {
	assert := assert.New(t)
	tracer := New(WithTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err := errors.New("Something wrong")
	span.SetError(err)
	assert.Equal(int32(1), span.Error)
	assert.Equal("Something wrong", span.Meta["error.msg"])
	assert.Equal("*errors.errorString", span.Meta["error.type"])
	assert.NotEqual("", span.Meta["error.stack"])

	// operating on a finished span is a no-op
	span = tracer.newRootSpan("flask.request", "flask", "/")
	nMeta := len(span.Meta)
	span.Finish()
	span.SetError(err)
	assert.Equal(int32(0), span.Error)
	assert.Equal(nMeta, len(span.Meta))
	assert.Equal("", span.Meta["error.msg"])
	assert.Equal("", span.Meta["error.type"])
	assert.Equal("", span.Meta["error.stack"])
}

func TestSpanError_Typed(t *testing.T) {
	assert := assert.New(t)
	tracer := New(WithTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err := &boomError{}
	span.SetError(err)
	assert.Equal(int32(1), span.Error)
	assert.Equal("boom", span.Meta["error.msg"])
	assert.Equal("*tracer.boomError", span.Meta["error.type"])
	assert.NotEqual("", span.Meta["error.stack"])
}

func TestSpanErrorNil(t *testing.T) {
	assert := assert.New(t)
	tracer := New(WithTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// don't set the error if it's nil
	nMeta := len(span.Meta)
	span.SetError(nil)
	assert.Equal(int32(0), span.Error)
	assert.Equal(nMeta, len(span.Meta))
}

func TestSpanFinish(t *testing.T) {
	assert := assert.New(t)
	wait := time.Millisecond * 2
	tracer := New(WithTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

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
	defer tracer.Stop()

	assert.Len(tracer.channels.trace, 0)

	// the finish must be idempotent
	span := tracer.newRootSpan("pylons.request", "pylons", "/")
	time.Sleep(wait)
	span.Finish()
	assert.Len(tracer.channels.trace, 1)

	previousDuration := span.Duration
	time.Sleep(wait)
	span.Finish()
	assert.Equal(previousDuration, span.Duration)
	assert.Len(tracer.channels.trace, 1)
}

// Prior to a bug fix, this failed when running `go test -race`
func TestSpanModifyWhileFlushing(t *testing.T) {
	tracer, _ := getTestTracer()
	defer tracer.Stop()

	done := make(chan struct{})
	go func() {
		span := tracer.newRootSpan("pylons.request", "pylons", "/")
		span.Finish()
		// It doesn't make much sense to update the span after it's been finished,
		// but an error in a user's code could lead to this.
		span.SetTag("race_test", "true")
		span.setMetric("race_test2", 133.7)
		span.setMetric("race_test3", 133.7)
		span.SetError(errors.New("t"))
		done <- struct{}{}
	}()

	run := true
	for run {
		select {
		case <-done:
			run = false
		default:
			tracer.flushTraces()
		}
	}
}

func TestSpanSamplingPriority(t *testing.T) {
	assert := assert.New(t)
	tracer := New(WithTransport(newDefaultTransport()))

	span := tracer.newRootSpan("my.name", "my.service", "my.resource")
	assert.Equal(0.0, span.Metrics["_sampling_priority_v1"], "default sampling priority if undefined is 0")
	assert.False(span.hasSamplingPriority(), "by default, sampling priority is undefined")
	assert.Equal(0, span.getSamplingPriority(), "default sampling priority for root spans is 0")

	childSpan := tracer.newChildSpan("my.child", span)
	assert.Equal(span.Metrics["_sampling_priority_v1"], childSpan.Metrics["_sampling_priority_v1"])
	assert.Equal(span.hasSamplingPriority(), childSpan.hasSamplingPriority())
	assert.Equal(span.getSamplingPriority(), childSpan.getSamplingPriority())

	for _, priority := range []int{
		ext.PriorityUserReject,
		ext.PriorityAutoReject,
		ext.PriorityAutoKeep,
		ext.PriorityUserKeep,
		999, // not used yet, but we should allow it
	} {
		span.setSamplingPriority(priority)
		assert.True(span.hasSamplingPriority())
		assert.Equal(priority, span.getSamplingPriority())
		childSpan = tracer.newChildSpan("my.child", span)
		assert.Equal(span.Metrics["_sampling_priority_v1"], childSpan.Metrics["_sampling_priority_v1"])
		assert.Equal(span.hasSamplingPriority(), childSpan.hasSamplingPriority())
		assert.Equal(span.getSamplingPriority(), childSpan.getSamplingPriority())
	}
}

type boomError struct{}

func (e *boomError) Error() string { return "boom" }
