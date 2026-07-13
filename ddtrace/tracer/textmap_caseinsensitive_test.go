// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractHeaderNameCaseInsensitivity pins the case-insensitive header-name
// dispatch: extractors match header names with strings.EqualFold, so an
// incoming carrier may present header names in any case (canonical MIME case
// from net/http, all-caps, mixed) and still be recognised. It also pins that
// the OpenTracing "ot-baggage-<key>" suffix is lowercased regardless of the
// incoming case, and that a user-configured mixed-case header name matches.
func TestExtractHeaderNameCaseInsensitivity(t *testing.T) {
	t.Run("datadog default headers, non-canonical case", func(t *testing.T) {
		t.Setenv(headerPropagationStyleExtract, "datadog")
		p := NewPropagator(nil)
		// TextMapCarrier does not canonicalize keys, so the exact case below is
		// what reaches the extractor.
		carrier := TextMapCarrier{
			"X-Datadog-Trace-Id":          "1234",
			"X-DATADOG-PARENT-ID":         "5678",
			"x-DaTaDoG-SaMpLiNg-PrIoRiTy": "1",
			"OT-BAGGAGE-UserID":           "abc",
		}
		ctx, err := p.Extract(carrier)
		require.NoError(t, err)
		require.NotNil(t, ctx)
		assert.Equal(t, uint64(1234), ctx.TraceIDLower())
		assert.Equal(t, uint64(5678), ctx.SpanID())
		// OT-baggage suffix must be lowercased regardless of incoming case.
		assert.Equal(t, "abc", ctx.baggageItem("userid"))
		assert.Equal(t, "", ctx.baggageItem("UserID"))
	})

	t.Run("b3 headers, non-canonical case", func(t *testing.T) {
		t.Setenv(headerPropagationStyleExtract, "b3multi")
		p := NewPropagator(nil)
		carrier := TextMapCarrier{
			"X-B3-TraceId": "0000000000000000000000000000007b",
			"X-B3-SpanId":  "00000000000001c8",
			"X-B3-Sampled": "1",
		}
		ctx, err := p.Extract(carrier)
		require.NoError(t, err)
		require.NotNil(t, ctx)
		assert.Equal(t, uint64(0x7b), ctx.TraceIDLower())
		assert.Equal(t, uint64(0x1c8), ctx.SpanID())
	})

	t.Run("w3c traceparent, non-canonical case", func(t *testing.T) {
		t.Setenv(headerPropagationStyleExtract, "tracecontext")
		p := NewPropagator(nil)
		carrier := TextMapCarrier{
			"TraceParent": "00-00000000000000000000000000000064-00000000000000c8-01",
		}
		ctx, err := p.Extract(carrier)
		require.NoError(t, err)
		require.NotNil(t, ctx)
		assert.Equal(t, uint64(0x64), ctx.TraceIDLower())
		assert.Equal(t, uint64(0xc8), ctx.SpanID())
	})

	t.Run("custom mixed-case configured header", func(t *testing.T) {
		t.Setenv(headerPropagationStyleExtract, "datadog")
		p := NewPropagator(&PropagatorConfig{
			TraceHeader:  "My-Trace-Id",
			ParentHeader: "My-Parent-Id",
		})
		carrier := TextMapCarrier{
			"my-trace-id":  "42",
			"MY-PARENT-ID": "7",
		}
		ctx, err := p.Extract(carrier)
		require.NoError(t, err)
		require.NotNil(t, ctx)
		assert.Equal(t, uint64(42), ctx.TraceIDLower())
		assert.Equal(t, uint64(7), ctx.SpanID())
	})
}

// canonicalHTTPHeaders builds an http.Header whose keys are stored in canonical
// MIME case (as net/http delivers server-side), exercising the case-insensitive
// dispatch path that the pre-lowercased TextMapCarrier benchmarks do not.
func canonicalDatadogHeaders() HTTPHeadersCarrier {
	h := http.Header{}
	h.Set(DefaultTraceIDHeader, "1123123132131312313123123")
	h.Set(DefaultParentIDHeader, "1212321131231312312312312")
	h.Set(DefaultPriorityHeader, "-1")
	h.Set(DefaultBaggageHeaderPrefix+"userId", "abc123")
	h.Set("User-Agent", "Go-http-client/1.1")
	h.Set("Accept-Encoding", "gzip")
	h.Set("X-Forwarded-For", "10.0.0.1")
	return HTTPHeadersCarrier(h)
}

func canonicalW3CHeaders() HTTPHeadersCarrier {
	h := http.Header{}
	h.Set(traceparentHeader, "00-00000000000000001111111111111111-2222222222222222-01")
	h.Set(tracestateHeader, "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE")
	h.Set("User-Agent", "Go-http-client/1.1")
	h.Set("Accept-Encoding", "gzip")
	return HTTPHeadersCarrier(h)
}

// BenchmarkExtractDatadogHTTPHeaders extracts from a canonical-case http.Header
// carrier (unlike BenchmarkExtractDatadog, which uses a pre-lowercased
// TextMapCarrier and so never exercises header-name case folding).
func BenchmarkExtractDatadogHTTPHeaders(b *testing.B) {
	b.Setenv(headerPropagationStyleExtract, "datadog")
	propagator := NewPropagator(nil)
	carrier := canonicalDatadogHeaders()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		propagator.Extract(carrier)
	}
}

// BenchmarkExtractW3CHTTPHeaders is the W3C counterpart to
// BenchmarkExtractDatadogHTTPHeaders.
func BenchmarkExtractW3CHTTPHeaders(b *testing.B) {
	b.Setenv(headerPropagationStyleExtract, "tracecontext")
	propagator := NewPropagator(nil)
	carrier := canonicalW3CHeaders()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		propagator.Extract(carrier)
	}
}
