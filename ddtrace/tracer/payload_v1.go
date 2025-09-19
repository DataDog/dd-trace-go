// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer/idx"
)

// v1Payload implements the payload interface for the v1 efficient trace payload format.
// It uses string interning and the InternalTracerPayload structure for efficient serialization.
type v1Payload struct {
	// mu protects concurrent access to the payload
	mu sync.RWMutex

	// internalPayload holds the v1 format data
	internalPayload *idx.InternalTracerPayload

	// buf holds the serialized data
	buf bytes.Buffer

	// reader is used for reading the contents of buf
	reader *bytes.Reader

	// count specifies the number of items in the stream
	count uint32

	// protocolVersion specifies the trace protocol version to use
	protocolVersion float64
}

// newV1Payload returns a ready to use v1 payload.
func newV1Payload(protocol float64) *v1Payload {
	return &v1Payload{
		internalPayload: &idx.InternalTracerPayload{
			Strings:    idx.NewStringTable(),
			Attributes: make(map[uint32]*idx.AnyValue),
			Chunks:     make([]*idx.InternalTraceChunk, 0),
		},
		protocolVersion: protocol,
	}
}

// push pushes a new item (spanList) into the v1 payload.
func (p *v1Payload) push(t spanList) (stats payloadStats, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Convert spanList to InternalTracerPayload format
	chunk, err := p.convertSpanListToChunk(t)
	if err != nil {
		return payloadStats{}, err
	}

	// Add the chunk to the payload
	p.internalPayload.Chunks = append(p.internalPayload.Chunks, chunk)

	// Update stats
	atomic.AddUint32(&p.count, 1)

	// Serialize the updated payload
	if err := p.serializePayload(); err != nil {
		return payloadStats{}, err
	}

	return p.statsUnsafe(), nil
}

// convertSpanListToChunk converts a spanList to an InternalTraceChunk.
func (p *v1Payload) convertSpanListToChunk(spans spanList) (*idx.InternalTraceChunk, error) {
	if len(spans) == 0 {
		return &idx.InternalTraceChunk{
			Strings:      p.internalPayload.Strings,
			Priority:     0,
			Attributes:   make(map[uint32]*idx.AnyValue),
			Spans:        make([]*idx.InternalSpan, 0),
			DroppedTrace: false,
			TraceID:      make([]byte, 16),
		}, nil
	}

	// Convert spans to InternalSpan format
	internalSpans := make([]*idx.InternalSpan, len(spans))
	for i, span := range spans {
		internalSpan, err := p.convertSpanToInternal(span)
		if err != nil {
			return nil, err
		}
		internalSpans[i] = internalSpan
	}

	// Extract trace ID from the first span
	var traceID []byte
	if len(spans) > 0 {
		traceID = p.extractTraceID(spans[0])
	}

	// Create the chunk
	chunk := &idx.InternalTraceChunk{
		Strings:      p.internalPayload.Strings,
		Priority:     0, // Priority is not available in the Span struct
		Attributes:   make(map[uint32]*idx.AnyValue),
		Spans:        internalSpans,
		DroppedTrace: false,
		TraceID:      traceID,
	}
	chunk.SetSamplingMechanism(0)

	// Extract chunk-level attributes from the first span
	p.extractChunkAttributes(spans[0], chunk)

	return chunk, nil
}

// convertSpanToInternal converts a Span to an InternalSpan.
func (p *v1Payload) convertSpanToInternal(span *Span) (*idx.InternalSpan, error) {
	// Create the internal span structure
	internalSpan := &idx.Span{
		ServiceRef:   p.internalPayload.Strings.Add(span.service),
		NameRef:      p.internalPayload.Strings.Add(span.name),
		ResourceRef:  p.internalPayload.Strings.Add(span.resource),
		SpanID:       span.spanID,
		ParentID:     span.parentID,
		Start:        uint64(span.start),
		Duration:     uint64(span.duration),
		Error:        span.error > 0,
		Attributes:   make(map[uint32]*idx.AnyValue),
		TypeRef:      p.internalPayload.Strings.Add(span.spanType),
		EnvRef:       p.internalPayload.Strings.Add(span.meta["env"]),
		VersionRef:   p.internalPayload.Strings.Add(span.meta["version"]),
		ComponentRef: p.internalPayload.Strings.Add(span.meta["component"]),
		Kind:         p.convertSpanKind(span.meta["kind"]),
		Links:        p.convertSpanLinks(span.spanLinks),
		Events:       p.convertSpanEvents(span.spanEvents),
	}

	// Convert span attributes
	for k, v := range span.meta {
		if k == "env" || k == "version" || k == "component" || k == "kind" {
			continue // Already handled above
		}
		internalSpan.Attributes[p.internalPayload.Strings.Add(k)] = &idx.AnyValue{
			Value: &idx.AnyValue_StringValueRef{
				StringValueRef: p.internalPayload.Strings.Add(v),
			},
		}
	}

	// Convert metrics
	for k, v := range span.metrics {
		internalSpan.Attributes[p.internalPayload.Strings.Add(k)] = &idx.AnyValue{
			Value: &idx.AnyValue_DoubleValue{
				DoubleValue: v,
			},
		}
	}

	// Convert meta struct
	for k, v := range span.metaStruct {
		// Convert the value to bytes
		var bytesValue []byte
		if b, ok := v.([]byte); ok {
			bytesValue = b
		} else {
			// Convert to string and then to bytes as fallback
			bytesValue = []byte(fmt.Sprintf("%v", v))
		}
		internalSpan.Attributes[p.internalPayload.Strings.Add(k)] = &idx.AnyValue{
			Value: &idx.AnyValue_BytesValue{
				BytesValue: bytesValue,
			},
		}
	}

	return idx.NewInternalSpan(p.internalPayload.Strings, internalSpan), nil
}

// convertSpanKind converts a string span kind to the idx.SpanKind enum.
func (p *v1Payload) convertSpanKind(kindStr string) idx.SpanKind {
	switch kindStr {
	case "server":
		return idx.SpanKind_SPAN_KIND_SERVER
	case "client":
		return idx.SpanKind_SPAN_KIND_CLIENT
	case "producer":
		return idx.SpanKind_SPAN_KIND_PRODUCER
	case "consumer":
		return idx.SpanKind_SPAN_KIND_CONSUMER
	case "internal":
		return idx.SpanKind_SPAN_KIND_INTERNAL
	default:
		return idx.SpanKind_SPAN_KIND_INTERNAL
	}
}

// convertSpanLinks converts span links to the internal format.
func (p *v1Payload) convertSpanLinks(links []SpanLink) []*idx.SpanLink {
	if len(links) == 0 {
		return nil
	}

	internalLinks := make([]*idx.SpanLink, len(links))
	for i, link := range links {
		linkTraceID := make([]byte, 16)
		binary.BigEndian.PutUint64(linkTraceID[8:], link.TraceID)
		binary.BigEndian.PutUint64(linkTraceID[:8], link.TraceIDHigh)

		internalLinks[i] = &idx.SpanLink{
			TraceID:       linkTraceID,
			SpanID:        link.SpanID,
			TracestateRef: p.internalPayload.Strings.Add(link.Tracestate),
			Flags:         link.Flags,
			Attributes:    p.convertAttributesMap(link.Attributes),
		}
	}

	return internalLinks
}

// convertSpanEvents converts span events to the internal format.
func (p *v1Payload) convertSpanEvents(events []spanEvent) []*idx.SpanEvent {
	if len(events) == 0 {
		return nil
	}

	internalEvents := make([]*idx.SpanEvent, len(events))
	for i, event := range events {
		internalEvents[i] = &idx.SpanEvent{
			Time:       uint64(event.TimeUnixNano),
			NameRef:    p.internalPayload.Strings.Add(event.Name),
			Attributes: p.convertSpanEventAttributes(event.RawAttributes),
		}
	}

	return internalEvents
}

// convertSpanEventAttributes converts span event attributes to the internal format.
func (p *v1Payload) convertSpanEventAttributes(attrs map[string]interface{}) map[uint32]*idx.AnyValue {
	if len(attrs) == 0 {
		return nil
	}

	internalAttrs := make(map[uint32]*idx.AnyValue, len(attrs))
	for k, v := range attrs {
		keyRef := p.internalPayload.Strings.Add(k)
		internalAttrs[keyRef] = p.convertAnyValue(v)
	}

	return internalAttrs
}

// convertAnyValue converts an interface{} to an idx.AnyValue.
func (p *v1Payload) convertAnyValue(v interface{}) *idx.AnyValue {
	switch val := v.(type) {
	case string:
		return &idx.AnyValue{
			Value: &idx.AnyValue_StringValueRef{
				StringValueRef: p.internalPayload.Strings.Add(val),
			},
		}
	case bool:
		return &idx.AnyValue{
			Value: &idx.AnyValue_BoolValue{
				BoolValue: val,
			},
		}
	case int64:
		return &idx.AnyValue{
			Value: &idx.AnyValue_IntValue{
				IntValue: val,
			},
		}
	case float64:
		return &idx.AnyValue{
			Value: &idx.AnyValue_DoubleValue{
				DoubleValue: val,
			},
		}
	case []interface{}:
		values := make([]*idx.AnyValue, len(val))
		for i, item := range val {
			values[i] = p.convertAnyValue(item)
		}
		return &idx.AnyValue{
			Value: &idx.AnyValue_ArrayValue{
				ArrayValue: &idx.ArrayValue{
					Values: values,
				},
			},
		}
	default:
		// Convert to string as fallback
		return &idx.AnyValue{
			Value: &idx.AnyValue_StringValueRef{
				StringValueRef: p.internalPayload.Strings.Add(strings.TrimSpace(fmt.Sprintf("%v", val))),
			},
		}
	}
}

// convertAttributesMap converts a map[string]string to the internal format.
func (p *v1Payload) convertAttributesMap(attrs map[string]string) map[uint32]*idx.AnyValue {
	if len(attrs) == 0 {
		return nil
	}

	internalAttrs := make(map[uint32]*idx.AnyValue, len(attrs))
	for k, v := range attrs {
		internalAttrs[p.internalPayload.Strings.Add(k)] = &idx.AnyValue{
			Value: &idx.AnyValue_StringValueRef{
				StringValueRef: p.internalPayload.Strings.Add(v),
			},
		}
	}

	return internalAttrs
}

// extractTraceID extracts the 128-bit trace ID from a span.
func (p *v1Payload) extractTraceID(span *Span) []byte {
	traceID := make([]byte, 16)
	binary.BigEndian.PutUint64(traceID[8:], span.traceID)
	// TraceIDHigh is not available in the Span struct, so we use 0
	binary.BigEndian.PutUint64(traceID[:8], 0)
	return traceID
}

// extractChunkAttributes extracts chunk-level attributes from a span.
func (p *v1Payload) extractChunkAttributes(span *Span, chunk *idx.InternalTraceChunk) {
	// Extract sampling mechanism from _dd.p.dm tag
	if dmStr, exists := span.meta["_dd.p.dm"]; exists {
		valueStr := dmStr
		if strings.HasPrefix(dmStr, "-") {
			valueStr = strings.TrimPrefix(dmStr, "-")
		}
		if val, err := strconv.ParseUint(valueStr, 10, 32); err == nil {
			chunk.SetSamplingMechanism(uint32(val))
		}
	}

	// Add other chunk-level attributes (excluding _dd.p.dm)
	for k, v := range span.meta {
		if k == "_dd.p.dm" {
			continue
		}
		chunk.Attributes[p.internalPayload.Strings.Add(k)] = &idx.AnyValue{
			Value: &idx.AnyValue_StringValueRef{
				StringValueRef: p.internalPayload.Strings.Add(v),
			},
		}
	}
}

// serializePayload serializes the internal payload to the buffer.
func (p *v1Payload) serializePayload() error {
	p.buf.Reset()

	// Marshal the internal payload
	data, err := p.internalPayload.MarshalMsg(nil)
	if err != nil {
		return err
	}

	// Write the data to buffer
	_, err = p.buf.Write(data)
	return err
}

// itemCount returns the number of items available in the stream.
func (p *v1Payload) itemCount() int {
	return int(atomic.LoadUint32(&p.count))
}

// size returns the payload size in bytes.
func (p *v1Payload) size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.buf.Len()
}

// reset sets up the payload to be read a second time.
func (p *v1Payload) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

// clear empties the payload buffers.
func (p *v1Payload) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.buf = bytes.Buffer{}
	p.reader = nil
	p.internalPayload = &idx.InternalTracerPayload{
		Strings:    idx.NewStringTable(),
		Attributes: make(map[uint32]*idx.AnyValue),
		Chunks:     make([]*idx.InternalTraceChunk, 0),
	}
	atomic.StoreUint32(&p.count, 0)
}

// Read implements io.Reader. It reads from the serialized payload.
func (p *v1Payload) Read(b []byte) (n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.reader == nil {
		p.reader = bytes.NewReader(p.buf.Bytes())
	}
	return p.reader.Read(b)
}

// Write implements io.Writer. It writes data directly to the buffer.
func (p *v1Payload) Write(data []byte) (n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.Write(data)
}

// Close implements io.Closer.
func (p *v1Payload) Close() error {
	return nil
}

// grow grows the buffer to ensure it can accommodate n more bytes.
func (p *v1Payload) grow(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf.Grow(n)
}

// recordItem records that an item was added and updates the header.
func (p *v1Payload) recordItem() {
	atomic.AddUint32(&p.count, 1)
}

// stats returns the current stats of the payload.
func (p *v1Payload) stats() payloadStats {
	return payloadStats{
		size:      p.size(),
		itemCount: int(atomic.LoadUint32(&p.count)),
	}
}

// statsUnsafe returns the current stats of the payload without acquiring locks.
// This should only be called when the caller already holds the appropriate lock.
func (p *v1Payload) statsUnsafe() payloadStats {
	return payloadStats{
		size:      p.buf.Len(),
		itemCount: int(atomic.LoadUint32(&p.count)),
	}
}

// protocol returns the protocol version of the payload.
func (p *v1Payload) protocol() float64 {
	return p.protocolVersion
}

// Ensure v1Payload implements the payload interface
var _ payload = (*v1Payload)(nil)
