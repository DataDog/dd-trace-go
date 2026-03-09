// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"

	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
)

type payloadOTLP struct {
	resource *otlpresource.Resource
	scope    *otlpcommon.InstrumentationScope

	spans []otlptrace.Span
	count int

	buf []byte

	reader *bytes.Reader
}

func newPayloadOTLP() *payloadOTLP {
	return &payloadOTLP{
		resource: &otlpresource.Resource{},
		scope:    &otlpcommon.InstrumentationScope{},
		spans:    make([]otlptrace.Span, 0),
		reader:   bytes.NewReader([]byte{}),
	}
}

func (p *payloadOTLP) Read(b []byte) (int, error) {
	return p.reader.Read(b)
}

func (p *payloadOTLP) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	return len(b), nil
}

func (p *payloadOTLP) Close() error {
	return nil
}

func (p *payloadOTLP) push(t spanList) (stats payloadStats, err error) {
	for _, s := range t {
		p.spans = append(p.spans, convertSpan(s))
		p.count++
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
	p.count = 0
	p.reader.Seek(0, 0)
}

func (p *payloadOTLP) recordItem() {
	p.count++
}

func (p *payloadOTLP) stats() payloadStats {
	return payloadStats{
		size:      p.size(),
		itemCount: p.count,
	}
}

func (p *payloadOTLP) size() int {
	return 1
}

func (p *payloadOTLP) itemCount() int {
	return p.count
}

func (p *payloadOTLP) protocol() float64 {
	return traceProtocolOTLP
}
