// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/stretchr/testify/assert"
)

// newSpan creates a new span. This is a low-level function, required for testing and advanced usage.
// Most of the time one should prefer the Tracer NewRootSpan or NewChildSpan methods.
func newSpan(name, service, resource string, spanID, traceID, parentID uint64) *span {
	span := &span{
		Name:     name,
		Service:  service,
		Resource: resource,
		Meta:     map[string]string{},
		Metrics:  map[string]float64{},
		SpanID:   spanID,
		TraceID:  traceID,
		ParentID: parentID,
		Start:    now(),
	}
	span.context = newSpanContext(span, nil)
	return span
}

// newBasicSpan is the OpenTracing Span constructor
func newBasicSpan(operationName string) *span {
	return newSpan(operationName, "", "", 0, 0, 0)
}

func TestSpanBaggage(t *testing.T) {
	assert := assert.New(t)

	span := newBasicSpan("web.request")
	span.SetBaggageItem("key", "value")
	assert.Equal("value", span.BaggageItem("key"))
}

func TestSpanContext(t *testing.T) {
	assert := assert.New(t)

	span := newBasicSpan("web.request")
	assert.NotNil(span.Context())
}

func TestSpanOperationName(t *testing.T) {
	assert := assert.New(t)

	span := newBasicSpan("web.request")
	span.SetOperationName("http.request")
	assert.Equal("http.request", span.Name)
}

func TestSpanFinish(t *testing.T) {
	assert := assert.New(t)
	wait := time.Millisecond * 2
	tracer := newTracer(withTransport(newDefaultTransport()))
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

	tracer, _, stop := startTestTracer()
	defer stop()

	assert.Equal(tracer.payload.itemCount(), 0)

	// the finish must be idempotent
	span := tracer.newRootSpan("pylons.request", "pylons", "/")
	time.Sleep(wait)
	span.Finish()
	assert.Equal(tracer.payload.itemCount(), 1)

	previousDuration := span.Duration
	time.Sleep(wait)
	span.Finish()
	assert.Equal(previousDuration, span.Duration)
	assert.Equal(tracer.payload.itemCount(), 1)
}

func TestSpanFinishWithTime(t *testing.T) {
	assert := assert.New(t)

	finishTime := time.Now().Add(10 * time.Second)
	span := newBasicSpan("web.request")
	span.Finish(FinishTime(finishTime))

	duration := finishTime.UnixNano() - span.Start
	assert.Equal(duration, span.Duration)
}

func TestSpanFinishWithError(t *testing.T) {
	assert := assert.New(t)

	err := errors.New("test error")
	span := newBasicSpan("web.request")
	span.Finish(WithError(err))

	assert.Equal(int32(1), span.Error)
	assert.Equal("test error", span.Meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.Meta[ext.ErrorType])
	assert.NotEmpty(span.Meta[ext.ErrorStack])
}

func TestSpanFinishWithErrorNoDebugStack(t *testing.T) {
	assert := assert.New(t)

	err := errors.New("test error")
	span := newBasicSpan("web.request")
	span.Finish(WithError(err), NoDebugStack())

	assert.Equal(int32(1), span.Error)
	assert.Equal("test error", span.Meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.Meta[ext.ErrorType])
	assert.Empty(span.Meta[ext.ErrorStack])
}

func TestSpanFinishWithErrorStackFrames(t *testing.T) {
	assert := assert.New(t)

	err := errors.New("test error")
	span := newBasicSpan("web.request")
	span.Finish(WithError(err), StackFrames(2, 1))

	assert.Equal(int32(1), span.Error)
	assert.Equal("test error", span.Meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.Meta[ext.ErrorType])
	assert.Contains(span.Meta[ext.ErrorStack], "tracer.TestSpanFinishWithErrorStackFrames")
	assert.Contains(span.Meta[ext.ErrorStack], "tracer.(*span).Finish")
	assert.Equal(strings.Count(span.Meta[ext.ErrorStack], "\n\t"), 2)
}

func TestSpanSetTag(t *testing.T) {
	assert := assert.New(t)

	span := newBasicSpan("web.request")
	span.SetTag("component", "tracer")
	assert.Equal("tracer", span.Meta["component"])

	span.SetTag("tagInt", 1234)
	assert.Equal(float64(1234), span.Metrics["tagInt"])

	span.SetTag("tagStruct", struct{ A, B int }{1, 2})
	assert.Equal("{1 2}", span.Meta["tagStruct"])

	span.SetTag(ext.Error, true)
	assert.Equal(int32(1), span.Error)

	span.SetTag(ext.Error, nil)
	assert.Equal(int32(0), span.Error)

	span.SetTag(ext.Error, errors.New("abc"))
	assert.Equal(int32(1), span.Error)
	assert.Equal("abc", span.Meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.Meta[ext.ErrorType])
	assert.NotEmpty(span.Meta[ext.ErrorStack])

	span.SetTag(ext.Error, "something else")
	assert.Equal(int32(1), span.Error)

	span.SetTag(ext.Error, false)
	assert.Equal(int32(0), span.Error)

	span.SetTag(ext.SamplingPriority, 2)
	assert.Equal(float64(2), span.Metrics[keySamplingPriority])

	span.SetTag(ext.AnalyticsEvent, true)
	assert.Equal(1.0, span.Metrics[ext.EventSampleRate])

	span.SetTag(ext.AnalyticsEvent, false)
	assert.Equal(0.0, span.Metrics[ext.EventSampleRate])

	span.SetTag(ext.ManualDrop, true)
	assert.Equal(-1., span.Metrics[keySamplingPriority])

	span.SetTag(ext.ManualKeep, true)
	assert.Equal(2., span.Metrics[keySamplingPriority])

	span.SetTag("some.bool", true)
	assert.Equal("true", span.Meta["some.bool"])

	span.SetTag("some.other.bool", false)
	assert.Equal("false", span.Meta["some.other.bool"])
}

func TestSpanSetDatadogTags(t *testing.T) {
	assert := assert.New(t)

	span := newBasicSpan("web.request")
	span.SetTag(ext.SpanType, "http")
	span.SetTag(ext.ServiceName, "db-cluster")
	span.SetTag(ext.ResourceName, "SELECT * FROM users;")

	assert.Equal("http", span.Type)
	assert.Equal("db-cluster", span.Service)
	assert.Equal("SELECT * FROM users;", span.Resource)
}

func TestSpanStart(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// a new span sets the Start after the initialization
	assert.NotEqual(int64(0), span.Start)
}

func TestSpanString(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")
	// don't bother checking the contents, just make sure it works.
	assert.NotEqual("", span.String())
	span.Finish()
	assert.NotEqual("", span.String())
}

const (
	intUpperLimit = int64(1) << 53
	intLowerLimit = -intUpperLimit
)

func TestSpanSetMetric(t *testing.T) {
	for name, tt := range map[string]func(assert *assert.Assertions, span *span){
		"init": func(assert *assert.Assertions, span *span) {
			assert.Equal(2, len(span.Metrics))
			_, ok := span.Metrics[keySamplingPriority]
			assert.True(ok)
			_, ok = span.Metrics[keySamplingPriorityRate]
			assert.True(ok)
		},
		"float": func(assert *assert.Assertions, span *span) {
			span.SetTag("temp", 72.42)
			assert.Equal(72.42, span.Metrics["temp"])
		},
		"int": func(assert *assert.Assertions, span *span) {
			span.SetTag("bytes", 1024)
			assert.Equal(1024.0, span.Metrics["bytes"])
		},
		"max": func(assert *assert.Assertions, span *span) {
			span.SetTag("bytes", intUpperLimit-1)
			assert.Equal(float64(intUpperLimit-1), span.Metrics["bytes"])
		},
		"min": func(assert *assert.Assertions, span *span) {
			span.SetTag("bytes", intLowerLimit+1)
			assert.Equal(float64(intLowerLimit+1), span.Metrics["bytes"])
		},
		"toobig": func(assert *assert.Assertions, span *span) {
			span.SetTag("bytes", intUpperLimit)
			assert.Equal(0.0, span.Metrics["bytes"])
			assert.Equal(fmt.Sprint(intUpperLimit), span.Meta["bytes"])
		},
		"toosmall": func(assert *assert.Assertions, span *span) {
			span.SetTag("bytes", intLowerLimit)
			assert.Equal(0.0, span.Metrics["bytes"])
			assert.Equal(fmt.Sprint(intLowerLimit), span.Meta["bytes"])
		},
		"finished": func(assert *assert.Assertions, span *span) {
			span.Finish()
			span.SetTag("finished.test", 1337)
			assert.Equal(2, len(span.Metrics))
			_, ok := span.Metrics["finished.test"]
			assert.False(ok)
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			tracer := newTracer(withTransport(newDefaultTransport()))
			span := tracer.newRootSpan("http.request", "mux.router", "/")
			tt(assert, span)
		})
	}
}

func TestSpanError(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err := errors.New("Something wrong")
	span.SetTag(ext.Error, err)
	assert.Equal(int32(1), span.Error)
	assert.Equal("Something wrong", span.Meta["error.msg"])
	assert.Equal("*errors.errorString", span.Meta["error.type"])
	assert.NotEqual("", span.Meta["error.stack"])

	// operating on a finished span is a no-op
	span = tracer.newRootSpan("flask.request", "flask", "/")
	nMeta := len(span.Meta)
	span.Finish()
	span.SetTag(ext.Error, err)
	assert.Equal(int32(0), span.Error)
	assert.Equal(nMeta, len(span.Meta))
	assert.Equal("", span.Meta["error.msg"])
	assert.Equal("", span.Meta["error.type"])
	assert.Equal("", span.Meta["error.stack"])
}

func TestSpanError_Typed(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err := &boomError{}
	span.SetTag(ext.Error, err)
	assert.Equal(int32(1), span.Error)
	assert.Equal("boom", span.Meta["error.msg"])
	assert.Equal("*tracer.boomError", span.Meta["error.type"])
	assert.NotEqual("", span.Meta["error.stack"])
}

func TestSpanErrorNil(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// don't set the error if it's nil
	nMeta := len(span.Meta)
	span.SetTag(ext.Error, nil)
	assert.Equal(int32(0), span.Error)
	assert.Equal(nMeta, len(span.Meta))
}

// Prior to a bug fix, this failed when running `go test -race`
func TestSpanModifyWhileFlushing(t *testing.T) {
	tracer, _, stop := startTestTracer()
	defer stop()

	done := make(chan struct{})
	go func() {
		span := tracer.newRootSpan("pylons.request", "pylons", "/")
		span.Finish()
		// It doesn't make much sense to update the span after it's been finished,
		// but an error in a user's code could lead to this.
		span.SetTag("race_test", "true")
		span.SetTag("race_test2", 133.7)
		span.SetTag("race_test3", 133.7)
		span.SetTag(ext.Error, errors.New("t"))
		done <- struct{}{}
	}()

	for {
		select {
		case <-done:
			return
		default:
			tracer.forceFlush()
		}
	}
}

func TestSpanSamplingPriority(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))

	span := tracer.newRootSpan("my.name", "my.service", "my.resource")
	_, ok := span.Metrics[keySamplingPriority]
	assert.True(ok)
	_, ok = span.Metrics[keySamplingPriorityRate]
	assert.True(ok)

	for _, priority := range []int{
		ext.PriorityUserReject,
		ext.PriorityAutoReject,
		ext.PriorityAutoKeep,
		ext.PriorityUserKeep,
		999, // not used, but we should allow it
	} {
		span.SetTag(ext.SamplingPriority, priority)
		v, ok := span.Metrics[keySamplingPriority]
		assert.True(ok)
		assert.EqualValues(priority, v)
		assert.EqualValues(*span.context.trace.priority, v)

		childSpan := tracer.newChildSpan("my.child", span)
		v0, ok0 := span.Metrics[keySamplingPriority]
		v1, ok1 := childSpan.Metrics[keySamplingPriority]
		assert.Equal(ok0, ok1)
		assert.Equal(v0, v1)
		assert.EqualValues(*childSpan.context.trace.priority, v0)
	}
}

func BenchmarkSetTagMetric(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := string(keys[i%len(keys)])
		span.SetTag(k, float64(12.34))
	}
}

func BenchmarkSetTagString(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := string(keys[i%len(keys)])
		span.SetTag(k, "some text")
	}
}

func BenchmarkSetTagField(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := []string{ext.ServiceName, ext.ResourceName, ext.SpanType}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%len(keys)]
		span.SetTag(k, "some text")
	}
}

type boomError struct{}

func (e *boomError) Error() string { return "boom" }
