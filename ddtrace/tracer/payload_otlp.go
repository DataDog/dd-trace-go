// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"errors"
	"sync/atomic"

	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// TODO: Handle concurrent reads and writes for this struct. Update methods accordingly.
type payloadOTLP struct {
	resource *otlpresource.Resource
	scope    *otlpcommon.InstrumentationScope

	spans []*otlptrace.Span
	count uint32 // +checkatomic

	buf []byte

	reader *bytes.Reader
}

func newPayloadOTLP(c *config) *payloadOTLP {
	return &payloadOTLP{
		resource: buildResource(c),
		scope:    &otlpcommon.InstrumentationScope{Name: "dd-trace-go"},
		spans:    make([]*otlptrace.Span, 0),
		reader:   bytes.NewReader([]byte{}),
	}
}

func (p *payloadOTLP) Read(b []byte) (int, error) {
	// Ensure we encode only once
	if p.buf == nil {
		if err := p.encode(); err != nil {
			return 0, err
		}
		p.reader = bytes.NewReader(p.buf)
	}
	return p.reader.Read(b)
}

func (p *payloadOTLP) Write(b []byte) (int, error) {
	return 0, errors.New("payloadOTLP does not support direct writes")
}

func (p *payloadOTLP) Close() error {
	return nil
}

func (p *payloadOTLP) push(t spanList) (stats payloadStats, err error) {
	for _, s := range t {
		p.spans = append(p.spans, convertSpan(s))
		p.recordItem()
	}
	return p.stats(), nil
}

// no-op
func (p *payloadOTLP) grow(n int) {}

func (p *payloadOTLP) reset() {
	if p.reader != nil {
		p.reader.Seek(0, 0)
	}
}

func (p *payloadOTLP) clear() {
	p.spans = p.spans[:0]
	atomic.StoreUint32(&p.count, 0)
	p.buf = nil
	p.reader = bytes.NewReader([]byte{})
}

func (p *payloadOTLP) recordItem() {
	atomic.AddUint32(&p.count, 1)
}

func (p *payloadOTLP) stats() payloadStats {
	return payloadStats{
		size:      p.size(),
		itemCount: p.itemCount(),
	}
}

// size returns -1 because the protobuf encoding is deferred until Read() is called.
func (p *payloadOTLP) size() int {
	return -1
}

func (p *payloadOTLP) itemCount() int {
	return int(atomic.LoadUint32(&p.count))
}

func (p *payloadOTLP) protocol() float64 {
	return traceProtocolOTLP
}

func (p *payloadOTLP) encode() error {
	tracesData := &otlptrace.TracesData{
		ResourceSpans: []*otlptrace.ResourceSpans{
			{
				Resource: p.resource,
				ScopeSpans: []*otlptrace.ScopeSpans{
					{
						Scope: p.scope,
						Spans: p.spans,
					},
				},
			},
		},
	}
	b, err := proto.Marshal(tracesData)
	if err != nil {
		return err
	}
	p.buf = b
	return nil
}
