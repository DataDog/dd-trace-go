// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	sharedinternal "gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof"

	"github.com/DataDog/datadog-agent/pkg/obfuscate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	if strings.HasPrefix(runtime.GOOS, "windows") {
		t.Skip("Windows' sleep is not precise enough for this test.")
	}

	assert := assert.New(t)
	wait := time.Millisecond * 2
	tracer := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// the finish should set finished and the duration
	time.Sleep(wait)
	span.Finish()
	assert.Greater(span.Duration, int64(wait))
	assert.True(span.finished)
}

func TestSpanFinishTwice(t *testing.T) {
	assert := assert.New(t)
	wait := time.Millisecond * 2

	tracer, _, _, stop := startTestTracer(t)
	defer stop()

	assert.Equal(tracer.traceWriter.(*agentTraceWriter).payload.itemCount(), 0)

	// the finish must be idempotent
	span := tracer.newRootSpan("pylons.request", "pylons", "/")
	time.Sleep(wait)
	span.Finish()
	tracer.awaitPayload(t, 1)

	previousDuration := span.Duration
	time.Sleep(wait)
	span.Finish()
	assert.Equal(previousDuration, span.Duration)
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
			assert.Equal(t, shouldComputeStats(&span{Metrics: tt.metrics}), tt.want)
		})
	}
}

func TestNewAggregableSpan(t *testing.T) {
	t.Run("obfuscating", func(t *testing.T) {
		o := obfuscate.NewObfuscator(obfuscate.Config{})
		aggspan := newAggregableSpan(&span{
			Name:     "name",
			Resource: "SELECT * FROM table WHERE password='secret'",
			Service:  "service",
			Type:     "sql",
		}, o)
		assert.Equal(t, aggregation{
			Name:        "name",
			Type:        "sql",
			Resource:    "SELECT * FROM table WHERE password = ?",
			Service:     "service",
			IsTraceRoot: 1,
		}, aggspan.key)
	})

	t.Run("nil-obfuscator", func(t *testing.T) {
		aggspan := newAggregableSpan(&span{
			Name:     "name",
			Resource: "SELECT * FROM table WHERE password='secret'",
			Service:  "service",
			Type:     "sql",
		}, nil)
		assert.Equal(t, aggregation{
			Name:        "name",
			Type:        "sql",
			Resource:    "SELECT * FROM table WHERE password='secret'",
			Service:     "service",
			IsTraceRoot: 1,
		}, aggspan.key)
	})
}

func TestSpanFinishWithTime(t *testing.T) {
	assert := assert.New(t)

	finishTime := time.Now().Add(10 * time.Second)
	span := newBasicSpan("web.request")
	span.Finish(FinishTime(finishTime))

	duration := finishTime.UnixNano() - span.Start
	assert.Equal(duration, span.Duration)
}

func TestSpanFinishWithNegativeDuration(t *testing.T) {
	assert := assert.New(t)
	startTime := time.Now()
	finishTime := startTime.Add(-10 * time.Second)
	span := newBasicSpan("web.request")
	span.Start = startTime.UnixNano()
	span.Finish(FinishTime(finishTime))
	assert.Equal(int64(0), span.Duration)
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
}

// String causes panic which SetTag should not handle.
func (p *panicStringer) String() string {
	panic("This should not be handled.")
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

	span.SetTag("time", (*time.Time)(nil))
	assert.Equal("<nil>", span.Meta["time"])

	span.SetTag("nilStringer", (*nilStringer)(nil))
	assert.Equal("<nil>", span.Meta["nilStringer"])

	span.SetTag("somestrings", []string{"foo", "bar"})
	assert.Equal("foo", span.Meta["somestrings.0"])
	assert.Equal("bar", span.Meta["somestrings.1"])

	span.SetTag("somebools", []bool{true, false})
	assert.Equal("true", span.Meta["somebools.0"])
	assert.Equal("false", span.Meta["somebools.1"])

	span.SetTag("somenums", []int{-1, 5, 2})
	assert.Equal(-1., span.Metrics["somenums.0"])
	assert.Equal(5., span.Metrics["somenums.1"])
	assert.Equal(2., span.Metrics["somenums.2"])

	span.SetTag("someslices", [][]string{{"a, b, c"}, {"d"}, nil, {"e, f"}})
	assert.Equal("[a, b, c]", span.Meta["someslices.0"])
	assert.Equal("[d]", span.Meta["someslices.1"])
	assert.Equal("[]", span.Meta["someslices.2"])
	assert.Equal("[e, f]", span.Meta["someslices.3"])

	mapStrStr := map[string]string{"b": "c"}
	span.SetTag("map", sharedinternal.MetaStructValue{Value: map[string]string{"b": "c"}})
	assert.Equal(mapStrStr, span.MetaStruct["map"])

	mapOfMap := map[string]map[string]any{"a": {"b": "c"}}
	span.SetTag("mapOfMap", sharedinternal.MetaStructValue{Value: mapOfMap})
	assert.Equal(mapOfMap, span.MetaStruct["mapOfMap"])

	// testMsgpStruct is a struct that implements the msgp.Marshaler interface
	testValue := &testMsgpStruct{A: "test"}
	span.SetTag("struct", sharedinternal.MetaStructValue{Value: testValue})
	require.Equal(t, testValue, span.MetaStruct["struct"])

	s := "string"
	span.SetTag("str_ptr", &s)
	assert.Equal(s, span.Meta["str_ptr"])

	span.SetTag("nil_str_ptr", (*string)(nil))
	assert.Equal("", span.Meta["nil_str_ptr"])

	assert.Panics(func() {
		span.SetTag("panicStringer", &panicStringer{})
	})
}

type testMsgpStruct struct {
	A string
}

func (t *testMsgpStruct) MarshalMsg(_ []byte) ([]byte, error) {
	return nil, nil
}

func TestSpanSetTagError(t *testing.T) {
	assert := assert.New(t)

	t.Run("off", func(t *testing.T) {
		span := newBasicSpan("web.request")
		span.setTagError(errors.New("error value with no trace"), errorConfig{noDebugStack: true})
		assert.Empty(span.Meta[ext.ErrorStack])
	})

	t.Run("on", func(t *testing.T) {
		span := newBasicSpan("web.request")
		span.setTagError(errors.New("error value with trace"), errorConfig{noDebugStack: false})
		assert.NotEmpty(span.Meta[ext.ErrorStack])
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
			tracer := newTracer()
			defer tracer.Stop()
			span := tracer.newRootSpan("root span", "my service", "my resource")
			span.SetTag(scenario.tag, true)
			assert.Equal(t, scenario.keep, shouldKeep(span))
		})

		t.Run(fmt.Sprintf("%s/non-local", scenario.tag), func(t *testing.T) {
			tracer := newTracer()
			defer tracer.Stop()
			spanCtx := &spanContext{traceID: traceIDFrom64Bits(42), spanID: 42}
			spanCtx.setSamplingPriority(scenario.p, samplernames.RemoteRate)
			span := tracer.StartSpan("non-local root span", ChildOf(spanCtx)).(*span)
			span.SetTag(scenario.tag, true)
			assert.Equal(t, scenario.keep, shouldKeep(span))
		})
	}
}

// This test previously failed when running with -race.
func TestTraceManualKeepRace(t *testing.T) {
	const numGoroutines = 100

	t.Run("SetTag", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
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
		tracer := newTracer()
		defer tracer.Stop()
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

	assert.Equal("http", span.Type)
	assert.Equal("db-cluster", span.Service)
	assert.Equal("SELECT * FROM users;", span.Resource)
}

func TestSpanStart(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// a new span sets the Start after the initialization
	assert.NotEqual(int64(0), span.Start)
}

func TestSpanString(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))
	internal.SetGlobalTracer(tracer)
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
	for name, tt := range map[string]func(assert *assert.Assertions, span *span){
		"init": func(assert *assert.Assertions, span *span) {
			assert.Equal(6, len(span.Metrics))
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
			assert.Equal(6, len(span.Metrics))
			_, ok := span.Metrics["finished.test"]
			assert.False(ok)
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			tracer := newTracer(withTransport(newDefaultTransport()))
			defer tracer.Stop()
			span := tracer.newRootSpan("http.request", "mux.router", "/")
			tt(assert, span)
		})
	}
}

func TestSpanProfilingTags(t *testing.T) {
	tracer := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()

	for _, profilerEnabled := range []bool{false, true} {
		name := fmt.Sprintf("profilerEnabled=%t", profilerEnabled)
		t.Run(name, func(t *testing.T) {
			oldVal := traceprof.SetProfilerEnabled(profilerEnabled)
			defer func() { traceprof.SetProfilerEnabled(oldVal) }()

			span := tracer.newRootSpan("pylons.request", "pylons", "/")
			val, ok := span.Metrics["_dd.profiling.enabled"]
			require.Equal(t, true, ok)
			require.Equal(t, profilerEnabled, val != 0)

			childSpan := tracer.newChildSpan("my.child", span)
			_, ok = childSpan.Metrics["_dd.profiling.enabled"]
			require.Equal(t, false, ok)
		})
	}
}

func TestSpanError(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))
	internal.SetGlobalTracer(tracer)
	defer tracer.Stop()
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err := errors.New("Something wrong")
	span.SetTag(ext.Error, err)
	assert.Equal(int32(1), span.Error)
	assert.Equal("Something wrong", span.Meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.Meta[ext.ErrorType])
	assert.NotEqual("", span.Meta[ext.ErrorStack])
	span.Finish()

	// operating on a finished span is a no-op
	span = tracer.newRootSpan("flask.request", "flask", "/")
	nMeta := len(span.Meta)
	span.Finish()
	span.SetTag(ext.Error, err)
	assert.Equal(int32(0), span.Error)

	// '+3' is `_dd.p.dm` + `_dd.base_service`, `_dd.p.tid`
	t.Logf("%q\n", span.Meta)
	assert.Equal(nMeta+3, len(span.Meta))
	assert.Equal("", span.Meta[ext.ErrorMsg])
	assert.Equal("", span.Meta[ext.ErrorType])
	assert.Equal("", span.Meta[ext.ErrorStack])
}

func TestSpanError_Typed(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err := &boomError{}
	span.SetTag(ext.Error, err)
	assert.Equal(int32(1), span.Error)
	assert.Equal("boom", span.Meta[ext.ErrorMsg])
	assert.Equal("*tracer.boomError", span.Meta[ext.ErrorType])
	assert.NotEqual("", span.Meta[ext.ErrorStack])
}

func TestSpanErrorNil(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(withTransport(newDefaultTransport()))
	internal.SetGlobalTracer(tracer)
	defer tracer.Stop()
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// don't set the error if it's nil
	nMeta := len(span.Meta)
	span.SetTag(ext.Error, nil)
	assert.Equal(int32(0), span.Error)
	assert.Equal(nMeta, len(span.Meta))
}

func TestUniqueTagKeys(t *testing.T) {
	assert := assert.New(t)
	span := newBasicSpan("web.request")

	// check to see if setMeta correctly wipes out a metric tag
	span.SetTag("foo.bar", 12)
	span.SetTag("foo.bar", "val")

	assert.NotContains(span.Metrics, "foo.bar")
	assert.Equal("val", span.Meta["foo.bar"])

	// check to see if setMetric correctly wipes out a meta tag
	span.SetTag("foo.bar", "val")
	span.SetTag("foo.bar", 12)

	assert.Equal(12.0, span.Metrics["foo.bar"])
	assert.NotContains(span.Meta, "foo.bar")
}

// Prior to a bug fix, this failed when running `go test -race`
func TestSpanModifyWhileFlushing(t *testing.T) {
	tracer, _, _, stop := startTestTracer(t)
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
	tracer := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()

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

func TestSpanLog(t *testing.T) {
	// this test is executed multiple times to ensure we clean up global state correctly
	noServiceTest := func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t)
		defer stop()
		span := tracer.StartSpan("test.request").(*span)
		expect := fmt.Sprintf(`dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.TraceID, span.SpanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	}
	t.Run("noservice_first", noServiceTest)

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"))
		defer stop()
		span := tracer.StartSpan("test.request").(*span)
		expect := fmt.Sprintf(`dd.service=tracer.test dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.TraceID, span.SpanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request").(*span)
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.TraceID, span.SpanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("version", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"))
		defer stop()
		span := tracer.StartSpan("test.request").(*span)
		expect := fmt.Sprintf(`dd.service=tracer.test dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.TraceID, span.SpanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("full", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request").(*span)
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.TraceID, span.SpanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	// run no_service again: it should have forgotten the global state
	t.Run("no_service_after_full", noServiceTest)

	t.Run("subservice", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request", ServiceName("subservice name")).(*span)
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.TraceID, span.SpanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "tracer.test")
		t.Setenv("DD_VERSION", "1.2.3")
		t.Setenv("DD_ENV", "testenv")
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t)
		defer stop()
		span := tracer.StartSpan("test.request").(*span)
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.TraceID, span.SpanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("badformat", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"))
		defer stop()
		span := tracer.StartSpan("test.request").(*span)
		expect := fmt.Sprintf(`%%!b(ddtrace.Span=dd.service=tracer.test dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0")`, span.TraceID, span.SpanID)
		assert.Equal(expect, fmt.Sprintf("%b", span))
	})

	t.Run("notracer/options", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		span := tracer.StartSpan("test.request").(*span)
		stop()
		// no service, env, or version after the tracer is stopped
		expect := fmt.Sprintf(`dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.TraceID, span.SpanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("notracer/env", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "tracer.test")
		t.Setenv("DD_VERSION", "1.2.3")
		t.Setenv("DD_ENV", "testenv")
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t)
		span := tracer.StartSpan("test.request").(*span)
		stop()
		// service is not included: it is cleared when we stop the tracer
		// env, version are included: it reads the environment variable when there is no tracer
		expect := fmt.Sprintf(`dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.TraceID, span.SpanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("128-bit-generation-only", func(t *testing.T) {
		// Generate 128 bit trace ids, but don't log them. So only the lower
		// 64 bits should be logged in decimal form.
		// DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED is true by default
		// DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED is false by default
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request").(*span)
		span.TraceID = 12345678
		span.SpanID = 87654321
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
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request").(*span)
		span.TraceID = 12345678
		span.SpanID = 87654321
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
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request").(*span)
		span.SpanID = 87654321
		span.Finish()
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id=%q dd.span_id="87654321" dd.parent_id="0"`, span.context.TraceID128())
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
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request", WithSpanID(87654321)).(*span)
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
		tracer, _, _, stop := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request", WithSpanID(87654321)).(*span)
		span.Finish()
		assert.False(span.context.traceID.HasUpper()) // it should not have generated upper bits
		assert.Equal(`dd.service=tracer.test dd.env=testenv dd.trace_id="87654321" dd.span_id="87654321" dd.parent_id="0"`, fmt.Sprintf("%v", span))
		v, _ := span.context.meta(keyTraceID128)
		assert.Equal("", v)
	})
}

func TestRootSpanAccessor(t *testing.T) {
	tracer, _, _, stop := startTestTracer(t)
	defer stop()

	t.Run("nil-span", func(t *testing.T) {
		var s *span
		require.Nil(t, s.Root())
	})

	t.Run("single-span", func(t *testing.T) {
		sp := tracer.StartSpan("root")
		require.Equal(t, sp, sp.(*span).Root())
		sp.Finish()
	})

	t.Run("single-span-finished", func(t *testing.T) {
		sp := tracer.StartSpan("root")
		sp.Finish()
		require.Equal(t, sp, sp.(*span).Root())
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

		require.Equal(t, root, root.(*span).Root())
		require.Equal(t, root, child1.(*span).Root())
		require.Equal(t, root, child2.(*span).Root())
		require.Equal(t, root, child21.(*span).Root())
		require.Equal(t, root, child211.(*span).Root())
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

		require.Equal(t, root, root.(*span).Root())
		require.Equal(t, root, child1.(*span).Root())
		require.Equal(t, root, child2.(*span).Root())
		require.Equal(t, root, child21.(*span).Root())
		require.Equal(t, root, child211.(*span).Root())
	})
}

func TestSpanStartAndFinishLogs(t *testing.T) {
	tp := new(log.RecordLogger)
	tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithDebugMode(true))
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
	tracer, _, _, stop := startTestTracer(t)
	defer stop()

	// Service 1, create span with propagated user
	s := tracer.StartSpan("op")
	s.(*span).SetUser("userino", WithPropagation())
	m := make(map[string]string)
	err := tracer.Inject(s.Context(), TextMapCarrier(m))
	require.NoError(t, err)

	// Service 2, extract user
	c, err := tracer.Extract(TextMapCarrier(m))
	require.NoError(t, err)
	s = tracer.StartSpan("op", ChildOf(c))
	s.(*span).SetUser("userino")
	assert.True(t, s.(*span).context.updated)
}

func BenchmarkSetTagMetric(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := strings.Split("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", "")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%len(keys)]
		span.SetTag(k, float64(12.34))
	}
}

func BenchmarkSetTagString(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := strings.Split("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", "")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := string(keys[i%len(keys)])
		span.SetTag(k, "some text")
	}
}

func BenchmarkSetTagStringPtr(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := strings.Split("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", "")
	v := makePointer("some text")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%len(keys)]
		span.SetTag(k, v)
	}
}

func BenchmarkSetTagStringer(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := strings.Split("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", "")
	value := &stringer{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%len(keys)]
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

// TestConcurrentSpanSetTag tests that setting tags concurrently on a span directly or
// not (through tracer.Inject when trace sampling rules are in place) does not cause
// concurrent map writes. It seems to only be consistently reproduced with the -count=100
// flag when running go test, but it's a good test to have.
func TestConcurrentSpanSetTag(t *testing.T) {
	testConcurrentSpanSetTag(t)
	testConcurrentSpanSetTag(t)
}

func testConcurrentSpanSetTag(t *testing.T) {
	tracer, _, _, stop := startTestTracer(t, WithSamplingRules([]SamplingRule{NameRule("root", 1.0)}))
	defer stop()

	span := tracer.StartSpan("root")
	defer span.Finish()

	const n = 100
	wg := sync.WaitGroup{}
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		go func() {
			tracer.Inject(span.Context(), TextMapCarrier(map[string]string{}))
			wg.Done()
		}()
		go func() {
			span.SetTag("key", "value")
			wg.Done()
		}()
	}
	wg.Wait()
}
