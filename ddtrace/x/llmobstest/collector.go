// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package llmobstest provides test infrastructure for LLM Observability integrations.
//
// The [Collector] captures spans and metrics sent by the LLMObs transport. It
// starts its own HTTP server so that LLMObs traffic is kept separate from the
// APM agent. Wire the collector into the tracer via [Collector.TracerOption]:
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
	_ "unsafe"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	llmobstransport "github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

//go:linkname withLLMObsTestBaseURL github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.withLLMObsTestBaseURL
func withLLMObsTestBaseURL(string) tracer.StartOption

// LLMObsSpan is an alias for the LLMObs span event type.
type LLMObsSpan = llmobstransport.LLMObsSpanEvent

// LLMObsMetric is an alias for the LLMObs evaluation metric type.
type LLMObsMetric = llmobstransport.LLMObsMetric

// Collector captures LLMObs spans and metrics on its own HTTP server.
// Use [New] to create one, then include [Collector.TracerOption] in the tracer
// start options so that the LLMObs transport sends data here.
//
// After calling [tracer.Flush], all buffered data is guaranteed to be
// available for querying — no timeouts or polling are needed.
type Collector struct {
	server *httptest.Server
	mux    *http.ServeMux

	mu      sync.Mutex
	spans   []LLMObsSpan
	metrics []LLMObsMetric
}

// New creates a Collector that listens on its own HTTP server. The server is
// closed automatically when the test ends.
func New(tb testing.TB) *Collector {
	tb.Helper()
	c := &Collector{mux: http.NewServeMux()}
	c.mux.HandleFunc("/api/v2/llmobs", c.handleSpans)
	c.mux.HandleFunc("/api/intake/llm-obs/v2/eval-metric", c.handleMetrics)
	c.server = httptest.NewServer(c.mux)
	tb.Cleanup(c.server.Close)
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

// TracerOption returns a [tracer.StartOption] that points the LLMObs transport
// at this collector's HTTP server. Include it when starting the tracer.
func (c *Collector) TracerOption() tracer.StartOption {
	return withLLMObsTestBaseURL(c.server.URL)
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

// SpanCount returns the number of LLMObs spans collected so far.
func (c *Collector) SpanCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.spans)
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

func (c *Collector) handleSpans(w http.ResponseWriter, r *http.Request) {
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
	for _, m := range payload.Data.Attributes.Metrics {
		if m != nil {
			c.metrics = append(c.metrics, *m)
		}
	}
	c.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}
