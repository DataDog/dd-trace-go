// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

func newBenchOTLPWriter(b *testing.B) *otlpTraceWriter {
	b.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	b.Cleanup(srv.Close)
	cfg, err := newTestConfig()
	require.NoError(b, err)
	return &otlpTraceWriter{
		config:    cfg,
		transport: newOTLPTransport(srv.Client(), srv.URL, nil),
		resource:  buildResource(cfg.internalConfig),
		scope:     &otlpcommon.InstrumentationScope{Name: "dd-trace-go", Version: version.Tag},
		spans:     make([]*otlptrace.Span, 0),
		climit:    make(chan struct{}, concurrentConnectionLimit),
	}
}

func BenchmarkOTLPTraceWriterAdd(b *testing.B) {
	traceSizes := []struct {
		name     string
		numSpans int
	}{
		{"1span", 1},
		{"5spans", 5},
		{"10spans", 10},
		{"50spans", 50},
	}

	for _, size := range traceSizes {
		b.Run(size.name, func(b *testing.B) {
			writer := newBenchOTLPWriter(b)

			trace := make([]*Span, size.numSpans)
			for i := range size.numSpans {
				trace[i] = newBasicSpan("benchmark-span")
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				writer.add(trace)
			}
		})
	}
}

func BenchmarkOTLPTraceWriterFlush(b *testing.B) {
	writer := newBenchOTLPWriter(b)
	trace := []*Span{newBasicSpan("flush-test")}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		writer.add(trace)
		writer.flush()
		writer.wg.Wait()
	}
}

func BenchmarkOTLPProtoMarshal(b *testing.B) {
	spanCounts := []struct {
		name string
		n    int
	}{
		{"1span", 1},
		{"10spans", 10},
		{"100spans", 100},
		{"1000spans", 1000},
	}

	for _, sc := range spanCounts {
		b.Run(sc.name, func(b *testing.B) {
			spans := make([]*otlptrace.Span, sc.n)
			for i := range sc.n {
				s := newBasicSpan("bench-span")
				s.meta.Set("key", "value")
				spans[i] = convertSpan(s, "bench-svc")
			}
			tracesData := buildTracesData(spans)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				proto.Marshal(tracesData)
			}
		})
	}

	b.Run("rich_spans", func(b *testing.B) {
		spans := make([]*otlptrace.Span, 100)
		for i := range 100 {
			s := newSpan("op", "svc", "res", uint64(i+1), 1, 0)
			for j := range 20 {
				s.meta.Set(fmt.Sprintf("key-%d", j), fmt.Sprintf("value-%d", j))
			}
			for j := range 5 {
				s.metrics[fmt.Sprintf("metric-%d", j)] = float64(j) * 1.5
			}
			s.spanLinks = append(s.spanLinks, SpanLink{
				TraceID:     uint64(i + 100),
				TraceIDHigh: uint64(i + 200),
				SpanID:      uint64(i + 300),
				Attributes:  map[string]string{"link-key": "link-val"},
			})
			spans[i] = convertSpan(s, "bench-svc")
		}
		tracesData := buildTracesData(spans)

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			proto.Marshal(tracesData)
		}
	})
}

func BenchmarkOTLPProtoSize(b *testing.B) {
	spanCounts := []struct {
		name string
		n    int
	}{
		{"1span", 1},
		{"10spans", 10},
		{"100spans", 100},
		{"1000spans", 1000},
	}

	for _, sc := range spanCounts {
		b.Run(sc.name, func(b *testing.B) {
			spans := make([]*otlptrace.Span, sc.n)
			for i := range sc.n {
				s := newBasicSpan("bench-span")
				spans[i] = convertSpan(s, "bench-svc")
			}
			tracesData := buildTracesData(spans)

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				proto.Size(tracesData)
			}
		})
	}
}

func buildTracesData(spans []*otlptrace.Span) *otlptrace.TracesData {
	return &otlptrace.TracesData{
		ResourceSpans: []*otlptrace.ResourceSpans{{
			Resource: buildResource(nil),
			ScopeSpans: []*otlptrace.ScopeSpans{{
				Scope: &otlpcommon.InstrumentationScope{Name: "dd-trace-go"},
				Spans: spans,
			}},
		}},
	}
}

func BenchmarkOTLPTraceWriterConcurrent(b *testing.B) {
	concurrencyLevels := []int{1, 2, 4, 8}

	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("concurrency_%d", concurrency), func(b *testing.B) {
			writer := newBenchOTLPWriter(b)
			trace := []*Span{newBasicSpan("concurrent-test")}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				var wg sync.WaitGroup

				for range concurrency {
					wg.Go(func() {
						writer.add(trace)
					})
				}

				wg.Wait()
			}
		})
	}
}
