// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func BenchmarkHTTPTransportSend(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"rate_by_service":{}}`))
	}))
	defer server.Close()

	transport := newHTTPTransport(server.URL, defaultHTTPClient(5*time.Second, false))

	payloadSizes := []struct {
		name     string
		numSpans int
		spanSize int
	}{
		{"small_1span", 1, 1},
		{"medium_10spans", 10, 1},
		{"large_100spans", 100, 1},
		{"xlarge_1000spans", 1000, 1},
	}

	for _, size := range payloadSizes {
		b.Run(size.name, func(b *testing.B) {
			payload := newPayload(traceProtocolV04)
			spans := make([]*Span, size.numSpans)
			for i := 0; i < size.numSpans; i++ {
				span := newBasicSpan("transport-test")
				span.meta["data"] = strings.Repeat("x", size.spanSize*1024)
				spans[i] = span
			}
			_, _ = payload.push(spans)

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				payload.reset()
				rc, err := transport.send(payload)
				if err == nil {
					rc.Close()
				}
			}
		})
	}
}

func BenchmarkTransportSendConcurrent(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"rate_by_service":{}}`))
	}))
	defer server.Close()

	transport := newHTTPTransport(server.URL, defaultHTTPClient(5*time.Second, false))
	concurrencyLevels := []int{1, 2, 4, 8}

	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("concurrency_%d", concurrency), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup

				for j := 0; j < concurrency; j++ {
					wg.Add(1)
					go func() {
						defer wg.Done()

						payload := newPayload(traceProtocolV04)
						spans := []*Span{newBasicSpan("concurrent-transport-test")}
						_, _ = payload.push(spans)

						rc, err := transport.send(payload)
						if err == nil {
							rc.Close()
						}
					}()
				}

				wg.Wait()
			}
		})
	}
}
