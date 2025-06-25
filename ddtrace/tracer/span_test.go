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
	spanContext := newSpanContext(rootSpan, nil)
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

	// check that the span does not have any span links serialized
	// spans don't have span links by default and they are serialized in the meta map
	// as part of the Finish call
	assert.Zero(span.meta["_dd.span_links"])

	// manipulate the span
	span.AddLink(SpanLink{
		TraceID: span.traceID,
		SpanID:  span.spanID,
		Attributes: map[string]string{
			"manual.keep": "true",
		},
	})

	previousDuration := span.duration
	time.Sleep(wait)
	span.Finish()

	assert.Equal(previousDuration, span.duration)
	assert.Zero(span.meta["_dd.span_links"])

	tracer.awaitPayload(t, 1) // this checks that no other span was seen by the tracerWriter
}

func TestSpanFinishNilOption(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	assert.NoError(err)

	tc := []struct {
		name    string
		wantErr bool
		options []FinishOption
	}{
		{
			name:    "all nil options",
			options: []FinishOption{nil, nil, nil},
			wantErr: false,
		},
		{
			name:    "nil options at end",
			options: []FinishOption{WithError(errors.New("test error")), nil, nil},
			wantErr: true,
		},
		{
			name:    "nil options at beginning and end",
			options: []FinishOption{nil, WithError(errors.New("test error")), nil},
			wantErr: true,
		},
		{
			name:    "nil options at beginning",
			options: []FinishOption{nil, nil, WithError(errors.New("test error"))},
			wantErr: true,
		},
	}

	for _, tc := range tc {
		t.Run(tc.name, func(_ *testing.T) {
			span := tracer.newRootSpan("pylons.request", "pylons", "/")
			span.Finish(tc.options...)
			if tc.wantErr {
				assert.Equal(tc.wantErr, span.error != 0)
				assert.Equal(span.meta[ext.ErrorMsg], "test error")
				assert.Equal(span.meta[ext.ErrorType], "*errors.errorString")
			} else {
				assert.Equal(span.error, int32(0))
				assert.Empty(span.meta[ext.ErrorMsg])
				assert.Empty(span.meta[ext.ErrorType])
			}
		})
	}
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
			s.context.errors.Store(tt.errors)
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
}

// String causes panic which SetTag should not handle.
func (p *panicStringer) String() string {
	panic("This should not be handled.")
}

func TestSpanSetTag(t *testing.T) {
	assert := assert.New(t)
	span := newBasicSpan("web.request")
	assert.Equal("web.request", span.name)

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

	mapStrStr := map[string]string{"b": "c"}
	span.SetTag("map", sharedinternal.MetaStructValue{Value: map[string]string{"b": "c"}})
	assert.Equal(mapStrStr, span.metaStruct["map"])

	mapOfMap := map[string]map[string]any{"a": {"b": "c"}}
	span.SetTag("mapOfMap", sharedinternal.MetaStructValue{Value: mapOfMap})
	assert.Equal(mapOfMap, span.metaStruct["mapOfMap"])

	// testMsgpStruct is a struct that implements the msgp.Marshaler interface
	testValue := &testMsgpStruct{A: "test"}
	span.SetTag("struct", sharedinternal.MetaStructValue{Value: testValue})
	require.Equal(t, testValue, span.metaStruct["struct"])

	mapStrStr = map[string]string{"b": "c"}
	span.SetTag("map", sharedinternal.MetaStructValue{Value: map[string]string{"b": "c"}})
	assert.Equal(mapStrStr, span.metaStruct["map"])

	mapOfMap = map[string]map[string]any{"a": {"b": "c"}}
	span.SetTag("mapOfMap", sharedinternal.MetaStructValue{Value: mapOfMap})
	assert.Equal(mapOfMap, span.metaStruct["mapOfMap"])

	// testMsgpStruct is a struct that implements the msgp.Marshaler interface
	testValue = &testMsgpStruct{A: "test"}
	span.SetTag("struct", sharedinternal.MetaStructValue{Value: testValue})
	require.Equal(t, testValue, span.metaStruct["struct"])

	s := "string"
	span.SetTag("str_ptr", &s)
	assert.Equal(s, span.meta["str_ptr"])

	span.SetTag("nil_str_ptr", (*string)(nil))
	assert.Equal("", span.meta["nil_str_ptr"])

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
		span.setTagError(errors.New("error value with no trace"), errorConfig{noDebugStack: true})
		assert.Empty(t, span.meta[ext.ErrorStack])
	})

	t.Run("on", func(t *testing.T) {
		span := newBasicSpan("web.request")
		span.setTagError(errors.New("error value with trace"), errorConfig{noDebugStack: false})
		assert.NotEmpty(t, span.meta[ext.ErrorStack])
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

func TestSpanStartNilOption(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	assert.NoError(err)

	tc := []struct {
		name    string
		wantTag bool
		options []StartSpanOption
	}{
		{
			name:    "all nil options",
			options: []StartSpanOption{nil, nil, nil},
			wantTag: false,
		},
		{
			name:    "nil options at end",
			options: []StartSpanOption{Tag("tag", "value"), nil, nil},
			wantTag: true,
		},
		{
			name:    "nil options at beginning and end",
			options: []StartSpanOption{nil, Tag("tag", "value"), nil},
			wantTag: true,
		},
		{
			name:    "nil options at beginning",
			options: []StartSpanOption{nil, nil, Tag("tag", "value")},
			wantTag: true,
		},
	}

	for _, tc := range tc {
		t.Run(tc.name, func(_ *testing.T) {
			span := tracer.StartSpan("pylons.request", tc.options...)
			if tc.wantTag {
				assert.Equal(tc.wantTag, span.meta["tag"] == "value")
			} else {
				assert.Empty(span.meta["tag"])
			}
		})
	}
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
	assert := assert.New(t)
	tracer, err := newTracer(withTransport(newDefaultTransport()))
	assert.NoError(err)
	setGlobalTracer(tracer)
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
	setGlobalTracer(tracer)
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
		span.setSamplingPriority(priority, samplernames.Default)
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
		expect := fmt.Sprintf(`dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.context.TraceID(), span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	}
	t.Run("noservice_first", noServiceTest)

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.context.TraceID(), span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.context.TraceID(), span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("version", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.context.TraceID(), span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("full", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.context.TraceID(), span.spanID)
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
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.context.TraceID(), span.spanID)
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
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.context.TraceID(), span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("badformat", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`%%!b(tracer.Span=dd.service=tracer.test dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0")`, span.context.TraceID(), span.spanID)
		assert.Equal(expect, fmt.Sprintf("%b", span))
	})

	t.Run("notracer/options", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		assert.Nil(err)
		span := tracer.StartSpan("test.request")
		stop()
		// no service, env, or version after the tracer is stopped
		expect := fmt.Sprintf(`dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.context.TraceID(), span.spanID)
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
		expect := fmt.Sprintf(`dd.env=testenv dd.version=1.2.3 dd.trace_id="%s" dd.span_id="%d" dd.parent_id="0"`, span.context.TraceID(), span.spanID)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("128-bit-logging-default", func(t *testing.T) {
		// Generate and log 128 bit trace ids by default
		assert := assert.New(t)
		tracer, _, _, stop, err := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		assert.Nil(err)
		defer stop()
		span := tracer.StartSpan("test.request")
		span.spanID = 87654321
		span.Finish()
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id=%q dd.span_id="87654321" dd.parent_id="0"`, span.context.TraceID())
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
		v, _ := getMeta(span, keyTraceID128)
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
		span.context.traceID.SetUpper(1)
		span.Finish()
		assert.Equal(`dd.service=tracer.test dd.env=testenv dd.trace_id="00000000000000010000000005397fb1" dd.span_id="87654321" dd.parent_id="0"`, fmt.Sprintf("%v", span))
		v, _ := getMeta(span, keyTraceID128)
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
		assert.False(span.context.traceID.HasUpper()) // it should not have generated upper bits
		assert.Equal(`dd.service=tracer.test dd.env=testenv dd.trace_id="87654321" dd.span_id="87654321" dd.parent_id="0"`, fmt.Sprintf("%v", span))
		v, _ := getMeta(span, keyTraceID128)
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
		span.traceID = 12345678
		span.spanID = 87654321
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		span.serializeSpanLinksInMeta()
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
		_, ok := internalSpan.meta["_dd.span_links"]
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
		raw, ok := internalSpan.meta["_dd.span_links"]
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

		assert.Equal(t, "kafka-cluster", sp.meta["peer.service"])

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

		assert.Equal(t, "", sp.meta["peer.service"])

		c.Stop()
		stats := transport.Stats()
		assert.Equal(t, 1, len(stats))
		peerTags := stats[0].Stats[0].Stats[0].PeerTags
		assert.Empty(t, peerTags)
	})
}

func TestSpanErrorStackNoDebugStackInteraction(t *testing.T) {
	tracer, err := newTracer()
	require.NoError(t, err)
	defer tracer.Stop()

	sp := tracer.StartSpan("test-error-stack")
	sp.SetTag("error.stack", "boom")
	sp.Finish(
		WithError(errors.New("test error")),
		NoDebugStack(),
	)

	assert.Equal(t, "boom", sp.meta["error.stack"])
}
