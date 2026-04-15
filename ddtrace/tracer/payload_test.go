// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

var fixedTime = now()

// creates a simple span list with n spans
func newSpanList(n int) spanList {
	itoa := map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4", 5: "5"}
	list := make([]*Span, n)
	for i := range n {
		list[i] = newBasicSpan("span.list." + itoa[i%5+1])
		list[i].start = fixedTime
	}
	return list
}

// creates a list of n spans, populated with SpanLinks, SpanEvents, and other fields
func newDetailedSpanList(n int) spanList {
	itoa := map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4", 5: "5"}
	list := make([]*Span, n)
	for i := range n {
		list[i] = newBasicSpan("span.list." + itoa[i%5+1])
		list[i].context.trace.setPropagatingTag(keyDecisionMaker, "1")
		list[i].start = fixedTime
		list[i].service = "golden"
		list[i].resource = "resource." + itoa[i%5+1]
		list[i].error = int32(i % 2)
		list[i].SetTag("tag."+itoa[i%5+1], "value."+itoa[i%5+1])
		list[i].spanLinks = []SpanLink{{TraceID: 1, SpanID: 1}, {TraceID: 2, SpanID: 2}}
		list[i].spanEvents = []spanEvent{{Name: "span.event." + itoa[i%5+1]}}
	}
	return list
}

// creates a list of n spans, populated with repetitive tags
func newLowCardinalitySpanList(n int) spanList {
	itoa := map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4", 5: "5"}
	list := make([]*Span, n)
	for i := range n {
		list[i] = newBasicSpan("span.list." + itoa[i%5+1])
		list[i].start = fixedTime
		list[i].service = "high-cardinality-string-value"
		list[i].resource = "resource." + itoa[i%5+1]
		list[i].SetTag("tag.1", "high-cardinality-string-value")
		list[i].SetTag("tag.2", "high-cardinality-string-value")
		list[i].SetTag("tag.3", "high-cardinality-string-value")
		list[i].SetTag("tag.4", "high-cardinality-string-value")
	}
	return list
}

// creates a list of n spans, populated with many unique tags
func newHighCardinalitySpanList(n int) spanList {
	itoa := map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4", 5: "5"}
	list := make([]*Span, n)
	for i := range n {
		list[i] = newBasicSpan("span.list." + itoa[i%5+1])
		list[i].start = fixedTime
		list[i].service = "service." + itoa[i%5+1]
		list[i].resource = "resource." + itoa[i%5+1]
		for i := range 50 {
			list[i].SetTag("tag."+itoa[i%5+1], "value."+itoa[i%5+1])
		}
	}
	return list
}

// TestPayloadIntegrity tests that whatever we push into the payload
// allows us to read the same content as would have been encoded by
// the codec.
func TestPayloadIntegrity(t *testing.T) {
	want := new(bytes.Buffer)
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			assert := assert.New(t)
			p := newPayload(traceProtocolV04)
			lists := make(spanLists, n)
			for i := range n {
				list := newSpanList(i%5 + 1)
				lists[i] = list
				_, _ = p.push(list)
			}
			want.Reset()
			err := msgp.Encode(want, lists)
			assert.NoError(err)
			stats := p.stats()
			assert.Equal(want.Len(), stats.size)
			assert.Equal(n, stats.itemCount)

			got, err := io.ReadAll(p)
			assert.NoError(err)
			assert.Equal(want.Bytes(), got)
		})
	}
}

// TestPayloadV04Decode ensures that whatever we push into a v0.4 payload can
// be decoded by the codec.
func TestPayloadV04Decode(t *testing.T) {
	for _, n := range []int{10, 1 << 10} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			assert := assert.New(t)
			p := newPayload(traceProtocolV04)
			for i := range n {
				_, _ = p.push(newSpanList(i%5 + 1))
			}
			var got spanLists
			err := msgp.Decode(p, &got)
			assert.NoError(err)
			assertProcessTags(t, got)
		})
	}
}

// TestPayloadV1Decode ensures that whatever we push into a v1 payload can
// be decoded by the codec, and that it matches the original payload.
func TestPayloadV1Decode(t *testing.T) {
	for _, n := range []int{10, 1 << 10} {
		t.Run("simple"+strconv.Itoa(n), func(t *testing.T) {
			var (
				assert = assert.New(t)
				p      = newPayloadV1()
			)
			p.SetContainerID("containerID")
			p.SetLanguageName("go")
			p.SetLanguageVersion("1.25")
			p.SetTracerVersion(version.Tag)
			p.SetRuntimeID(globalconfig.RuntimeID())
			p.SetEnv("test")
			p.SetHostname("hostname")
			p.SetAppVersion("appVersion")

			for i := range n {
				_, _ = p.push(newSpanList(i%5 + 1))
			}

			encoded, err := io.ReadAll(p)
			assert.NoError(err)

			got := newPayloadV1()
			buf := bytes.NewBuffer(encoded)
			_, err = buf.WriteTo(got)
			assert.NoError(err)

			o, err := got.decodeBuffer()
			assert.NoError(err)
			assert.Empty(o)
			assert.Equal(p.fields, got.fields)
			assert.Equal(p.containerID, got.containerID)
			assert.Equal(p.languageName, got.languageName)
			assert.Equal(p.languageVersion, got.languageVersion)
			assert.Equal(p.tracerVersion, got.tracerVersion)
			assert.Equal(p.runtimeID, got.runtimeID)
			assert.Equal(p.env, got.env)
			assert.Equal(p.hostname, got.hostname)
			assert.Equal(p.appVersion, got.appVersion)
			assert.Equal(p.fields, got.fields)
		})

		t.Run("detailed"+strconv.Itoa(n), func(t *testing.T) {
			var (
				assert = assert.New(t)
				p      = newPayloadV1()
			)

			for i := range n {
				_, _ = p.push(newDetailedSpanList(i%5 + 1))
			}
			encoded, err := io.ReadAll(p)
			assert.NoError(err)

			got := newPayloadV1()
			buf := bytes.NewBuffer(encoded)
			_, err = buf.WriteTo(got)
			assert.NoError(err)

			o, err := got.decodeBuffer()
			assert.NoError(err)
			assert.Empty(o)
			assert.NotEmpty(got.attributes)
			assert.Equal(p.attributes, got.attributes)
			assert.Equal(got.attributes[keyProcessTags].value, processtags.GlobalTags().String())
			assert.Greater(len(got.chunks), 0)
			assert.Equal(p.chunks[0].traceID, got.chunks[0].traceID)
			assert.Equal(p.chunks[0].spans[0].spanID, got.chunks[0].spans[0].spanID)
			assert.Equal(got.chunks[0].attributes["service"].value, "golden")
			assert.Equal(uint32(1), got.chunks[0].samplingMechanism)
		})

		// Test that a span with no decision maker does not error
		t.Run("no decision maker", func(t *testing.T) {
			var (
				assert = assert.New(t)
				p      = newPayloadV1()
			)

			s := newBasicSpan("span.list")
			s.context.trace.replacePropagatingTags(map[string]string{"keyDecisionMaker": ""})
			p.push([]*Span{s})
			encoded, err := io.ReadAll(p)
			assert.NoError(err)

			got := newPayloadV1()
			buf := bytes.NewBuffer(encoded)
			_, err = buf.WriteTo(got)
			assert.NoError(err)

			o, err := got.decodeBuffer()
			assert.NoError(err)
			assert.Empty(o)
			assert.Greater(len(got.chunks), 0)
			assert.Equal(p.chunks[0].traceID, got.chunks[0].traceID)
			assert.Equal(p.chunks[0].spans[0].spanID, got.chunks[0].spans[0].spanID)
		})

		t.Run("with priority", func(t *testing.T) {
			var (
				assert = assert.New(t)
				p      = newPayloadV1()
			)
			p.SetContainerID("containerID")
			p.SetLanguageName("go")
			p.SetLanguageVersion("1.25")
			p.SetTracerVersion(version.Tag)
			p.SetRuntimeID(globalconfig.RuntimeID())
			p.SetEnv("test")
			p.SetHostname("hostname")
			p.SetAppVersion("appVersion")

			for i := range n {
				sl := newSpanList(i%5 + 1)
				sl[0].context.trace.setSamplingPriority(1, samplernames.Manual)
				_, _ = p.push(sl)
			}

			encoded, err := io.ReadAll(p)
			assert.NoError(err)

			got := newPayloadV1()
			buf := bytes.NewBuffer(encoded)
			_, err = buf.WriteTo(got)
			assert.NoError(err)

			o, err := got.decodeBuffer()
			assert.NoError(err)
			assert.Empty(o)
			assert.Equal(uint32(4), got.chunks[0].samplingMechanism)
			assert.Equal(int32(1), got.chunks[0].priority)
		})

		t.Run("with meta_struct", func(t *testing.T) {
			var (
				assert = assert.New(t)
				p      = newPayloadV1()
			)
			for i := range n {
				sl := newSpanList(i%5 + 1)
				createMetaStructMap(sl)
				_, _ = p.push(sl)
			}

			encoded, err := io.ReadAll(p)
			assert.NoError(err)

			got := newPayloadV1()
			buf := bytes.NewBuffer(encoded)
			_, err = buf.WriteTo(got)
			assert.NoError(err)

			o, err := got.decodeBuffer()
			assert.NoError(err)
			assert.Empty(o)
			meta := got.chunks[0].spans[0].metaStruct
			assert.Equal(int64(1), meta["key1"])
			assert.Equal("value2", meta["key2"])
			assert.Equal([]any{int64(1), int64(2), int64(3)}, meta["key3"])
			assert.Equal(true, meta["key4"])
			assert.Equal([]byte("test"), meta["key5"])
			assert.Equal(map[string]any{"nested-key": "nested-value"}, meta["key6"])
		})
	}
}

func createMetaStructMap(sl spanList) {
	s := sl[0]
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setMetaStructLocked("key1", 1)
	s.setMetaStructLocked("key2", "value2")
	s.setMetaStructLocked("key3", []int64{1, 2, 3})
	s.setMetaStructLocked("key4", true)
	s.setMetaStructLocked("key5", []byte("test"))
	s.setMetaStructLocked("key6", map[string]any{"nested-key": "nested-value"})
}

func TestPayloadV1SpanLinkTraceID(t *testing.T) {
	assert := assert.New(t)
	p := newPayloadV1()

	span := newBasicSpan("test.span")
	span.spanLinks = []SpanLink{
		{TraceID: 123, TraceIDHigh: 456, SpanID: 789},
	}
	span.setMeta("_dd.span_links", "test") // should not get serialized

	_, err := p.push(spanList{span})
	assert.NoError(err)

	encoded, err := io.ReadAll(p)
	assert.NoError(err)

	got := newPayloadV1()
	buf := bytes.NewBuffer(encoded)
	_, err = buf.WriteTo(got)
	assert.NoError(err)

	_, err = got.decodeBuffer()
	assert.NoError(err)

	require.Len(t, got.chunks, 1)
	require.Len(t, got.chunks[0].spans, 1)
	require.Len(t, got.chunks[0].spans[0].spanLinks, 1)

	link := got.chunks[0].spans[0].spanLinks[0]
	assert.Equal(uint64(123), link.TraceID)
	assert.Equal(uint64(456), link.TraceIDHigh)
	assert.Equal(uint64(789), link.SpanID)

	span = got.chunks[0].spans[0]
	assert.False(span.meta.Has("_dd.span_links"))
}

// TestPayloadV1SpanEventArray tests that a span with a span event containing ArrayValue
// attributes (string, int, float, bool) serializes and deserializes correctly in payload v1.
// This covers all types supported by encodeSpanEventArrayValues.
func TestPayloadV1SpanEventArray(t *testing.T) {
	assert := assert.New(t)
	p := newPayloadV1()

	span := newBasicSpan("test.span")
	span.supportsEvents = true
	span.AddEvent("test.event", WithSpanEventAttributes(map[string]any{
		"tags":   []string{"first", "second"},
		"ids":    []int64{10, 20},
		"scores": []float64{1.5, 2.5},
		"flags":  []bool{true, false},
	}))
	_, err := p.push(spanList{span})
	assert.NoError(err)

	encoded, err := io.ReadAll(p)
	assert.NoError(err)

	got := newPayloadV1()
	buf := bytes.NewBuffer(encoded)
	_, err = buf.WriteTo(got)
	assert.NoError(err)

	_, err = got.decodeBuffer()
	assert.NoError(err)

	require.Len(t, got.chunks, 1)
	require.Len(t, got.chunks[0].spans, 1)
	require.Len(t, got.chunks[0].spans[0].spanEvents, 1)

	event := got.chunks[0].spans[0].spanEvents[0]
	assert.Equal("test.event", event.Name)

	// String array
	require.NotNil(t, event.Attributes["tags"])
	tags := event.Attributes["tags"]
	assert.Equal(spanEventAttributeTypeArray, tags.Type)
	require.NotNil(t, tags.ArrayValue)
	require.Len(t, tags.ArrayValue.Values, 2)
	assert.Equal(spanEventArrayAttributeValueTypeString, tags.ArrayValue.Values[0].Type)
	assert.Equal("first", tags.ArrayValue.Values[0].StringValue)
	assert.Equal(spanEventArrayAttributeValueTypeString, tags.ArrayValue.Values[1].Type)
	assert.Equal("second", tags.ArrayValue.Values[1].StringValue)

	// Int array
	require.NotNil(t, event.Attributes["ids"])
	ids := event.Attributes["ids"]
	assert.Equal(spanEventAttributeTypeArray, ids.Type)
	require.NotNil(t, ids.ArrayValue)
	require.Len(t, ids.ArrayValue.Values, 2)
	assert.Equal(spanEventArrayAttributeValueTypeInt, ids.ArrayValue.Values[0].Type)
	assert.Equal(int64(10), ids.ArrayValue.Values[0].IntValue)
	assert.Equal(spanEventArrayAttributeValueTypeInt, ids.ArrayValue.Values[1].Type)
	assert.Equal(int64(20), ids.ArrayValue.Values[1].IntValue)

	// Float array
	require.NotNil(t, event.Attributes["scores"])
	scores := event.Attributes["scores"]
	assert.Equal(spanEventAttributeTypeArray, scores.Type)
	require.NotNil(t, scores.ArrayValue)
	require.Len(t, scores.ArrayValue.Values, 2)
	assert.Equal(spanEventArrayAttributeValueTypeDouble, scores.ArrayValue.Values[0].Type)
	assert.Equal(1.5, scores.ArrayValue.Values[0].DoubleValue)
	assert.Equal(spanEventArrayAttributeValueTypeDouble, scores.ArrayValue.Values[1].Type)
	assert.Equal(2.5, scores.ArrayValue.Values[1].DoubleValue)

	// Bool array
	require.NotNil(t, event.Attributes["flags"])
	flags := event.Attributes["flags"]
	assert.Equal(spanEventAttributeTypeArray, flags.Type)
	require.NotNil(t, flags.ArrayValue)
	require.Len(t, flags.ArrayValue.Values, 2)
	assert.Equal(spanEventArrayAttributeValueTypeBool, flags.ArrayValue.Values[0].Type)
	assert.True(flags.ArrayValue.Values[0].BoolValue)
	assert.Equal(spanEventArrayAttributeValueTypeBool, flags.ArrayValue.Values[1].Type)
	assert.False(flags.ArrayValue.Values[1].BoolValue)
}

// TestPayloadV1EmbeddedStreamingStringTable tests that string values on the payload
// can be encoded and decoded correctly after using the string table.
// Tests repeated string values.
func TestPayloadV1EmbeddedStreamingStringTable(t *testing.T) {
	// Reset process tags to ensure deterministic payload sizes
	processtags.Reload()

	p := newPayloadV1()
	p.SetHostname("production")
	p.SetEnv("production")
	p.SetLanguageName("go")

	assert := assert.New(t)
	encoded, err := io.ReadAll(p)
	assert.NoError(err)

	got := newPayloadV1()
	buf := bytes.NewBuffer(encoded)
	_, err = buf.WriteTo(got)
	assert.NoError(err)

	o, err := got.decodeBuffer()
	assert.NoError(err)
	assert.Empty(o)
	assert.Equal(p.languageName, got.languageName)
	assert.Equal(p.hostname, got.hostname)
	assert.Equal(p.env, got.env)
}

// TestPayloadV1UpdateHeader tests that the header of the payload is updated and grown correctly.
func TestPayloadV1UpdateHeader(t *testing.T) {
	testCases := []uint32{ // Number of items
		0,
		15,
		math.MaxUint16,
		math.MaxUint32,
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("n=%d", tc), func(t *testing.T) {
			var (
				p = payloadV1{
					fields: tc,
					header: make([]byte, 8),
				}
				expected []byte
			)
			expected = msgp.AppendMapHeader(expected, tc)
			p.updateHeader()
			if got := p.header[p.readOff:]; !bytes.Equal(expected, got) {
				t.Fatalf("expected %+v, got %+v", expected, got)
			}
		})
	}
}

// TestEmptyPayloadV1 tests that an empty payload can be encoded and decoded correctly.
// Notably, it should send an empty map.
func TestEmptyPayloadV1(t *testing.T) {
	// Reset process tags to ensure deterministic behavior
	processtags.Reload()

	p := newPayloadV1()
	assert := assert.New(t)
	encoded, err := io.ReadAll(p)
	assert.NoError(err)
	length, o, err := msgp.ReadMapHeaderBytes(encoded)
	assert.NoError(err)
	assert.Equal(uint32(0), length)
	assert.Empty(o)
}

// TestPayloadV1IncrementalChunkEncoding verifies that pushing N chunks into the
// same payloadV1 produces a correctly encoded payload where every chunk retains
// its original span data. Each chunk is encoded exactly once as it is pushed;
// the shared string table persists across all pushes so duplicate strings are
// de-duplicated payload-wide.
func TestPayloadV1IncrementalChunkEncoding(t *testing.T) {
	type chunkSpec struct {
		service string
		name    string
		tagKey  string
		tagVal  string
	}
	chunks := []chunkSpec{
		{"svc-a", "op-a", "k", "alpha"},
		{"svc-b", "op-b", "k", "beta"},  // "k" is a duplicate key → exercises cross-chunk string table
		{"svc-c", "op-c", "k", "gamma"}, // third chunk to confirm in-place count update
	}

	p := newPayloadV1()
	for _, c := range chunks {
		s := newBasicSpan(c.name)
		s.service = c.service
		s.SetTag(c.tagKey, c.tagVal)
		_, err := p.push(spanList{s})
		require.NoError(t, err)
	}

	require.Equal(t, len(chunks), p.itemCount(), "payload chunk count must equal number of pushes")

	encoded, err := io.ReadAll(p)
	require.NoError(t, err)

	got := newPayloadV1()
	_, err = bytes.NewBuffer(encoded).WriteTo(got)
	require.NoError(t, err)
	_, err = got.decodeBuffer()
	require.NoError(t, err)

	require.Len(t, got.chunks, len(chunks), "decoded chunk count must match")
	for i, c := range chunks {
		require.Len(t, got.chunks[i].spans, 1, "chunk %d must have 1 span", i)
		s := got.chunks[i].spans[0]
		assert.Equal(t, c.service, s.service, "chunk %d: wrong service", i)
		assert.Equal(t, c.name, s.name, "chunk %d: wrong name", i)
		v, _ := s.meta.Get(c.tagKey)
		assert.Equal(t, c.tagVal, v, "chunk %d: wrong tag value", i)
	}
}

func assertProcessTags(t *testing.T, payload spanLists) {
	assert := assert.New(t)
	for i, spanList := range payload {
		for j, span := range spanList {
			processTags, ok := span.meta.Get(keyProcessTags)
			if i+j == 0 {
				assert.True(ok, "process tags should be present on the first span of each chunk only")
				assert.Contains(processTags, "entrypoint.name", "process tags should have entrypoint.name")
				break
			}
			require.False(t, ok, "process tags should be present on the first span of each chunk only (chunk: %d span: %d)", i, j)
		}
	}
}

func TestPayloadV1SerializationFailure(t *testing.T) {
	t.Run("nil span", func(t *testing.T) {
		assert := assert.New(t)
		p := newPayloadV1()
		sl := newSpanList(1)
		sl = append(sl, nil) // add a nil span

		_, err := p.push(sl)
		assert.NoError(err)

		encoded, err := io.ReadAll(p)
		assert.NoError(err)

		got := newPayloadV1()
		buf := bytes.NewBuffer(encoded)
		_, err = buf.WriteTo(got)
		assert.NoError(err)

		_, err = got.decodeBuffer()
		assert.NoError(err)

		require.Len(t, got.chunks, 1)
		require.Len(t, got.chunks[0].spans, 2)
		assert.Equal(&Span{}, got.chunks[0].spans[1])
	})

	t.Run("invalid valueType", func(t *testing.T) {
		p := newPayloadV1()
		p.attributes["bad-attr"] = anyValue{valueType: 999, value: "x"}
		s := newBasicSpan("test-span")
		_, err := p.push(spanList{s})
		require.NoError(t, err)
		encoded, err := io.ReadAll(p)
		require.NoError(t, err)

		got := newPayloadV1()
		_, err = bytes.NewBuffer(encoded).WriteTo(got)
		require.NoError(t, err)

		_, err = got.decodeBuffer()
		require.NoError(t, err)
		require.NotNil(t, got.attributes["bad-attr"])
		assert.Equal(t, StringValueType, got.attributes["bad-attr"].valueType)
		assert.Equal(t, serializationFailed, got.attributes["bad-attr"].value)
	})

	t.Run("invalid meta struct value", func(t *testing.T) {
		p := newPayloadV1()
		s := newBasicSpan("test-span")
		s.mu.Lock()
		s.setMetaStructLocked("bad-key", make(chan int)) // unsupported type
		s.mu.Unlock()
		_, err := p.push(spanList{s})
		require.NoError(t, err)
		encoded, err := io.ReadAll(p)
		require.NoError(t, err)

		got := newPayloadV1()
		_, err = bytes.NewBuffer(encoded).WriteTo(got)
		require.NoError(t, err)

		_, err = got.decodeBuffer()
		require.NoError(t, err)
		require.Len(t, got.chunks, 1)
		require.Len(t, got.chunks[0].spans, 1)
		ms := got.chunks[0].spans[0].metaStruct["bad-key"]
		require.NotNil(t, ms)
		v, ok := ms.([]byte)
		assert.True(t, ok)
		assert.Equal(t, []byte(serializationFailed), v)
	})
}

func BenchmarkPayloadThroughput(b *testing.B) {
	b.Run("10K", benchmarkPayloadThroughput(1))
	b.Run("100K", benchmarkPayloadThroughput(10))
	b.Run("1MB", benchmarkPayloadThroughput(100))
}

// benchmarkPayloadThroughput benchmarks the throughput of the payload by subsequently
// pushing a trace containing count spans of approximately 10KB in size each, until the
// payload is filled.
func benchmarkPayloadThroughput(count int) func(*testing.B) {
	return func(b *testing.B) {
		p := newPayloadV04()
		s := newBasicSpan("X")
		s.meta.Set("key", strings.Repeat("X", 10*1024))
		trace := make(spanList, count)
		for i := range count {
			trace[i] = s
		}
		b.ReportAllocs()
		b.ResetTimer()
		reset := func() {
			p.header = make([]byte, 8)
			p.off = 8
			atomic.StoreUint32(&p.count, 0)
			p.buf.Reset()
		}
		for b.Loop() {
			reset()
			for p.stats().size < payloadMaxLimit {
				_, _ = p.push(trace)
			}
		}
	}
}

// TestPayloadConcurrentAccess tests that payload operations are safe for concurrent use
func TestPayloadConcurrentAccess(t *testing.T) {
	p := newPayload(traceProtocolV04)

	// Create some test spans
	spans := make(spanList, 10)
	for i := range 10 {
		spans[i] = newBasicSpan("test-span")
	}

	var wg sync.WaitGroup

	// Start multiple goroutines that perform concurrent operations
	for range 10 {
		wg.Go(func() {

			// Push some spans
			for range 5 {
				_, _ = p.push(spans)
			}

			// Read size and item count concurrently
			for range 10 {
				stats := p.stats()
				_ = stats.size
				_ = stats.itemCount
			}
		})
	}

	// Also perform operations from the main goroutine
	wg.Go(func() {
		for range 20 {
			_ = p.stats().size
		}
	})

	wg.Wait()

	// Verify the payload is in a consistent state
	if p.stats().itemCount == 0 {
		t.Error("Expected payload to have items after concurrent operations")
	}

	if p.stats().size <= 0 {
		t.Error("Expected payload size to be positive after concurrent operations")
	}
}

// TestPayloadConcurrentReadWrite tests concurrent read and write operations
func TestPayloadConcurrentReadWrite(t *testing.T) {
	p := newPayload(traceProtocolV04)

	// Add some initial data
	span := newBasicSpan("test")
	spans := spanList{span}
	_, _ = p.push(spans)

	var wg sync.WaitGroup

	// Concurrent writers
	for range 5 {
		wg.Go(func() {
			for range 10 {
				_, _ = p.push(spans)
			}
		})
	}

	// Concurrent readers
	for range 5 {
		wg.Go(func() {
			buf := make([]byte, 1024)
			for range 10 {
				p.reset()
				_, _ = p.Read(buf)
			}
		})
	}

	// Concurrent size/count checkers
	for range 3 {
		wg.Go(func() {
			for range 20 {
				stats := p.stats()
				_ = stats.size
				_ = stats.itemCount
			}
		})
	}

	wg.Wait()

	// Verify final state
	if p.stats().itemCount == 0 {
		t.Error("Expected payload to have items")
	}
}

func BenchmarkPayloadPush(b *testing.B) {
	sizes := []struct {
		name     string
		numSpans int
		spanSize int
	}{
		{"1span_1KB", 1, 1},
		{"5span_1KB", 5, 1},
		{"10span_1KB", 10, 1},
		{"1span_10KB", 1, 10},
		{"5span_10KB", 5, 10},
		{"10span_50KB", 10, 50},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			spans := make(spanList, size.numSpans)
			for i := 0; i < size.numSpans; i++ {
				span := newBasicSpan("benchmark-span")
				span.meta.Set("data", strings.Repeat("x", size.spanSize*1024))
				spans[i] = span
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				p := newPayloadV04()
				_, _ = p.push(spans)
			}
		})
	}
}

func BenchmarkPayloadStats(b *testing.B) {
	tests := []struct {
		name      string
		numTraces int
		spansPer  int
	}{
		{"empty", 0, 0},
		{"small_1trace_1span", 1, 1},
		{"medium_10trace_5span", 10, 5},
		{"large_100trace_10span", 100, 10},
	}

	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			p := newPayload(traceProtocolV04)

			for i := 0; i < test.numTraces; i++ {
				spans := make(spanList, test.spansPer)
				for j := 0; j < test.spansPer; j++ {
					spans[j] = newBasicSpan("test-span")
				}
				_, _ = p.push(spans)
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				stats := p.stats()
				_ = stats.size
				_ = stats.itemCount
			}
		})
	}
}

func BenchmarkPayloadConcurrentAccess(b *testing.B) {
	concurrencyLevels := []int{1, 2, 4, 8}

	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("concurrency_%d", concurrency), func(b *testing.B) {
			p := newPayload(traceProtocolV04)
			span := newBasicSpan("concurrent-test")
			spans := spanList{span}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				var wg sync.WaitGroup

				for range concurrency {
					wg.Go(func() {
						_, _ = p.push(spans)
					})
				}

				for range concurrency {
					wg.Go(func() {
						_ = p.stats()
					})
				}

				wg.Wait()
				p.clear()
			}
		})
	}
}

func TestMsgsizeAnalysis(t *testing.T) {
	sizes := []int{1, 5, 10}
	for _, numSpans := range sizes {
		spans := make(spanList, numSpans)
		for i := range numSpans {
			span := newBasicSpan("test")
			span.meta.Set("data", strings.Repeat("x", 1024))
			spans[i] = span
		}

		msgsize := spans.Msgsize()
		t.Logf("%d spans with 1KB each: msgsize=%d bytes", numSpans, msgsize)
	}
}

func BenchmarkPayloadVersions(b *testing.B) {
	sizes := []int{1, 10, 100, 1000}
	for _, n := range sizes {
		spans := newSpanList(n)
		detailedSpans := newDetailedSpanList(n)

		b.Run(fmt.Sprintf("simple_%dspans/v0.4", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				p := newPayloadV04()
				_, _ = p.push(spans)
			}
		})

		b.Run(fmt.Sprintf("simple_%dspans/v1.0", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				p := getPayloadV1()
				_, _ = p.push(spans)
				putPayloadV1(p)
			}
		})

		b.Run(fmt.Sprintf("detailed_%dspans/v0.4", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				p := newPayloadV04()
				_, _ = p.push(detailedSpans)
			}
		})

		b.Run(fmt.Sprintf("detailed_%dspans/v1.0", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				p := getPayloadV1()
				_, _ = p.push(detailedSpans)
				putPayloadV1(p)
			}
		})

		b.Run(fmt.Sprintf("metastruct_%dspans/v1.0", n), func(b *testing.B) {
			metaStructSpans := newSpanList(n)
			createMetaStructMap(metaStructSpans)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				p := newPayloadV1()
				_, _ = p.push(metaStructSpans)
			}
		})
	}
}

func BenchmarkPayloads(b *testing.B) {
	b.Run("v0.4", func(b *testing.B) {
		b.Run("push/10spans", func(b *testing.B) {
			p := newPayloadV04()
			sl := newSpanList(10)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("push/1000spans", func(b *testing.B) {
			p := newPayloadV04()
			sl := newSpanList(1000)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("push/10_detailed_spans", func(b *testing.B) {
			p := newPayloadV04()
			sl := newDetailedSpanList(10)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("push/1000_detailed_spans", func(b *testing.B) {
			p := newPayloadV04()
			sl := newDetailedSpanList(1000)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("push/low_cardinality_spans", func(b *testing.B) {
			p := newPayloadV04()
			sl := newLowCardinalitySpanList(1000)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("push/high_cardinality_spans", func(b *testing.B) {
			p := newPayloadV04()
			sl := newHighCardinalitySpanList(1000)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("flush/1span", func(b *testing.B) {
			p := newPayloadV04()

			p.push(newSpanList(1))

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				p.reset()
				io.ReadAll(p)
			}
		})

		b.Run("flush/100spans", func(b *testing.B) {
			p := newPayloadV04()

			p.push(newSpanList(100))

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				p.reset()
				io.ReadAll(p)
			}
		})

		b.Run("flush/1000spans", func(b *testing.B) {
			p := newPayloadV04()

			p.push(newSpanList(1000))

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				p.reset()
				io.ReadAll(p)
			}
		})
	})

	b.Run("v1", func(b *testing.B) {
		b.Run("push/10spans", func(b *testing.B) {
			p := newPayloadV1()
			sl := newSpanList(10)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("push/1000spans", func(b *testing.B) {
			p := newPayloadV1()
			sl := newSpanList(1000)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("push/10_detailed_spans", func(b *testing.B) {
			p := newPayloadV1()
			sl := newDetailedSpanList(10)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("push/1000_detailed_spans", func(b *testing.B) {
			p := newPayloadV1()
			sl := newDetailedSpanList(1000)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("push/low_cardinality_spans", func(b *testing.B) {
			p := newPayloadV1()
			sl := newLowCardinalitySpanList(1000)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("push/high_cardinality_spans", func(b *testing.B) {
			p := newPayloadV1()
			sl := newHighCardinalitySpanList(1000)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = p.push(sl)
			}
		})

		b.Run("flush/1span", func(b *testing.B) {
			p := newPayloadV1()

			p.push(newSpanList(1))

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				p.reset()
				io.ReadAll(p)
			}
		})

		b.Run("flush/100spans", func(b *testing.B) {
			p := newPayloadV1()

			p.push(newSpanList(100))

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				p.reset()
				io.ReadAll(p)
			}
		})

		b.Run("flush/1000spans", func(b *testing.B) {
			p := newPayloadV1()

			p.push(newSpanList(1000))

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				p.reset()
				io.ReadAll(p)
			}
		})
	})

	// ... Add more payload versions here...
}
