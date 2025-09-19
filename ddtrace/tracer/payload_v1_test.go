// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package tracer

import (
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer/idx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV1Payload_BasicFunctionality(t *testing.T) {
	payload := newV1Payload(1.0)

	// Test initial state
	assert.Equal(t, 0, payload.itemCount())
	assert.Equal(t, 0, payload.size())
	assert.Equal(t, 1.0, payload.protocol())

	// Test clear
	payload.clear()
	assert.Equal(t, 0, payload.itemCount())
	assert.Equal(t, 0, payload.size())
}

func TestV1Payload_PushSingleSpan(t *testing.T) {
	payload := newV1Payload(1.0)

	// Create a test span
	span := &Span{
		name:     "test.operation",
		service:  "test.service",
		resource: "/test/resource",
		spanType: "web",
		start:    time.Now().UnixNano(),
		duration: 1000000, // 1ms
		spanID:   12345,
		traceID:  67890,
		parentID: 0,
		error:    0,
		meta: map[string]string{
			"env":       "test",
			"version":   "1.0.0",
			"component": "test-component",
			"kind":      "server",
			"custom":    "value",
		},
		metrics: map[string]float64{
			"custom.metric": 42.0,
		},
		metaStruct: map[string]interface{}{
			"custom.struct": []byte("struct data"),
		},
		spanLinks: []SpanLink{
			{
				TraceID:     11111,
				TraceIDHigh: 0,
				SpanID:      22222,
				Attributes: map[string]string{
					"link.attr": "link.value",
				},
				Tracestate: "link-tracestate",
				Flags:      0x01,
			},
		},
		spanEvents: []spanEvent{
			{
				Name:         "test.event",
				TimeUnixNano: uint64(time.Now().UnixNano()),
				RawAttributes: map[string]interface{}{
					"event.attr":   "event.value",
					"event.number": 42,
					"event.bool":   true,
				},
			},
		},
	}

	// Push the span
	stats, err := payload.push(spanList{span})
	require.NoError(t, err)

	// Verify stats
	assert.Equal(t, 1, stats.itemCount)
	assert.Greater(t, stats.size, 0)
	assert.Equal(t, 1, payload.itemCount())
	assert.Greater(t, payload.size(), 0)

	// Test reading the payload
	data, err := io.ReadAll(payload)
	require.NoError(t, err)
	assert.Greater(t, len(data), 0)

	// Test unmarshaling with InternalTracerPayload
	var unmarshaledPayload idx.InternalTracerPayload
	remaining, err := unmarshaledPayload.UnmarshalMsg(data)
	require.NoError(t, err)
	assert.Empty(t, remaining)

	// Verify the unmarshaled payload structure
	assert.NotNil(t, unmarshaledPayload.Strings)
	assert.Len(t, unmarshaledPayload.Chunks, 1)

	chunk := unmarshaledPayload.Chunks[0]
	assert.Len(t, chunk.Spans, 1)
	assert.Equal(t, int32(0), chunk.Priority)
	assert.False(t, chunk.DroppedTrace)
	assert.Len(t, chunk.TraceID, 16)

	// Verify span data
	internalSpan := chunk.Spans[0]
	assert.Equal(t, "test.service", internalSpan.Service())
	assert.Equal(t, "test.operation", internalSpan.Name())
	assert.Equal(t, "/test/resource", internalSpan.Resource())
	assert.Equal(t, "web", internalSpan.Type())
	assert.Equal(t, uint64(12345), internalSpan.SpanID())
	assert.Equal(t, uint64(0), internalSpan.ParentID())
	assert.Equal(t, uint64(span.start), internalSpan.Start())
	assert.Equal(t, uint64(span.duration), internalSpan.Duration())
	assert.False(t, internalSpan.Error())
	assert.Equal(t, idx.SpanKind_SPAN_KIND_SERVER, internalSpan.Kind())

	// Verify string references
	assert.Equal(t, "test", internalSpan.Env())
	assert.Equal(t, "1.0.0", internalSpan.Version())
	assert.Equal(t, "test-component", internalSpan.Component())

	// Verify attributes
	attributes := internalSpan.Attributes()
	assert.Len(t, attributes, 3) // custom, custom.metric, custom.struct
	assert.Contains(t, attributes, unmarshaledPayload.Strings.Lookup("custom"))
	assert.Contains(t, attributes, unmarshaledPayload.Strings.Lookup("custom.metric"))
	assert.Contains(t, attributes, unmarshaledPayload.Strings.Lookup("custom.struct"))

	// Verify span links
	links := internalSpan.Links()
	assert.Len(t, links, 1)
	link := links[0]
	assert.Len(t, link.TraceID(), 16)
	assert.Equal(t, uint64(11111), binary.BigEndian.Uint64(link.TraceID()[8:]))
	assert.Equal(t, uint64(22222), link.SpanID())
	assert.Equal(t, "link-tracestate", link.Tracestate())
	assert.Equal(t, uint32(0x01), link.Flags())
	assert.Len(t, link.Attributes(), 1)

	// Verify span events
	events := internalSpan.Events()
	assert.Len(t, events, 1)
	event := events[0]
	assert.Equal(t, "test.event", event.Name())
	// Note: Time and Attributes are not directly accessible through getter methods
	// We can verify the event exists and has the correct name
}

func TestV1Payload_PushMultipleSpans(t *testing.T) {
	payload := newV1Payload(1.0)

	// Create multiple spans with the same trace ID
	spans := make([]*Span, 3)
	for i := 0; i < 3; i++ {
		spans[i] = &Span{
			name:     "test.operation",
			service:  "test.service",
			resource: "/test/resource",
			spanType: "web",
			start:    time.Now().UnixNano(),
			duration: 1000000,
			spanID:   uint64(1000 + i),
			traceID:  12345, // Same trace ID
			parentID: 0,
			error:    0,
			meta: map[string]string{
				"env": "test",
			},
		}
	}

	// Push all spans
	stats, err := payload.push(spanList(spans))
	require.NoError(t, err)

	// Verify stats
	assert.Equal(t, 1, stats.itemCount) // One chunk
	assert.Greater(t, stats.size, 0)
	assert.Equal(t, 1, payload.itemCount())

	// Test reading the payload
	data, err := io.ReadAll(payload)
	require.NoError(t, err)

	// Test unmarshaling
	var unmarshaledPayload idx.InternalTracerPayload
	remaining, err := unmarshaledPayload.UnmarshalMsg(data)
	require.NoError(t, err)
	assert.Empty(t, remaining)

	// Verify structure
	assert.Len(t, unmarshaledPayload.Chunks, 1)
	chunk := unmarshaledPayload.Chunks[0]
	assert.Len(t, chunk.Spans, 3)

	// Verify all spans have the same trace ID
	// Note: TraceID is not directly accessible through getter methods
	// We can verify the chunk has the correct trace ID
	assert.Len(t, chunk.TraceID, 16)
}

func TestV1Payload_PushMultipleChunks(t *testing.T) {
	payload := newV1Payload(1.0)

	// Create spans for different traces
	trace1Spans := []*Span{
		{
			name:     "operation1",
			service:  "service1",
			resource: "/resource1",
			spanType: "web",
			start:    time.Now().UnixNano(),
			duration: 1000000,
			spanID:   1001,
			traceID:  11111,
			parentID: 0,
			error:    0,
			meta:     map[string]string{"env": "test"},
		},
	}

	trace2Spans := []*Span{
		{
			name:     "operation2",
			service:  "service2",
			resource: "/resource2",
			spanType: "db",
			start:    time.Now().UnixNano(),
			duration: 2000000,
			spanID:   2001,
			traceID:  22222,
			parentID: 0,
			error:    0,
			meta:     map[string]string{"env": "test"},
		},
	}

	// Push first chunk
	stats1, err := payload.push(spanList(trace1Spans))
	require.NoError(t, err)
	assert.Equal(t, 1, stats1.itemCount)

	// Push second chunk
	stats2, err := payload.push(spanList(trace2Spans))
	require.NoError(t, err)
	assert.Equal(t, 2, stats2.itemCount)

	// Verify total count
	assert.Equal(t, 2, payload.itemCount())

	// Test reading the payload
	data, err := io.ReadAll(payload)
	require.NoError(t, err)

	// Test unmarshaling
	var unmarshaledPayload idx.InternalTracerPayload
	remaining, err := unmarshaledPayload.UnmarshalMsg(data)
	require.NoError(t, err)
	assert.Empty(t, remaining)

	// Verify structure
	assert.Len(t, unmarshaledPayload.Chunks, 2)

	// Verify first chunk
	chunk1 := unmarshaledPayload.Chunks[0]
	assert.Len(t, chunk1.Spans, 1)
	assert.Equal(t, "operation1", chunk1.Spans[0].Name())
	assert.Equal(t, "service1", chunk1.Spans[0].Service())

	// Verify second chunk
	chunk2 := unmarshaledPayload.Chunks[1]
	assert.Len(t, chunk2.Spans, 1)
	assert.Equal(t, "operation2", chunk2.Spans[0].Name())
	assert.Equal(t, "service2", chunk2.Spans[0].Service())
}

func TestV1Payload_EmptySpanList(t *testing.T) {
	payload := newV1Payload(1.0)

	// Push empty span list
	stats, err := payload.push(spanList{})
	require.NoError(t, err)

	// Should still create a chunk
	assert.Equal(t, 1, stats.itemCount)
	assert.Equal(t, 1, payload.itemCount())

	// Test reading the payload
	data, err := io.ReadAll(payload)
	require.NoError(t, err)

	// Test unmarshaling
	var unmarshaledPayload idx.InternalTracerPayload
	remaining, err := unmarshaledPayload.UnmarshalMsg(data)
	require.NoError(t, err)
	assert.Empty(t, remaining)

	// Verify structure
	assert.Len(t, unmarshaledPayload.Chunks, 1)
	chunk := unmarshaledPayload.Chunks[0]
	assert.Len(t, chunk.Spans, 0)
	assert.False(t, chunk.DroppedTrace)
}

func TestV1Payload_ResetAndClear(t *testing.T) {
	payload := newV1Payload(1.0)

	// Add some data
	span := &Span{
		name:     "test.operation",
		service:  "test.service",
		resource: "/test/resource",
		spanType: "web",
		start:    time.Now().UnixNano(),
		duration: 1000000,
		spanID:   12345,
		traceID:  67890,
		parentID: 0,
		error:    0,
		meta:     map[string]string{"env": "test"},
	}

	_, err := payload.push(spanList{span})
	require.NoError(t, err)

	// Test reset
	payload.reset()
	assert.Equal(t, 1, payload.itemCount()) // Count should remain

	// Test clear
	payload.clear()
	assert.Equal(t, 0, payload.itemCount())
	assert.Equal(t, 0, payload.size())
}

func TestV1Payload_ConcurrentAccess(t *testing.T) {
	payload := newV1Payload(1.0)

	// Test concurrent writes (should be safe due to mutex)
	done := make(chan bool, 2)

	go func() {
		span := &Span{
			name:     "operation1",
			service:  "service1",
			resource: "/resource1",
			spanType: "web",
			start:    time.Now().UnixNano(),
			duration: 1000000,
			spanID:   1001,
			traceID:  11111,
			parentID: 0,
			error:    0,
			meta:     map[string]string{"env": "test"},
		}
		_, err := payload.push(spanList{span})
		assert.NoError(t, err)
		done <- true
	}()

	go func() {
		span := &Span{
			name:     "operation2",
			service:  "service2",
			resource: "/resource2",
			spanType: "db",
			start:    time.Now().UnixNano(),
			duration: 2000000,
			spanID:   2001,
			traceID:  22222,
			parentID: 0,
			error:    0,
			meta:     map[string]string{"env": "test"},
		}
		_, err := payload.push(spanList{span})
		assert.NoError(t, err)
		done <- true
	}()

	// Wait for both goroutines to complete
	<-done
	<-done

	// Verify final state
	assert.Equal(t, 2, payload.itemCount())
}

func TestV1Payload_StringInterning(t *testing.T) {
	payload := newV1Payload(1.0)

	// Create spans with duplicate strings
	spans := []*Span{
		{
			name:     "test.operation",
			service:  "test.service",
			resource: "/test/resource",
			spanType: "web",
			start:    time.Now().UnixNano(),
			duration: 1000000,
			spanID:   1001,
			traceID:  11111,
			parentID: 0,
			error:    0,
			meta:     map[string]string{"env": "test", "custom": "value"},
		},
		{
			name:     "test.operation", // Same name
			service:  "test.service",   // Same service
			resource: "/test/resource", // Same resource
			spanType: "web",            // Same type
			start:    time.Now().UnixNano(),
			duration: 2000000,
			spanID:   1002,
			traceID:  11111, // Same trace
			parentID: 1001,  // Child of first span
			error:    0,
			meta:     map[string]string{"env": "test", "custom": "value"}, // Same meta
		},
	}

	// Push spans
	_, err := payload.push(spanList(spans))
	require.NoError(t, err)

	// Test reading the payload
	data, err := io.ReadAll(payload)
	require.NoError(t, err)

	// Test unmarshaling
	var unmarshaledPayload idx.InternalTracerPayload
	remaining, err := unmarshaledPayload.UnmarshalMsg(data)
	require.NoError(t, err)
	assert.Empty(t, remaining)

	// Verify string interning worked
	assert.Len(t, unmarshaledPayload.Chunks, 1)
	chunk := unmarshaledPayload.Chunks[0]
	assert.Len(t, chunk.Spans, 2)

	// Both spans should have the same string values (string interning should work)
	span1 := chunk.Spans[0]
	span2 := chunk.Spans[1]

	// Verify the strings are actually the same
	assert.Equal(t, "test.service", span1.Service())
	assert.Equal(t, "test.operation", span1.Name())
	assert.Equal(t, "/test/resource", span1.Resource())
	assert.Equal(t, "web", span1.Type())
	assert.Equal(t, "test", span1.Env())

	// Verify both spans have the same values
	assert.Equal(t, span1.Service(), span2.Service())
	assert.Equal(t, span1.Name(), span2.Name())
	assert.Equal(t, span1.Resource(), span2.Resource())
	assert.Equal(t, span1.Type(), span2.Type())
	assert.Equal(t, span1.Env(), span2.Env())
}

func TestV1Payload_ChunkAttributes(t *testing.T) {
	payload := newV1Payload(1.0)

	// Create span with sampling mechanism
	span := &Span{
		name:     "test.operation",
		service:  "test.service",
		resource: "/test/resource",
		spanType: "web",
		start:    time.Now().UnixNano(),
		duration: 1000000,
		spanID:   12345,
		traceID:  67890,
		parentID: 0,
		error:    0,
		meta: map[string]string{
			"env":      "test",
			"_dd.p.dm": "123", // Sampling mechanism
			"custom":   "value",
		},
	}

	// Push span
	_, err := payload.push(spanList{span})
	require.NoError(t, err)

	// Test reading the payload
	data, err := io.ReadAll(payload)
	require.NoError(t, err)

	// Test unmarshaling
	var unmarshaledPayload idx.InternalTracerPayload
	remaining, err := unmarshaledPayload.UnmarshalMsg(data)
	require.NoError(t, err)
	assert.Empty(t, remaining)

	// Verify chunk attributes
	assert.Len(t, unmarshaledPayload.Chunks, 1)
	chunk := unmarshaledPayload.Chunks[0]
	assert.Equal(t, uint32(123), chunk.SamplingMechanism())

	// Verify _dd.p.dm was not included in chunk attributes
	found := false
	for keyRef := range chunk.Attributes {
		key := unmarshaledPayload.Strings.Get(keyRef)
		if key == "_dd.p.dm" {
			found = true
			break
		}
	}
	assert.False(t, found, "_dd.p.dm should not be in chunk attributes")

	// Verify custom attribute was included
	found = false
	for keyRef := range chunk.Attributes {
		key := unmarshaledPayload.Strings.Get(keyRef)
		if key == "custom" {
			found = true
			break
		}
	}
	assert.True(t, found, "custom attribute should be in chunk attributes")
}

func TestV1Payload_WriteAndGrow(t *testing.T) {
	payload := newV1Payload(1.0)

	// Test Write method
	testData := []byte("test data")
	n, err := payload.Write(testData)
	require.NoError(t, err)
	assert.Equal(t, len(testData), n)

	// Test grow method
	payload.grow(1000)

	// Test that we can still read the data
	data, err := io.ReadAll(payload)
	require.NoError(t, err)
	assert.Equal(t, testData, data)
}

func TestV1Payload_Close(t *testing.T) {
	payload := newV1Payload(1.0)

	// Close should not return an error
	err := payload.Close()
	assert.NoError(t, err)
}
