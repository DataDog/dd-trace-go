// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package llmobstest provides test infrastructure for LLM Observability integrations.
//
// The [Collector] captures spans and metrics sent by the LLMObs transport via
// an in-process RoundTripper — no real TCP listener is started. Wire the
// collector into the tracer via [Collector.TracerOption]:
//
//	agent, err := tracertest.StartAgent(t)
//	require.NoError(t, err)
//	coll := llmobstest.New(t)
//	_, err = tracertest.Start(t, agent,
//	    tracer.WithLLMObsEnabled(true),
//	    tracer.WithLLMObsMLApp("my-app"),
//	    coll.TracerOption(),
//	)
//	require.NoError(t, err)
//	// ... exercise code ...
//	tracer.Flush()
//	span := coll.RequireSpan(t, "my-span")
package llmobstest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	_ "unsafe"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	llmobstransport "github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

//go:linkname withLLMObsInProcessTransport github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.withLLMObsInProcessTransport
func withLLMObsInProcessTransport(string, http.RoundTripper) tracer.StartOption

// LLMObsSpan is an alias for the LLMObs span event type.
type LLMObsSpan = llmobstransport.LLMObsSpanEvent

// LLMObsMetric is an alias for the LLMObs evaluation metric type.
type LLMObsMetric = llmobstransport.LLMObsMetric

// Collector captures LLMObs spans and metrics via an in-process RoundTripper.
// Use [New] to create one, then include [Collector.TracerOption] in the tracer
// start options so that the LLMObs transport sends data here.
//
// After calling [tracer.Flush], all buffered data is guaranteed to be
// available for querying — no timeouts or polling are needed.
type Collector struct {
	mux *http.ServeMux

	mu               sync.Mutex
	spans            []LLMObsSpan
	metrics          []LLMObsMetric
	spanBatchSizes   []int         // raw body byte-lengths, one entry per span batch HTTP request
	metricBatchSizes []int         // raw body byte-lengths, one entry per eval-metric batch HTTP request
	spanDelay        time.Duration // artificial delay applied before responding to span batches
}

// New creates a Collector that routes LLMObs requests via an in-process
// RoundTripper — no real TCP listener is started.
func New(tb testing.TB) *Collector {
	tb.Helper()
	c := &Collector{mux: http.NewServeMux()}
	c.mux.HandleFunc("/api/v2/llmobs", c.handleSpans)
	c.mux.HandleFunc("/api/intake/llm-obs/v2/eval-metric", c.handleMetrics)
	return c
}

// HandleFunc registers an HTTP handler on the collector's server for the given
// pattern. Use this to mock dataset, project, or experiment API endpoints in
// tests. With [Collector.TracerOption], all LLMObs transport calls — including
// dataset and experiment API calls — go to this server, so mock handlers must
// be registered here rather than on the APM agent.
//
// Patterns use the direct path (no /evp_proxy/v2 prefix), e.g.:
//
//	coll.HandleFunc("/api/unstable/llm-obs/v1/", myMockHandler)
func (c *Collector) HandleFunc(pattern string, handler http.HandlerFunc) {
	c.mux.HandleFunc(pattern, handler)
}

// TracerOption returns a [tracer.StartOption] that routes the LLMObs transport
// through this collector's in-process RoundTripper. Include it when starting
// the tracer.
func (c *Collector) TracerOption() tracer.StartOption {
	return withLLMObsInProcessTransport(
		"http://llmobstest.invalid",
		&inProcessRoundTripper{handler: c.mux},
	)
}

type inProcessRoundTripper struct{ handler http.Handler }

func (t *inProcessRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, req)
	return w.Result(), nil
}

// FindSpan returns the first collected LLMObs span whose Name equals name, or
// nil if none is found. Call this after [tracer.Flush].
func (c *Collector) FindSpan(name string) *LLMObsSpan {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.spans {
		if c.spans[i].Name == name {
			return &c.spans[i]
		}
	}
	return nil
}

// RequireSpan returns the first collected LLMObs span with the given name.
// It fails the test immediately if no matching span is found.
func (c *Collector) RequireSpan(tb testing.TB, name string) *LLMObsSpan {
	tb.Helper()
	s := c.FindSpan(name)
	if s == nil {
		tb.Fatalf("llmobstest: no LLMObs span found with name %q", name)
	}
	return s
}

// Spans returns a copy of all LLMObs spans collected so far, in arrival order.
// Use it when a span has no distinguishing name (e.g. spans started with an
// empty name) and must be addressed positionally. Call after [tracer.Flush].
func (c *Collector) Spans() []LLMObsSpan {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]LLMObsSpan, len(c.spans))
	copy(out, c.spans)
	return out
}

// SpanCount returns the number of LLMObs spans collected so far.
func (c *Collector) SpanCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.spans)
}

// SpanBatchSizes returns the raw body byte-length of each span batch HTTP
// request received so far, in order. Each entry corresponds to one call to
// the /api/v2/llmobs endpoint. Use this to verify size-based flushing
// behaviour: with correct flushing, no single entry should exceed 5 MB.
func (c *Collector) SpanBatchSizes() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, len(c.spanBatchSizes))
	copy(out, c.spanBatchSizes)
	return out
}

// MetricBatchSizes returns the raw body byte-length of each eval-metric batch
// HTTP request received so far, in order. Each entry corresponds to one call to
// the /api/intake/llm-obs/v2/eval-metric endpoint. Use this to verify
// size-based flushing behaviour: with correct flushing, no single entry should
// exceed 5 MB.
func (c *Collector) MetricBatchSizes() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, len(c.metricBatchSizes))
	copy(out, c.metricBatchSizes)
	return out
}

// FindMetric returns the first collected evaluation metric whose Label equals
// label, or nil if none is found.
func (c *Collector) FindMetric(label string) *LLMObsMetric {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.metrics {
		if c.metrics[i].Label == label {
			return &c.metrics[i]
		}
	}
	return nil
}

// RequireMetric returns the first evaluation metric with the given label.
// It fails the test immediately if no matching metric is found.
func (c *Collector) RequireMetric(tb testing.TB, label string) *LLMObsMetric {
	tb.Helper()
	m := c.FindMetric(label)
	if m == nil {
		tb.Fatalf("llmobstest: no LLMObs metric found with label %q", label)
	}
	return m
}

// MetricCount returns the number of evaluation metrics collected so far.
func (c *Collector) MetricCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.metrics)
}

// SetSpanResponseDelay makes the collector sleep for d before responding to
// each span-batch request, simulating a slow transport. Use it to exercise
// flush timing behaviour (e.g. that FlushSync blocks until the send completes).
func (c *Collector) SetSpanResponseDelay(d time.Duration) {
	c.mu.Lock()
	c.spanDelay = d
	c.mu.Unlock()
}

func (c *Collector) handleSpans(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	delay := c.spanDelay
	c.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var payload []llmobstransport.PushSpanEventsRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.spanBatchSizes = append(c.spanBatchSizes, len(body))
	for _, p := range payload {
		for _, span := range p.Spans {
			if span != nil {
				c.spans = append(c.spans, *span)
			}
		}
	}
	c.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func (c *Collector) handleMetrics(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var payload llmobstransport.PushMetricsRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.metricBatchSizes = append(c.metricBatchSizes, len(body))
	for _, m := range payload.Data.Attributes.Metrics {
		if m != nil {
			c.metrics = append(c.metrics, *m)
		}
	}
	c.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}
