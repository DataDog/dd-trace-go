// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export_test

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/llmobs/export"
)

// discardTransport accepts every request and retains nothing, so benchmarks
// measure the client's encode/assemble cost without network or bookkeeping noise.
type discardTransport struct{}

func (discardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
}

func benchSpans(n int) []export.SpanEvent {
	spans := make([]export.SpanEvent, n)
	for i := range spans {
		spans[i] = export.SpanEvent{
			TraceID:       "12345678901234567890",
			SpanID:        strconv.Itoa(i),
			Kind:          export.KindLLM,
			Name:          "chat",
			ModelName:     "gpt-4o",
			ModelProvider: "openai",
			Input:         strings.Repeat("prompt tokens ", 40),
			Output:        strings.Repeat("completion tokens ", 40),
			Metrics:       &export.SpanMetrics{InputTokens: ptr(int64(120)), OutputTokens: ptr(int64(80)), TotalTokens: ptr(int64(200))},
			Tags:          []string{"team:ml"},
		}
	}
	return spans
}

func benchEvals(n int) []export.EvaluationMetric {
	evals := make([]export.EvaluationMetric, n)
	for i := range evals {
		evals[i] = export.EvaluationMetric{
			SpanID:     strconv.Itoa(i),
			TraceID:    "12345678901234567890",
			Label:      "quality",
			ScoreValue: ptr(0.9),
			Tags:       []string{"team:ml"},
		}
	}
	return evals
}

func benchClient(b *testing.B) *export.Client {
	b.Helper()
	c, err := export.NewClient("app",
		export.WithDatadogIntake("datadoghq.com", "k"),
		export.WithHTTPClient(&http.Client{Transport: discardTransport{}}),
	)
	if err != nil {
		b.Fatal(err)
	}
	return c
}

func BenchmarkSubmitSpans(b *testing.B) {
	c := benchClient(b)
	spans := benchSpans(50) // one full default-size batch
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.SubmitSpans(ctx, spans); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSubmitEvaluations(b *testing.B) {
	c := benchClient(b)
	evals := benchEvals(1000) // one full default-size batch
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.SubmitEvaluations(ctx, evals); err != nil {
			b.Fatal(err)
		}
	}
}
