// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	sharedinternal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"

	"github.com/DataDog/datadog-go/v5/statsd"
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
	span.context = newSpanContext(span, nil) // +checklocksignore
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
		t.Run(tt.name, func(_ *testing.T) {
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
	assertions.Equal(uint64(0), ctx.TraceIDLower())
	assertions.Equal(uint64(0), ctx.SpanID())
	sp, ok := ctx.SamplingPriority()
	assertions.Equal(0, sp)
	assertions.Equal(false, ok)
	// calls on nil span should be no-op
	assertions.Nil(span.Root())
	span.SetBaggageItem("key", "value")
	if v := span.BaggageItem("key"); v != "" {
		t.Errorf("expected empty string, got %s", v)
	}
	span.SetTag("key", "value")
	if v := span.AsMap()["key"]; v != nil {
		t.Errorf("expected nil, got %s", v)
	}
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

func BenchmarkAddLink(b *testing.B) {
	rootSpan := newSpan("root", "service", "res", 123, 456, 0)
	spanContext := newSpanContext(rootSpan, nil) // +checklocksignore
	attrs := map[string]string{"key1": "val1"}
	link := SpanLink{
		TraceID:     spanContext.TraceIDLower(),
		TraceIDHigh: spanContext.TraceIDUpper(),
		SpanID:      spanContext.SpanID(),
		Attributes:  attrs,
	}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		rootSpan.AddLink(link)
	}
}

func TestSpanOperationName(t *testing.T) {
	assert := assert.New(t)

	span := newBasicSpan("web.request")
	span.SetOperationName("http.request")
	assert.Equal("http.request", span.getName())
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

	// TODO(kakkoyun): Refactor.
	span.mu.RLock()
	defer span.mu.RUnlock()

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

	// check that the span does not have any span links serialized
	// spans don't have span links by default and they are serialized in the meta map
	// as part of the Finish call
	assert.Zero(span.getMeta("_dd.span_links"))

	// manipulate the span
	span.AddLink(SpanLink{
		TraceID: span.getTraceID(),
		SpanID:  span.getSpanID(),
		Attributes: map[string]string{
			"manual.keep": "true",
		},
	})

	previousDuration := span.getDuration()
	time.Sleep(wait)
	span.Finish()

	assert.Equal(previousDuration, span.getDuration())
	assert.Zero(span.getMeta("_dd.span_links"))

	tracer.awaitPayload(t, 1) // this checks that no other span was seen by the tracerWriter
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
			s.setSamplingPriority(tt.prio, samplernames.Default)
			s.SetTag(ext.EventSampleRate, tt.rate)
			atomic.StoreInt32(&s.Context().errors, tt.errors)
			s.mu.RLock()
			assert.Equal(t, s.shouldKeepAssumesHoldingLock(), tt.want)
			s.mu.RUnlock()
		})
	}

	t.Run("none", func(t *testing.T) {
		s := newSpan("", "", "", 1, 1, 0)
		s.mu.RLock()
		assert.Equal(t, s.shouldKeepAssumesHoldingLock(), false)
		s.mu.RUnlock()
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
			s := &Span{metrics: tt.metrics}
			s.mu.RLock()
			assert.Equal(t, s.shouldComputeStatsAssumesHoldingLock(), tt.want)
			s.mu.RUnlock()
		})
	}
}

func TestSpanFinishWithTime(t *testing.T) {
	assert := assert.New(t)

	finishTime := time.Now().Add(10 * time.Second)
	span := newBasicSpan("web.request")
	span.Finish(FinishTime(finishTime))

	span.mu.RLock()
	defer span.mu.RUnlock()

	duration := finishTime.UnixNano() - span.start
	assert.Equal(duration, span.duration)
}

func TestSpanFinishWithNegativeDuration(t *testing.T) {
	assert := assert.New(t)
	startTime := time.Now()
	finishTime := startTime.Add(-10 * time.Second)
	span := newBasicSpan("web.request")
	span.start = startTime.UnixNano() // +checklocksignore
	span.Finish(FinishTime(finishTime))
	assert.Equal(int64(0), span.getDuration())
}

func TestSpanFinishWithError(t *testing.T) {
	assert := assert.New(t)

	err := errors.New("test error")
	span := newBasicSpan("web.request")
	span.Finish(WithError(err))

	span.mu.RLock()
	defer span.mu.RUnlock()

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

	span.mu.RLock()
	defer span.mu.RUnlock()

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

	span.mu.RLock()
	defer span.mu.RUnlock()

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
}

// String causes panic which SetTag should not handle.
func (p *panicStringer) String() string {
	panic("This should not be handled.")
}

func TestSpanSetTag(t *testing.T) {
	assert := assert.New(t)
	span := newBasicSpan("web.request")
	assert.Equal("web.request", span.getName())

	span.SetTag("component", "tracer")
	mt, ok := span.getMeta("component")
	assert.True(ok)
	assert.Equal("tracer", mt)

	span.SetTag("tagInt", 1234)
	mtr, ok := span.getMetric("tagInt")
	assert.True(ok)
	assert.Equal(float64(1234), mtr)

	span.SetTag("tagStruct", struct{ A, B int }{1, 2})
	mt, ok = span.getMeta("tagStruct")
	assert.True(ok)
	assert.Equal("{1 2}", mt)

	span.SetTag(ext.Error, true)
	assert.Equal(int32(1), span.getError())

	span.SetTag(ext.Error, nil)
	assert.Equal(int32(0), span.getError())

	span.SetTag(ext.Error, errors.New("abc"))
	assert.Equal(int32(1), span.getError())
	emsg, ok := span.getMeta(ext.ErrorMsg)
	assert.True(ok)
	assert.Equal("abc", emsg)
	etyp, ok := span.getMeta(ext.ErrorType)
	assert.True(ok)
	assert.Equal("*errors.errorString", etyp)
	estk, ok := span.getMeta(ext.ErrorStack)
	assert.True(ok)
	assert.NotEmpty(estk)

	span.SetTag(ext.Error, "something else")
	assert.Equal(int32(1), span.getError())

	span.SetTag(ext.Error, false)
	assert.Equal(int32(0), span.getError())

	span.SetTag("some.bool", true)
	bt, ok := span.getMeta("some.bool")
	assert.True(ok)
	assert.Equal("true", bt)

	span.SetTag("some.other.bool", false)
	bt, ok = span.getMeta("some.other.bool")
	assert.True(ok)
	assert.Equal("false", bt)

	span.SetTag("time", (*time.Time)(nil))
	nt, ok := span.getMeta("time")
	assert.True(ok)
	assert.Equal("<nil>", nt)

	span.SetTag("nilStringer", (*nilStringer)(nil))
	nt, ok = span.getMeta("nilStringer")
	assert.True(ok)
	assert.Equal("<nil>", nt)

	span.SetTag("somestrings", []string{"foo", "bar"})
	mtd := span.getMetas()
	assert.Equal("foo", mtd["somestrings.0"])
	assert.Equal("bar", mtd["somestrings.1"])

	span.SetTag("somebools", []bool{true, false})
	mtd = span.getMetas()
	assert.Equal("true", mtd["somebools.0"])
	assert.Equal("false", mtd["somebools.1"])

	span.SetTag("somenums", []int{-1, 5, 2})
	mtrs := span.getMetrics()
	assert.Equal(-1., mtrs["somenums.0"])
	assert.Equal(5., mtrs["somenums.1"])
	assert.Equal(2., mtrs["somenums.2"])

	span.SetTag("someslices", [][]string{{"a, b, c"}, {"d"}, nil, {"e, f"}})
	mtd = span.getMetas()
	assert.Equal("[a, b, c]", mtd["someslices.0"])
	assert.Equal("[d]", mtd["someslices.1"])
	assert.Equal("[]", mtd["someslices.2"])
	assert.Equal("[e, f]", mtd["someslices.3"])

	mapStrStr := map[string]string{"b": "c"}
	span.SetTag("map", sharedinternal.MetaStructValue{Value: map[string]string{"b": "c"}})
	assert.Equal(mapStrStr, span.getMetaStruct()["map"])

	mapOfMap := map[string]map[string]any{"a": {"b": "c"}}
	span.SetTag("mapOfMap", sharedinternal.MetaStructValue{Value: mapOfMap})
	assert.Equal(mapOfMap, span.getMetaStruct()["mapOfMap"])

	// testMsgpStruct is a struct that implements the msgp.Marshaler interface
	testValue := &testMsgpStruct{A: "test"}
	span.SetTag("struct", sharedinternal.MetaStructValue{Value: testValue})
	assert.Equal(testValue, span.getMetaStruct()["struct"])

	mapStrStr = map[string]string{"b": "c"}
	span.SetTag("map", sharedinternal.MetaStructValue{Value: map[string]string{"b": "c"}})
	assert.Equal(mapStrStr, span.getMetaStruct()["map"])

	mapOfMap = map[string]map[string]any{"a": {"b": "c"}}
	span.SetTag("mapOfMap", sharedinternal.MetaStructValue{Value: mapOfMap})
	assert.Equal(mapOfMap, span.getMetaStruct()["mapOfMap"])

	// testMsgpStruct is a struct that implements the msgp.Marshaler interface
	testValue = &testMsgpStruct{A: "test"}
	span.SetTag("struct", sharedinternal.MetaStructValue{Value: testValue})
	assert.Equal(testValue, span.getMetaStruct()["struct"])

	s := "string"
	span.SetTag("str_ptr", &s)
	mt, ok = span.getMeta("str_ptr")
	assert.True(ok)
	assert.Equal(s, mt)

	span.SetTag("nil_str_ptr", (*string)(nil))
	mt, ok = span.getMeta("nil_str_ptr")
	assert.True(ok)
	assert.Equal("", mt)

	assert.Panics(func() {
		span.SetTag("panicStringer", &panicStringer{})
	})
}

func TestSpanTagsStartSpan(t *testing.T) {
	assert := assert.New(t)
	tr, _, _, stop, err := startTestTracer(t)
	assert.NoError(err)
	defer stop()

	span := tr.StartSpan("operation-name", ServiceName("service"), Tag("tag", "value"))

	tags := span.AsMap()
	assert.Equal("value", tags["tag"])
	assert.Equal("service", tags[ext.ServiceName])
	assert.Equal("operation-name", tags[ext.SpanName])
}

type testMsgpStruct struct {
	A string
}

func (t *testMsgpStruct) MarshalMsg(_ []byte) ([]byte, error) {
	return nil, nil
}

func TestSpanSetTagError(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		span := newBasicSpan("web.request")
		span.setTagErrorAssumesHoldingLock(errors.New("error value with no trace"), errorConfig{noDebugStack: true}) // +checklocksignore
		val, _ := span.getMeta(ext.ErrorStack)
		assert.Empty(t, val)
	})

	t.Run("on", func(t *testing.T) {
		span := newBasicSpan("web.request")
		span.setTagErrorAssumesHoldingLock(errors.New("error value with trace"), errorConfig{noDebugStack: false}) // +checklocksignore
		val, _ := span.getMeta(ext.ErrorStack)
		assert.NotEmpty(t, val)
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
			span.mu.RLock()
			assert.Equal(t, scenario.keep, span.shouldKeepAssumesHoldingLock())
			span.mu.RUnlock()
		})

		t.Run(fmt.Sprintf("%s/non-local", scenario.tag), func(t *testing.T) {
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			spanCtx := &SpanContext{traceID: traceIDFrom64Bits(42), spanID: 42}
			spanCtx.setSamplingPriority(scenario.p, samplernames.RemoteRate)
			span := tracer.StartSpan("non-local root span", ChildOf(spanCtx))
			span.SetTag(scenario.tag, true)
			span.mu.RLock()
			assert.Equal(t, scenario.keep, span.shouldKeepAssumesHoldingLock())
			span.mu.RUnlock()
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
	t.Run("StartSpanOption", func(_ *testing.T) {
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

	assert.Equal("http", span.getSpanType())
	assert.Equal("db-cluster", span.getService())
	assert.Equal("SELECT * FROM users;", span.getResource())
}

func TestSpanStart(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	assert.NoError(err)
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// a new span sets the Start after the initialization
	assert.NotEqual(int64(0), span.getStart())
}

func TestSpanString(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	assert.NoError(err)
	setGlobalTracer(tracer)
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
			assert.Equal(6, len(span.getMetrics()))
			_, ok := span.getMetric(keySamplingPriority)
			assert.True(ok)
			_, ok = span.getMetric(keySamplingPriorityRate)
			assert.True(ok)
		},
		"float": func(assert *assert.Assertions, span *Span) {
			span.SetTag("temp", 72.42)
			val, ok := span.getMetric("temp")
			assert.True(ok)
			assert.Equal(72.42, val)
		},
		"int": func(assert *assert.Assertions, span *Span) {
			span.SetTag("bytes", 1024)
			val, ok := span.getMetric("bytes")
			assert.True(ok)
			assert.Equal(1024.0, val)
		},
		"max": func(assert *assert.Assertions, span *Span) {
			span.SetTag("bytes", intUpperLimit-1)
			val, ok := span.getMetric("bytes")
			assert.True(ok)
			assert.Equal(float64(intUpperLimit-1), val)
		},
		"min": func(assert *assert.Assertions, span *Span) {
			span.SetTag("bytes", intLowerLimit+1)
			val, ok := span.getMetric("bytes")
			assert.True(ok)
			assert.Equal(float64(intLowerLimit+1), val)
		},
		"toobig": func(assert *assert.Assertions, span *Span) {
			span.SetTag("bytes", intUpperLimit)
			val, _ := span.getMetric("bytes")
			assert.Equal(0.0, val)
			mt, _ := span.getMeta("bytes")
			assert.Equal(fmt.Sprint(intUpperLimit), mt)
		},
		"toosmall": func(assert *assert.Assertions, span *Span) {
			span.SetTag("bytes", intLowerLimit)
			val, _ := span.getMetric("bytes")
			assert.Equal(0.0, val)
			mt, _ := span.getMeta("bytes")
			assert.Equal(fmt.Sprint(intLowerLimit), mt)
		},
		"finished": func(assert *assert.Assertions, span *Span) {
			span.Finish()
			span.SetTag("finished.test", 1337)
			assert.Equal(6, len(span.getMetrics()))
			_, ok := span.getMetric("finished.test")
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
			val, ok := span.getMetric("_dd.profiling.enabled")
			require.Equal(t, true, ok)
			require.Equal(t, profilerEnabled, val != 0)

			childSpan := tracer.newChildSpan("my.child", span)
			_, ok = childSpan.getMetric("_dd.profiling.enabled")
			require.Equal(t, false, ok)
		})
	}
}

func TestSpanError(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	assert.NoError(err)
	setGlobalTracer(tracer)
	defer tracer.Stop()
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// check the error is set in the default meta
	err = errors.New("Something wrong")
	span.SetTag(ext.Error, err)

	span.mu.RLock()
	assert.Equal(int32(1), span.error)
	assert.Equal("Something wrong", span.meta[ext.ErrorMsg])
	assert.Equal("*errors.errorString", span.meta[ext.ErrorType])
	assert.NotEqual("", span.meta[ext.ErrorStack])
	span.mu.RUnlock()

	span.Finish()

	// operating on a finished span is a no-op
	span = tracer.newRootSpan("flask.request", "flask", "/")
	nMeta := len(span.getMetas())
	span.Finish()
	span.SetTag(ext.Error, err)

	span.mu.RLock()
	defer span.mu.RUnlock()

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

	span.mu.RLock()
	defer span.mu.RUnlock()

	assert.Equal(int32(1), span.error)
	assert.Equal("boom", span.meta[ext.ErrorMsg])
	assert.Equal("*tracer.boomError", span.meta[ext.ErrorType])
	assert.NotEqual("", span.meta[ext.ErrorStack])
}

func TestSpanErrorNil(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	assert.NoError(err)
	setGlobalTracer(tracer)
	defer tracer.Stop()
	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	// don't set the error if it's nil
	nMeta := len(span.getMetas())
	span.SetTag(ext.Error, nil)

	span.mu.RLock()
	defer span.mu.RUnlock()

	assert.Equal(int32(0), span.error)
	assert.Equal(nMeta, len(span.meta))
}

func TestUniqueTagKeys(t *testing.T) {
	assert := assert.New(t)
	span := newBasicSpan("web.request")

	// check to see if setMeta correctly wipes out a metric tag
	span.SetTag("foo.bar", 12)
	span.SetTag("foo.bar", "val")

	assert.NotContains(span.getMetrics(), "foo.bar")
	mt, ok := span.getMeta("foo.bar")
	assert.True(ok)
	assert.Equal("val", mt)

	// check to see if setMetric correctly wipes out a meta tag
	span.SetTag("foo.bar", "val")
	span.SetTag("foo.bar", 12)

	mtrs := span.getMetrics()
	assert.Equal(12.0, mtrs["foo.bar"])
	assert.NotContains(span.getMetas(), "foo.bar")
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
	_, ok := span.getMetric(keySamplingPriority)
	assert.True(ok)
	_, ok = span.getMetric(keySamplingPriorityRate)
	assert.True(ok)

	for _, priority := range []int{
		ext.PriorityUserReject,
		ext.PriorityAutoReject,
		ext.PriorityAutoKeep,
		ext.PriorityUserKeep,
		999, // not used, but we should allow it
	} {
		span.setSamplingPriority(priority, samplernames.Default)
		v, ok := span.getMetric(keySamplingPriority)
		assert.True(ok)
		assert.EqualValues(priority, v)
		sp, ok := span.Context().trace.samplingPriority()
		assert.True(ok)
		assert.EqualValues(float64(sp), v)

		childSpan := tracer.newChildSpan("my.child", span)
		v0, ok0 := span.getMetric(keySamplingPriority)
		v1, ok1 := childSpan.getMetric(keySamplingPriority)
		assert.Equal(ok0, ok1)
		assert.Equal(v0, v1)
		sp, ok = childSpan.Context().trace.samplingPriority()
		assert.True(ok)
		assert.EqualValues(float64(sp), v0)
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
		expect := fmt.Sprintf(`dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.getSpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	}
	t.Run("noservice_first", noServiceTest)

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.getSpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.getSpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("version", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.getSpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("full", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.getSpanID())
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
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.getSpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "tracer.test")
		t.Setenv("DD_VERSION", "1.2.3")
		t.Setenv("DD_ENV", "testenv")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t)
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.getSpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("badformat", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`%%!b(tracer.Span=dd.service=tracer.test dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0")`, span.Context().TraceID(), span.getSpanID())
		assert.Equal(expect, fmt.Sprintf("%b", span))
	})

	t.Run("notracer/options", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		assert.Nil(err)
		span := tracer.StartSpan("test.request")
		stop()
		// no service, env, or version after the tracer is stopped
		expect := fmt.Sprintf(`dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.getSpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("notracer/env", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "tracer.test")
		t.Setenv("DD_VERSION", "1.2.3")
		t.Setenv("DD_ENV", "testenv")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t)
		assert.Nil(err)
		span := tracer.StartSpan("test.request")
		stop()
		// service is not included: it is cleared when we stop the tracer
		// env, version are included: it reads the environment variable when there is no tracer
		expect := fmt.Sprintf(`dd.env=testenv dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.getSpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("128-bit-logging-default", func(t *testing.T) {
		// Generate and log 128 bit trace ids by default
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		span.spanID = 87654321 // +checklocksignore
		span.Finish()
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id=%q dd.span_id="87654321" dd.parent_id="0"`, span.Context().TraceID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("128-bit-logging-only", func(t *testing.T) {
		// Logging 128-bit trace ids is enabled, but 128bit format is not present in
		// the span. So only the lower 64 bits should be logged in decimal form.
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		span.traceID = 12345678 // +checklocksignore
		span.spanID = 87654321  // +checklocksignore
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
		span.spanID = 87654321 // +checklocksignore
		span.Finish()
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id=%q dd.span_id="87654321" dd.parent_id="0"`, span.Context().TraceID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
		v, _ := span.getMeta(keyTraceID128)
		assert.NotEmpty(v)
	})

	t.Run("128-bit-logging-with-small-upper-bits", func(t *testing.T) {
		// Logging 128-bit trace ids is enabled, and a 128-bit trace id, so
		// a quoted 32 byte hex string should be printed for the dd.trace_id.
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request", WithSpanID(87654321))
		span.Context().traceID.SetUpper(1)
		span.Finish()
		assert.Equal(`dd.service=tracer.test dd.env=testenv dd.trace_id="00000000000000010000000005397fb1" dd.span_id="87654321" dd.parent_id="0"`, fmt.Sprintf("%v", span))
		v, _ := span.getMeta(keyTraceID128)
		assert.Equal("0000000000000001", v)
	})

	t.Run("128-bit-logging-with-empty-upper-bits", func(t *testing.T) {
		// Logging 128-bit trace ids is enabled, but the upper 64 bits
		// are empty, so the dd.trace_id should be printed as raw digits (not hex).
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request", WithSpanID(87654321))
		span.Finish()
		assert.False(span.Context().traceID.HasUpper()) // it should not have generated upper bits
		assert.Equal(`dd.service=tracer.test dd.env=testenv dd.trace_id="87654321" dd.span_id="87654321" dd.parent_id="0"`, fmt.Sprintf("%v", span))
		v, _ := span.getMeta(keyTraceID128)
		assert.Equal("", v)
	})

	t.Run("128-bit-logging-disabled", func(t *testing.T) {
		// Only the lower 64 bits should be logged in decimal form.
		// DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED is true by default
		t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		assert.Nil(err)
		span := tracer.StartSpan("test.request")
		span.traceID = 12345678 // +checklocksignore
		span.spanID = 87654321  // +checklocksignore
		span.Finish()
		expect := `dd.service=tracer.test dd.env=testenv dd.trace_id="12345678" dd.span_id="87654321" dd.parent_id="0"`
		assert.Equal(expect, fmt.Sprintf("%v", span))
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
	assert.True(t, s.Context().updated)
}

func TestStartChild(t *testing.T) {
	t.Run("own-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t)
		assert.Nil(err)
		defer stop()
		root := tracer.StartSpan("web.request", ServiceName("root-service"))
		child := root.StartChild("db.query", ServiceName("child-service"), WithSpanID(1337))

		root.mu.RLock()
		child.mu.RLock()

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

		root.mu.RUnlock()
		child.mu.RUnlock()
	})

	t.Run("inherit-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t)
		assert.Nil(err)
		defer stop()
		root := tracer.StartSpan("web.request", ServiceName("root-service"))
		child := root.StartChild("db.query")

		root.mu.RLock()
		child.mu.RLock()

		assert.NotEqual(uint64(0), child.traceID)
		assert.NotEqual(uint64(0), child.spanID)
		assert.Equal(root.spanID, child.parentID)

		assert.Equal("root-service", child.service)
		// the root is marked as "top level", but the child is not
		assert.Equal(1.0, root.metrics[keyTopLevel])
		assert.NotContains(child.metrics, keyTopLevel)

		root.mu.RUnlock()
		child.mu.RUnlock()
	})
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

func BenchmarkSerializeSpanLinksInMeta(b *testing.B) {
	span := newBasicSpan("bench.span")

	span.AddLink(SpanLink{SpanID: 123, TraceID: 456})
	span.AddLink(SpanLink{SpanID: 789, TraceID: 101})

	// Sample span pointer
	attributes := map[string]string{
		"link.kind": "span-pointer",
		"ptr.dir":   "d",
		"ptr.hash":  "eb29cb7d923f904f02bd8b3d85e228ed",
		"ptr.kind":  "aws.s3.object",
	}
	span.AddLink(SpanLink{TraceID: 0, SpanID: 0, Attributes: attributes})

	// TODO(kakkoyun): Refactor.
	span.mu.Lock()
	defer span.mu.Unlock()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		span.serializeSpanLinksInMetaAssumesHoldingLock()
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
	tracer, _, _, stop, err := startTestTracer(t, WithSamplingRules(SpanSamplingRules(Rule{NameGlob: "root", Rate: 1.0})))
	assert.NoError(t, err)
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

func TestSpanLinksInMeta(t *testing.T) {
	t.Run("no_links", func(t *testing.T) {
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		sp := tracer.StartSpan("test-no-links")
		sp.Finish()

		internalSpan := sp
		_, ok := internalSpan.getMeta("_dd.span_links")
		assert.False(t, ok, "Expected no _dd.span_links in Meta.")
	})

	t.Run("with_links", func(t *testing.T) {
		tracer, err := newTracer()
		require.NoError(t, err)
		defer tracer.Stop()

		sp := tracer.StartSpan("test-with-links")

		sp.AddLink(SpanLink{SpanID: 123, TraceID: 456})
		sp.AddLink(SpanLink{SpanID: 789, TraceID: 012})
		sp.Finish()

		internalSpan := sp
		raw, ok := internalSpan.getMeta("_dd.span_links")
		require.True(t, ok, "Expected _dd.span_links in Meta after adding links.")

		var links []SpanLink
		err = json.Unmarshal([]byte(raw), &links)
		require.NoError(t, err, "Failed to unmarshal links JSON")
		require.Len(t, links, 2, "Expected 2 links in _dd.span_links JSON")

		assert.Equal(t, uint64(123), links[0].SpanID)
		assert.Equal(t, uint64(456), links[0].TraceID)
		assert.Equal(t, uint64(789), links[1].SpanID)
		assert.Equal(t, uint64(012), links[1].TraceID)
	})
}

func TestStatsAfterFinish(t *testing.T) {
	t.Run("peerServiceDefaults-enabled", func(t *testing.T) {
		tracer, err := newTracer(
			WithPeerServiceDefaults(true),
			WithStatsComputation(true),
		)
		assert.NoError(t, err)
		defer tracer.Stop()
		setGlobalTracer(tracer)

		transport := newDummyTransport()
		tracer.config.transport = transport
		tracer.config.agent.Stats = true
		tracer.config.agent.DropP0s = true
		tracer.config.agent.peerTags = []string{"peer.service"}

		c := newConcentrator(tracer.config, (10 * time.Second).Nanoseconds(), &statsd.NoOpClientDirect{})
		assert.Len(t, transport.Stats(), 0)
		c.Start()
		tracer.stats = c

		sp := tracer.StartSpan("sp1")
		sp.SetTag("span.kind", "client")
		sp.SetTag("messaging.system", "kafka")
		sp.SetTag("messaging.kafka.bootstrap.servers", "kafka-cluster")
		sp.SetTag(keyMeasured, 1)
		sp.Finish()

		mt, ok := sp.getMeta("peer.service")
		assert.True(t, ok)
		assert.Equal(t, "kafka-cluster", mt)

		// peer.service has been added on the span.Finish() call. Ensure the StatSpan is also accessing this.
		c.Stop()
		stats := transport.Stats()
		assert.Equal(t, 1, len(stats))
		peerTags := stats[0].Stats[0].Stats[0].PeerTags
		assert.Contains(t, peerTags, "peer.service:kafka-cluster")
	})
	t.Run("peerServiceDefaults-disabled", func(t *testing.T) {
		tracer, err := newTracer(
			WithPeerServiceDefaults(false),
			WithStatsComputation(true),
		)
		assert.NoError(t, err)
		defer tracer.Stop()
		setGlobalTracer(tracer)

		transport := newDummyTransport()
		tracer.config.transport = transport
		tracer.config.agent.Stats = true
		tracer.config.agent.DropP0s = true
		tracer.config.agent.peerTags = []string{"peer.service"}

		c := newConcentrator(tracer.config, (10 * time.Second).Nanoseconds(), &statsd.NoOpClientDirect{})
		assert.Len(t, transport.Stats(), 0)
		c.Start()
		tracer.stats = c

		sp := tracer.StartSpan("sp1")
		sp.SetTag("span.kind", "client")
		sp.SetTag("messaging.system", "kafka")
		sp.SetTag("messaging.kafka.bootstrap.servers", "kafka-cluster")
		sp.SetTag(keyMeasured, 1)
		sp.Finish()

		mt, _ := sp.getMeta("peer.service")
		assert.Equal(t, "", mt)

		c.Stop()
		stats := transport.Stats()
		assert.Equal(t, 1, len(stats))
		peerTags := stats[0].Stats[0].Stats[0].PeerTags
		assert.Empty(t, peerTags)
	})
}
