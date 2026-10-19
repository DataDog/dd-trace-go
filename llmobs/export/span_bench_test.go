// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/llmobs/export"
)

type benchmarkHTTPTransport struct{}

func (benchmarkHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

func BenchmarkLLMObsExportSubmitSpans(b *testing.B) {
	b.Run("batch_50", func(b *testing.B) {
		benchmarkSubmitSpans(b, benchmarkSpanEvents(50, strings.Repeat("x", 1024)))
	})
	b.Run("oversized_split", func(b *testing.B) {
		benchmarkSubmitSpans(b, benchmarkSpanEvents(2, strings.Repeat("x", illmobs.SizeLimitEVPEvent/2)))
	})
}

func benchmarkSubmitSpans(b *testing.B, events []export.SpanEvent) {
	b.Helper()
	client, err := export.NewClient(
		"benchmark-app",
		export.WithAgentURL("http://127.0.0.1:8126"),
		export.WithHTTPClient(&http.Client{Transport: benchmarkHTTPTransport{}}),
		export.WithService("benchmark-service"),
	)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		result, err := client.SubmitSpans(ctx, events)
		if err != nil {
			b.Fatal(err)
		}
		if result.Sent != len(events) {
			b.Fatalf("sent %d spans, want %d", result.Sent, len(events))
		}
	}
}

func benchmarkSpanEvents(count int, output string) []export.SpanEvent {
	events := make([]export.SpanEvent, count)
	start := time.Unix(1_700_000_000, 0)
	for i := range events {
		event := export.NewSpanEvent(
			"100",
			strconv.Itoa(i+1),
			export.KindLLM,
			export.WithTiming(start, 20*time.Millisecond),
			export.WithModel("gpt-4o-mini", "openai"),
			export.WithTextIO("What is the current account status?", output),
			export.WithMetadata(map[string]any{
				"benchmark": "offline-export",
				"sequence":  i,
			}),
		)
		event.Name = "chat.completion"
		event.SessionID = "benchmark-session"
		event.Metrics = map[string]float64{
			"input_tokens":  12,
			"output_tokens": 24,
		}
		event.SpanLinks = []export.SpanLink{{
			TraceID: "200",
			SpanID:  "300",
			Attributes: map[string]string{
				"reason": "benchmark",
			},
		}}
		events[i] = event
	}
	return events
}
