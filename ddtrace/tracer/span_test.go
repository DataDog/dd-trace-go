// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"

	"github.com/DataDog/datadog-agent/pkg/obfuscate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSpan creates a new span. This is a low-level function, required for testing and advanced usage.
// Most of the time one should prefer the Tracer NewRootSpan or NewChildSpan methods.
func newSpan(name, service, resource string, spanID, traceID, parentID uint64) *Span {
	span := &Span{
		name:     name,
		service:  service,
		resource: resource,
		meta:     map[string]string{},
		metrics:  map[string]float64{},
		spanID:   spanID,
		traceID:  traceID,
		parentID: parentID,
		start:    now(),
	}
	span.context = newSpanContext(span, nil)
	return span
}

// newBasicSpan is the OpenTracing Span constructor
func newBasicSpan(operationName string) *Span {
	return newSpan(operationName, "", "", 0, 0, 0)
}

func TestSpanAsMap(t *testing.T) {
	assertions := assert.New(t)
	for _, tt := range []struct {
		name string
		span *Span
		want any
	}{
		{
			name: "basic",
			span: newBasicSpan("my.op"),
			want: "my.op",
		},
		{
			name: "nil span",
			span: nil,
			want: nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertions.Equal(tt.want, tt.span.AsMap()[ext.SpanName])
		})
	}
}

func TestNilSpan(t *testing.T) {
	assertions := assert.New(t)
	var (
		span *Span
		ctx  = span.Context()
	)
	// nil span should return a nil context
	assertions.Nil(ctx)
	assertions.Equal(TraceIDZero, ctx.TraceID())
	assertions.Equal([16]byte(emptyTraceID), ctx.TraceIDBytes())
	assertions.Equal(uint64(0), ctx.SpanID())
	sp, ok := ctx.SamplingPriority()
	assertions.Equal(0, sp)
	assertions.Equal(false, ok)
	// calls on nil span should be no-op
	assertions.Nil(span.Root())
	span.SetBaggageItem("key", "value")
	span.SetTag("key", "value")
	span.SetUser("user")
	assertions.Nil(span.StartChild("child"))
	span.Finish()
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
	assert.Equal("http.request", span.name)
}

func TestSpanFinish(t *testing.T) {
	if strings.HasPrefix(runtime.GOOS, "windows") {
		t.Skip("Windows' sleep is not precise enough for this test.")
	}

	assert := assert.New(t)
	wait := time.Millisecond * 2
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	assert.NoError(err)
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// the finish should set finished and the duration
	time.Sleep(wait)
	span.Finish()
	assert.Greater(span.duration, int64(wait))
	assert.True(span.finished)
}

func TestSpanFinishTwice(t *testing.T) {
	assert := assert.New(t)
	wait := time.Millisecond * 2

	tracer, _, _, stop, err := startTestTracer(t)
	assert.Nil(err)
	defer stop()

	assert.Equal(tracer.traceWriter.(*agentTraceWriter).payload.itemCount(), 0)

	// the finish must be idempotent
	span := tracer.newRootSpan("pylons.request", "pylons", "/")
	time.Sleep(wait)
	span.Finish()
	tracer.awaitPayload(t, 1)

	previousDuration := span.duration
	time.Sleep(wait)
	span.Finish()
	assert.Equal(previousDuration, span.duration)
	tracer.awaitPayload(t, 1)
}

func TestShouldDrop(t *testing.T) {
	for _, tt := range []struct {
		prio   int
		errors int32
		rate   float64
		want   bool
	}{
		{1, 0, 0, true},
		{2, 1, 0, true},
		{0, 1, 0, true},
		{0, 0, 1, true},
		{0, 0, 0.5, true},
		{0, 0, 0.00001, false},
		{0, 0, 0, false},
	} {
		t.Run("", func(t *testing.T) {
			s := newSpan("", "", "", 1, 1, 0)
			s.SetTag(ext.SamplingPriority, tt.prio)
			s.SetTag(ext.EventSampleRate, tt.rate)
			atomic.StoreInt32(&s.context.errors, tt.errors)
			assert.Equal(t, shouldKeep(s), tt.want)
		})
	}

	t.Run("none", func(t *testing.T) {
		s := newSpan("", "", "", 1, 1, 0)
		assert.Equal(t, shouldKeep(s), false)
	})
}

func TestShouldComputeStats(t *testing.T) {
	for _, tt := range []struct {
		metrics map[string]float64
		want    bool
	}{
		{map[string]float64{keyMeasured: 2}, false},
		{map[string]float64{keyMeasured: 1}, true},
		{map[string]float64{keyMeasured: 0}, false},
		{map[string]float64{keyTopLevel: 0}, false},
		{map[string]float64{keyTopLevel: 1}, true},
		{map[string]float64{keyTopLevel: 2}, false},
		{map[string]float64{keyTopLevel: 2, keyMeasured: 1}, true},
		{map[string]float64{keyTopLevel: 1, keyMeasured: 2}, true},
		{map[string]float64{keyTopLevel: 2, keyMeasured: 2}, false},
		{map[string]float64{}, false},
	} {
		t.Run("", func(t *testing.T) {
			assert.Equal(t, shouldComputeStats(&Span{metrics: tt.metrics}), tt.want)
		})
	}
}

func TestNewAggregableSpan(t *testing.T) {
	t.Run("obfuscating", func(t *testing.T) {
		o := obfuscate.NewObfuscator(obfuscate.Config{})
		aggspan := newAggregableSpan(&Span{
			name:     "name",
			resource: "SELECT * FROM table WHERE password='secret'",
			service:  "service",
			spanType: "sql",
		}, o)
		assert.Equal(t, aggregation{
			Name:     "name",
			Type:     "sql",
			Resource: "SELECT * FROM table WHERE password = ?",
			Service:  "service",
		}, aggspan.key)
	})

	t.Run("nil-obfuscator", func(t *testing.T) {
		aggspan := newAggregableSpan(&Span{
			name:     "name",
			resource: "SELECT * FROM table WHERE password='secret'",
			service:  "service",
			spanType: "sql",
		}, nil)
		assert.Equal(t, aggregation{
			Name:     "name",
			Type:     "sql",
			Resource: "SELECT * FROM table WHERE password='secret'",
			Service:  "service",
		}, aggspan.key)
	})
}

func TestSpanFinishWithTime(t *testing.T) {
	assert := assert.New(t)

	finishTime := time.Now().Add(10 * time.Second)
	span := newBasicSpan("web.request")
	span.Finish(FinishTime(finishTime))

	duration := finishTime.UnixNano() - span.start
	assert.Equal(duration, span.duration)
}

func TestSpanFinishWithNegativeDuration(t *testing.T) {
	assert := assert.New(t)
	startTime := time.Now()
	finishTime := startTime.Add(-10 * time.Second)
	span := newBasicSpan("web.request")
	span.start = startTime.UnixNano()
	span.Finish(FinishTime(finishTime))
	assert.Equal(int64(0), span.duration)
}

func TestSpanFinishWithError(t *testing.T) {
	assert := assert.New(t)

	err := errors.New("test error")
	span := newBasicSpan("web.request")
	span.Finish(WithError(err))

	assert.Equal(int32(1), span.error)
	assert.Equal("test error", span.meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.meta[ext.ErrorType])
	assert.NotEmpty(span.meta[ext.ErrorStack])
}

func TestSpanFinishWithErrorNoDebugStack(t *testing.T) {
	assert := assert.New(t)

	err := errors.New("test error")
	span := newBasicSpan("web.request")
	span.Finish(WithError(err), NoDebugStack())

	assert.Equal(int32(1), span.error)
	assert.Equal("test error", span.meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.meta[ext.ErrorType])
	assert.Empty(span.meta[ext.ErrorStack])
}

func TestSpanFinishWithErrorStackFrames(t *testing.T) {
	assert := assert.New(t)

	err := errors.New("test error")
	span := newBasicSpan("web.request")
	span.Finish(WithError(err), StackFrames(2, 1))

	assert.Equal(int32(1), span.error)
	assert.Equal("test error", span.meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.meta[ext.ErrorType])
	assert.Contains(span.meta[ext.ErrorStack], "tracer.TestSpanFinishWithErrorStackFrames")
	assert.Contains(span.meta[ext.ErrorStack], "tracer.(*Span).Finish")
	assert.Equal(strings.Count(span.meta[ext.ErrorStack], "\n\t"), 2)
}

// nilStringer is used to test nil detection when setting tags.
type nilStringer struct {
	s string
}

// String incorrectly assumes than n will not be nil in order
// to ensure SetTag identifies nils.
func (n *nilStringer) String() string {
	return n.s
}

type panicStringer struct {
	s string
}

// String causes panic which SetTag should not handle.
func (p *panicStringer) String() string {
	panic("This should not be handled.")
}

func TestSpanSetTag(t *testing.T) {
	assert := assert.New(t)

	span := newBasicSpan("web.request")
	span.SetTag("component", "tracer")
	assert.Equal("tracer", span.meta["component"])

	span.SetTag("tagInt", 1234)
	assert.Equal(float64(1234), span.metrics["tagInt"])

	span.SetTag("tagStruct", struct{ A, B int }{1, 2})
	assert.Equal("{1 2}", span.meta["tagStruct"])

	span.SetTag(ext.Error, true)
	assert.Equal(int32(1), span.error)

	span.SetTag(ext.Error, nil)
	assert.Equal(int32(0), span.error)

	span.SetTag(ext.Error, errors.New("abc"))
	assert.Equal(int32(1), span.error)
	assert.Equal("abc", span.meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.meta[ext.ErrorType])
	assert.NotEmpty(span.meta[ext.ErrorStack])

	span.SetTag(ext.Error, "something else")
	assert.Equal(int32(1), span.error)

	span.SetTag(ext.Error, false)
	assert.Equal(int32(0), span.error)

	span.SetTag(ext.SamplingPriority, 2)
	assert.Equal(float64(2), span.metrics[keySamplingPriority])

	span.SetTag(ext.AnalyticsEvent, true)
	assert.Equal(1.0, span.metrics[ext.EventSampleRate])

	span.SetTag(ext.AnalyticsEvent, false)
	assert.Equal(0.0, span.metrics[ext.EventSampleRate])

	span.SetTag(ext.ManualDrop, true)
	assert.Equal(-1., span.metrics[keySamplingPriority])

	span.SetTag(ext.ManualKeep, true)
	assert.Equal(2., span.metrics[keySamplingPriority])

	span.SetTag("some.bool", true)
	assert.Equal("true", span.meta["some.bool"])

	span.SetTag("some.other.bool", false)
	assert.Equal("false", span.meta["some.other.bool"])

	span.SetTag("time", (*time.Time)(nil))
	assert.Equal("<nil>", span.meta["time"])

	span.SetTag("nilStringer", (*nilStringer)(nil))
	assert.Equal("<nil>", span.meta["nilStringer"])

	span.SetTag("somestrings", []string{"foo", "bar"})
	assert.Equal("foo", span.meta["somestrings.0"])
	assert.Equal("bar", span.meta["somestrings.1"])

	span.SetTag("somebools", []bool{true, false})
	assert.Equal("true", span.meta["somebools.0"])
	assert.Equal("false", span.meta["somebools.1"])

	span.SetTag("somenums", []int{-1, 5, 2})
	assert.Equal(-1., span.metrics["somenums.0"])
	assert.Equal(5., span.metrics["somenums.1"])
	assert.Equal(2., span.metrics["somenums.2"])

	span.SetTag("someslices", [][]string{{"a, b, c"}, {"d"}, nil, {"e, f"}})
	assert.Equal("[a, b, c]", span.meta["someslices.0"])
	assert.Equal("[d]", span.meta["someslices.1"])
	assert.Equal("[]", span.meta["someslices.2"])
	assert.Equal("[e, f]", span.meta["someslices.3"])

	assert.Panics(func() {
		span.SetTag("panicStringer", &panicStringer{})
	})
}

func TestSpanSetTagError(t *testing.T) {
	assert := assert.New(t)

	t.Run("off", func(t *testing.T) {
		span := newBasicSpan("web.request")
		span.setTagError(errors.New("error value with no trace"), errorConfig{noDebugStack: true})
		assert.Empty(span.meta[ext.ErrorStack])
	})

	t.Run("on", func(t *testing.T) {
		span := newBasicSpan("web.request")
		span.setTagError(errors.New("error value with trace"), errorConfig{noDebugStack: false})
		assert.NotEmpty(span.meta[ext.ErrorStack])
	})
}

func TestTraceManualKeepAndManualDrop(t *testing.T) {
	for _, scenario := range []struct {
		tag  string
		keep bool
		p    int // priority
	}{
		{ext.ManualKeep, true, 0},
		{ext.ManualDrop, false, 1},
	} {
		t.Run(fmt.Sprintf("%s/local", scenario.tag), func(t *testing.T) {
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			span := tracer.newRootSpan("root span", "my service", "my resource")
			span.SetTag(scenario.tag, true)
			assert.Equal(t, scenario.keep, shouldKeep(span))
		})

		t.Run(fmt.Sprintf("%s/non-local", scenario.tag), func(t *testing.T) {
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			spanCtx := &SpanContext{traceID: traceIDFrom64Bits(42), spanID: 42}
			spanCtx.setSamplingPriority(scenario.p, samplernames.RemoteRate)
			span := tracer.StartSpan("non-local root span", ChildOf(spanCtx))
			span.SetTag(scenario.tag, true)
			assert.Equal(t, scenario.keep, shouldKeep(span))
		})
	}
}

// This test previously failed when running with -race.
func TestTraceManualKeepRace(t *testing.T) {
	const numGoroutines = 100

	t.Run("SetTag", func(t *testing.T) {
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(t, err)
		rootSpan := tracer.newRootSpan("root span", "my service", "my resource")
		defer rootSpan.Finish()

		wg := &sync.WaitGroup{}
		wg.Add(numGoroutines)
		for j := 0; j < numGoroutines; j++ {
			go func() {
				defer wg.Done()
				childSpan := tracer.newChildSpan("child", rootSpan)
				childSpan.SetTag(ext.ManualKeep, true)
				childSpan.Finish()
			}()
		}
		wg.Wait()
	})

	// setting the tag using a StartSpan option has the same race
	t.Run("StartSpanOption", func(t *testing.T) {
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(t, err)
		rootSpan := tracer.newRootSpan("root span", "my service", "my resource")
		defer rootSpan.Finish()

		wg := &sync.WaitGroup{}
		wg.Add(numGoroutines)
		for j := 0; j < numGoroutines; j++ {
			go func() {
				defer wg.Done()
				childSpan := tracer.StartSpan(
					"child",
					ChildOf(rootSpan.Context()),
					Tag(ext.ManualKeep, true),
				)
				childSpan.Finish()
			}()
		}
		wg.Wait()
	})
}

func TestSpanSetDatadogTags(t *testing.T) {
	assert := assert.New(t)

	span := newBasicSpan("web.request")
	span.SetTag(ext.SpanType, "http")
	span.SetTag(ext.ServiceName, "db-cluster")
	span.SetTag(ext.ResourceName, "SELECT * FROM users;")

	assert.Equal("http", span.spanType)
	assert.Equal("db-cluster", span.service)
	assert.Equal("SELECT * FROM users;", span.resource)
}

func TestSpanStart(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	assert.NoError(err)
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// a new span sets the Start after the initialization
	assert.NotEqual(int64(0), span.start)
}

func TestSpanString(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	assert.NoError(err)
	SetGlobalTracer(tracer)
	defer tracer.Stop()
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
	for name, tt := range map[string]func(assert *assert.Assertions, span *Span){
		"init": func(assert *assert.Assertions, span *Span) {
			assert.Equal(6, len(span.metrics))
			_, ok := span.metrics[keySamplingPriority]
			assert.True(ok)
			_, ok = span.metrics[keySamplingPriorityRate]
			assert.True(ok)
		},
		"float": func(assert *assert.Assertions, span *Span) {
			span.SetTag("temp", 72.42)
			assert.Equal(72.42, span.metrics["temp"])
		},
		"int": func(assert *assert.Assertions, span *Span) {
			span.SetTag("bytes", 1024)
			assert.Equal(1024.0, span.metrics["bytes"])
		},
		"max": func(assert *assert.Assertions, span *Span) {
			span.SetTag("bytes", intUpperLimit-1)
			assert.Equal(float64(intUpperLimit-1), span.metrics["bytes"])
		},
		"min": func(assert *assert.Assertions, span *Span) {
			span.SetTag("bytes", intLowerLimit+1)
			assert.Equal(float64(intLowerLimit+1), span.metrics["bytes"])
		},
		"toobig": func(assert *assert.Assertions, span *Span) {
			span.SetTag("bytes", intUpperLimit)
			assert.Equal(0.0, span.metrics["bytes"])
			assert.Equal(fmt.Sprint(intUpperLimit), span.meta["bytes"])
		},
		"toosmall": func(assert *assert.Assertions, span *Span) {
			span.SetTag("bytes", intLowerLimit)
			assert.Equal(0.0, span.metrics["bytes"])
			assert.Equal(fmt.Sprint(intLowerLimit), span.meta["bytes"])
		},
		"finished": func(assert *assert.Assertions, span *Span) {
			span.Finish()
			span.SetTag("finished.test", 1337)
			assert.Equal(6, len(span.metrics))
			_, ok := span.metrics["finished.test"]
			assert.False(ok)
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			tracer, err := newTracer(withTransport(newDefaultTransport()))
			defer tracer.Stop()
			assert.NoError(err)
			span := tracer.newRootSpan("http.request", "mux.router", "/")
			tt(assert, span)
		})
	}
}

func TestSpanProfilingTags(t *testing.T) {
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	assert.NoError(t, err)

	for _, profilerEnabled := range []bool{false, true} {
		name := fmt.Sprintf("profilerEnabled=%t", profilerEnabled)
		t.Run(name, func(t *testing.T) {
			oldVal := traceprof.SetProfilerEnabled(profilerEnabled)
			defer func() { traceprof.SetProfilerEnabled(oldVal) }()

			span := tracer.newRootSpan("pylons.request", "pylons", "/")
			val, ok := span.metrics["_dd.profiling.enabled"]
			require.Equal(t, true, ok)
			require.Equal(t, profilerEnabled, val != 0)

			childSpan := tracer.newChildSpan("my.child", span)
			_, ok = childSpan.metrics["_dd.profiling.enabled"]
			require.Equal(t, false, ok)
		})
	}

}

func TestSpanError(t *testing.T) {
	t.Setenv("DD_CLIENT_HOSTNAME_ENABLED", "false") // the host name is inconsistently returning a value, causing the test to flake.
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	assert.NoError(err)
	SetGlobalTracer(tracer)
	defer tracer.Stop()
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err = errors.New("Something wrong")
	span.SetTag(ext.Error, err)
	assert.Equal(int32(1), span.error)
	assert.Equal("Something wrong", span.meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.meta[ext.ErrorType])
	assert.NotEqual("", span.meta[ext.ErrorStack])
	span.Finish()

	// operating on a finished span is a no-op
	span = tracer.newRootSpan("flask.request", "flask", "/")
	nMeta := len(span.meta)
	span.Finish()
	span.SetTag(ext.Error, err)
	assert.Equal(int32(0), span.error)

	// '+3' is `_dd.p.dm` + `_dd.base_service`, `_dd.p.tid`
	t.Logf("%q\n", span.meta)
	assert.Equal(nMeta+3, len(span.meta))
	assert.Equal("", span.meta[ext.ErrorMsg])
	assert.Equal("", span.meta[ext.ErrorType])
	assert.Equal("", span.meta[ext.ErrorStack])
}

func TestSpanError_Typed(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	assert.NoError(err)
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err = &boomError{}
	span.SetTag(ext.Error, err)
	assert.Equal(int32(1), span.error)
	assert.Equal("boom", span.meta[ext.ErrorMsg])
	assert.Equal("*tracer.boomError", span.meta[ext.ErrorType])
	assert.NotEqual("", span.meta[ext.ErrorStack])
}

func TestSpanErrorNil(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	assert.NoError(err)
	SetGlobalTracer(tracer)
	defer tracer.Stop()
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// don't set the error if it's nil
	nMeta := len(span.meta)
	span.SetTag(ext.Error, nil)
	assert.Equal(int32(0), span.error)
	assert.Equal(nMeta, len(span.meta))
}

func TestUniqueTagKeys(t *testing.T) {
	assert := assert.New(t)
	span := newBasicSpan("web.request")

	// check to see if setMeta correctly wipes out a metric tag
	span.SetTag("foo.bar", 12)
	span.SetTag("foo.bar", "val")

	assert.NotContains(span.metrics, "foo.bar")
	assert.Equal("val", span.meta["foo.bar"])

	// check to see if setMetric correctly wipes out a meta tag
	span.SetTag("foo.bar", "val")
	span.SetTag("foo.bar", 12)

	assert.Equal(12.0, span.metrics["foo.bar"])
	assert.NotContains(span.meta, "foo.bar")
}

// Prior to a bug fix, this failed when running `go test -race`
func TestSpanModifyWhileFlushing(t *testing.T) {
	tracer, _, _, stop, err := startTestTracer(t)
	assert.Nil(t, err)
	defer stop()

	done := make(chan struct{})
	go func() {
		span := tracer.newRootSpan("pylons.request", "pylons", "/")
		span.Finish()
		// It doesn't make much sense to update the span after it's been finished,
		// but an error in a user's code could lead to this.
		span.SetOperationName("race_test")
		span.SetTag("race_test", "true")
		span.SetTag("race_test2", 133.7)
		span.SetTag("race_test3", 133.7)
		span.SetTag(ext.Error, errors.New("t"))
		span.SetUser("race_test_user_1")
		done <- struct{}{}
	}()

	for {
		select {
		case <-done:
			return
		default:
			tracer.traceWriter.flush()
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestSpanSamplingPriority(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	assert.NoError(err)

	span := tracer.newRootSpan("my.name", "my.service", "my.resource")
	_, ok := span.metrics[keySamplingPriority]
	assert.True(ok)
	_, ok = span.metrics[keySamplingPriorityRate]
	assert.True(ok)

	for _, priority := range []int{
		ext.PriorityUserReject,
		ext.PriorityAutoReject,
		ext.PriorityAutoKeep,
		ext.PriorityUserKeep,
		999, // not used, but we should allow it
	} {
		span.SetTag(ext.SamplingPriority, priority)
		v, ok := span.metrics[keySamplingPriority]
		assert.True(ok)
		assert.EqualValues(priority, v)
		assert.EqualValues(*span.context.trace.priority, v)

		childSpan := tracer.newChildSpan("my.child", span)
		v0, ok0 := span.metrics[keySamplingPriority]
		v1, ok1 := childSpan.metrics[keySamplingPriority]
		assert.Equal(ok0, ok1)
		assert.Equal(v0, v1)
		assert.EqualValues(*childSpan.context.trace.priority, v0)
	}
}

func TestSpanLog(t *testing.T) {
	// this test is executed multiple times to ensure we clean up global state correctly
	noServiceTest := func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t)
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.traceID, span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	}
	t.Run("noservice_first", noServiceTest)

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.traceID, span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.traceID, span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("version", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.traceID, span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("full", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.traceID, span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	// run no_service again: it should have forgotten the global state
	t.Run("no_service_after_full", noServiceTest)

	t.Run("subservice", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request", ServiceName("subservice name"))
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.traceID, span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("env", func(t *testing.T) {
		os.Setenv("DD_SERVICE", "tracer.test")
		defer os.Unsetenv("DD_SERVICE")
		os.Setenv("DD_VERSION", "1.2.3")
		defer os.Unsetenv("DD_VERSION")
		os.Setenv("DD_ENV", "testenv")
		defer os.Unsetenv("DD_ENV")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t)
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.traceID, span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("badformat", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`%%!b(ddtrace.Span=dd.service=tracer.test dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0")`, span.traceID, span.spanID)
		assert.Equal(expect, fmt.Sprintf("%b", span))
	})

	t.Run("notracer/options", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		assert.Nil(err)
		span := tracer.StartSpan("test.request")
		stop()
		// no service, env, or version after the tracer is stopped
		expect := fmt.Sprintf(`dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.traceID, span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("notracer/env", func(t *testing.T) {
		os.Setenv("DD_SERVICE", "tracer.test")
		defer os.Unsetenv("DD_SERVICE")
		os.Setenv("DD_VERSION", "1.2.3")
		defer os.Unsetenv("DD_VERSION")
		os.Setenv("DD_ENV", "testenv")
		defer os.Unsetenv("DD_ENV")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t)
		assert.Nil(err)
		span := tracer.StartSpan("test.request")
		stop()
		// service is not included: it is cleared when we stop the tracer
		// env, version are included: it reads the environment variable when there is no tracer
		expect := fmt.Sprintf(`dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.traceID, span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("128-bit-generation-only", func(t *testing.T) {
		// Generate 128 bit trace ids, but don't log them. So only the lower
		// 64 bits should be logged in decimal form.
		// DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED is true by default
		// DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED is false by default
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		span.traceID = 12345678
		span.spanID = 87654321
		span.Finish()
		expect := `dd.service=tracer.test dd.env=testenv dd.trace_id="12345678" dd.span_id="87654321" dd.parent_id="0"`
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("128-bit-logging-only", func(t *testing.T) {
		// Logging 128-bit trace ids is enabled, but it's not present in
		// the span. So only the lower 64 bits should be logged in decimal form.
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "true")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		span.traceID = 12345678
		span.spanID = 87654321
		span.Finish()
		expect := `dd.service=tracer.test dd.env=testenv dd.trace_id="12345678" dd.span_id="87654321" dd.parent_id="0"`
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("128-bit-logging-with-generation", func(t *testing.T) {
		// Logging 128-bit trace ids is enabled, and a 128-bit trace id, so
		// a quoted 32 byte hex string should be printed for the dd.trace_id.
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "true")
		t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "true")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		span.spanID = 87654321
		span.Finish()
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id=%q dd.span_id="87654321" dd.parent_id="0"`, span.context.TraceID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
		v, _ := span.context.meta(keyTraceID128)
		assert.NotEmpty(v)
	})

	t.Run("128-bit-logging-with-small-upper-bits", func(t *testing.T) {
		// Logging 128-bit trace ids is enabled, and a 128-bit trace id, so
		// a quoted 32 byte hex string should be printed for the dd.trace_id.
		t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "true")
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request", WithSpanID(87654321))
		span.context.traceID.SetUpper(1)
		span.Finish()
		assert.Equal(`dd.service=tracer.test dd.env=testenv dd.trace_id="00000000000000010000000005397fb1" dd.span_id="87654321" dd.parent_id="0"`, fmt.Sprintf("%v", span))
		v, _ := span.context.meta(keyTraceID128)
		assert.Equal("0000000000000001", v)
	})

	t.Run("128-bit-logging-with-empty-upper-bits", func(t *testing.T) {
		// Logging 128-bit trace ids is enabled, and but the upper 64 bits
		// are empty, so the dd.trace_id should be printed as raw digits (not hex).
		t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "true")
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request", WithSpanID(87654321))
		span.Finish()
		assert.False(span.context.traceID.HasUpper()) // it should not have generated upper bits
		assert.Equal(`dd.service=tracer.test dd.env=testenv dd.trace_id="87654321" dd.span_id="87654321" dd.parent_id="0"`, fmt.Sprintf("%v", span))
		v, _ := span.context.meta(keyTraceID128)
		assert.Equal("", v)
	})
}

func TestRootSpanAccessor(t *testing.T) {
	tracer, _, _, stop, err := startTestTracer(t)
	assert.Nil(t, err)
	defer stop()

	t.Run("nil-span", func(t *testing.T) {
		var s *Span
		require.Nil(t, s.Root())
	})

	t.Run("single-span", func(t *testing.T) {
		sp := tracer.StartSpan("root")
		require.Equal(t, sp, sp.Root())
		sp.Finish()
	})

	t.Run("single-span-finished", func(t *testing.T) {
		sp := tracer.StartSpan("root")
		sp.Finish()
		require.Equal(t, sp, sp.Root())
	})

	t.Run("root-with-children", func(t *testing.T) {
		root := tracer.StartSpan("root")
		defer root.Finish()
		child1 := tracer.StartSpan("child1", ChildOf(root.Context()))
		defer child1.Finish()
		child2 := tracer.StartSpan("child2", ChildOf(root.Context()))
		defer child2.Finish()
		child21 := tracer.StartSpan("child2.1", ChildOf(child2.Context()))
		defer child21.Finish()
		child211 := tracer.StartSpan("child2.1.1", ChildOf(child21.Context()))
		defer child211.Finish()

		require.Equal(t, root, root.Root())
		require.Equal(t, root, child1.Root())
		require.Equal(t, root, child2.Root())
		require.Equal(t, root, child21.Root())
		require.Equal(t, root, child211.Root())
	})

	t.Run("root-finished-with-children", func(t *testing.T) {
		root := tracer.StartSpan("root")
		root.Finish()
		child1 := tracer.StartSpan("child1", ChildOf(root.Context()))
		defer child1.Finish()
		child2 := tracer.StartSpan("child2", ChildOf(root.Context()))
		defer child2.Finish()
		child21 := tracer.StartSpan("child2.1", ChildOf(child2.Context()))
		defer child21.Finish()
		child211 := tracer.StartSpan("child2.1.1", ChildOf(child21.Context()))
		defer child211.Finish()

		require.Equal(t, root, root.Root())
		require.Equal(t, root, child1.Root())
		require.Equal(t, root, child2.Root())
		require.Equal(t, root, child21.Root())
		require.Equal(t, root, child211.Root())
	})
}

func TestSpanStartAndFinishLogs(t *testing.T) {
	tp := new(log.RecordLogger)
	tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithDebugMode(true))
	assert.Nil(t, err)
	defer stop()

	span := tracer.StartSpan("op")
	time.Sleep(time.Millisecond * 2)
	span.Finish()
	started, finished := false, false
	for _, l := range tp.Logs() {
		if !started {
			started = strings.Contains(l, "DEBUG: Started Span")
		}
		if !finished {
			finished = strings.Contains(l, "DEBUG: Finished Span")
		}
		if started && finished {
			break
		}
	}
	require.True(t, started)
	require.True(t, finished)
}

func TestSetUserPropagatedUserID(t *testing.T) {
	tracer, _, _, stop, err := startTestTracer(t)
	assert.Nil(t, err)
	defer stop()

	// Service 1, create span with propagated user
	s := tracer.StartSpan("op")
	s.SetUser("userino", WithPropagation())
	m := make(map[string]string)
	err = tracer.Inject(s.Context(), TextMapCarrier(m))
	require.NoError(t, err)

	// Service 2, extract user
	c, err := tracer.Extract(TextMapCarrier(m))
	require.NoError(t, err)
	s = tracer.StartSpan("op", ChildOf(c))
	s.SetUser("userino")
	assert.True(t, s.context.updated)
}

func TestStartChild(t *testing.T) {
	t.Run("own-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t)
		assert.Nil(err)
		defer stop()
		root := tracer.StartSpan("web.request", ServiceName("root-service"))
		child := root.StartChild("db.query", ServiceName("child-service"), WithSpanID(1337))

		assert.NotEqual(uint64(0), child.traceID)
		assert.NotEqual(uint64(0), child.spanID)
		assert.Equal(root.spanID, child.parentID)
		assert.Equal(root.traceID, child.parentID)
		assert.Equal(root.traceID, child.traceID)
		assert.Equal(uint64(1337), child.spanID)
		assert.Equal("child-service", child.service)

		// the root and child are both marked as "top level"
		assert.Equal(1.0, root.metrics[keyTopLevel])
		assert.Equal(1.0, child.metrics[keyTopLevel])
	})

	t.Run("inherit-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t)
		assert.Nil(err)
		defer stop()
		root := tracer.StartSpan("web.request", ServiceName("root-service"))
		child := root.StartChild("db.query")

		assert.NotEqual(uint64(0), child.traceID)
		assert.NotEqual(uint64(0), child.spanID)
		assert.Equal(root.spanID, child.parentID)

		assert.Equal("root-service", child.service)
		// the root is marked as "top level", but the child is not
		assert.Equal(1.0, root.metrics[keyTopLevel])
		assert.NotContains(child.metrics, keyTopLevel)
	})
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

func BenchmarkSetTagStringer(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	value := &stringer{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := string(keys[i%len(keys)])
		span.SetTag(k, value)
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

type stringer struct{}

func (s *stringer) String() string {
	return "string"
}
