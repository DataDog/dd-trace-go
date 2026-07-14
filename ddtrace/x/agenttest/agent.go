// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package agenttest provides a mock APM agent for inspecting traces in tests.
//
// The agent collects spans sent via its in-process HTTP transport and exposes
// them for assertions. No real networking is involved, allowing for flushes to
// be deterministic and immediate.
//
//	agent := agenttest.New()
//	agent.HandleTraces("/v0.4/traces", myHandler)
//	agent.Start(t)
//	// ... start tracer with agent.Addr() / agent.Transport() ...
//	// ... create spans, flush ...
//	span := agent.RequireSpan(t, agenttest.With().Operation("http.request"))
//
// By design, this API does not expose span slices or iterators. Order-dependent
// assertions are a common source of test flakiness; any future iterator must
// randomize its traversal order.
package agenttest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TraceHandler decodes trace data from an io.Reader and returns the decoded spans.
// Implementations handle a specific wire format (e.g. msgpack v0.4 or binary v1.0).
type TraceHandler func(io.Reader) []*Span

// Info holds agent configuration returned to the tracer on flush responses,
// such as per-service sampling rates.
type Info struct {
	Rates map[string]float64 `json:"rate_by_service"`
}

func newInfo() *Info {
	return &Info{
		Rates: make(map[string]float64),
	}
}

// RateByService sets the sampling rate for a given service/env pair.
// The tracer uses these rates when making sampling decisions.
func (i *Info) RateByService(service, env string, rate float64) {
	k := fmt.Sprintf("service:%s,env:%s", service, env)
	i.Rates[k] = rate
}

// Agent is a mock APM agent that collects spans in-process for test assertions.
type Agent interface {
	// Info returns the agent info configuration (e.g. sampling rates).
	Info() *Info
	// HandleTraces registers a handler that decodes traces arriving at the given
	// HTTP pattern (e.g. "/v0.4/traces") and stores the resulting spans.
	HandleTraces(string, TraceHandler)

	// Start initializes the agent. Call this before starting the tracer.
	Start(testing.TB) error
	// Addr returns the agent address to pass to the tracer via WithAgentAddr.
	Addr() string
	// Transport returns an optimized transport for interacting with the agent.
	Transport() http.RoundTripper

	// FindSpan returns the first collected span matching all provided conditions,
	// or nil if none is found.
	FindSpan(...*SpanMatch) *Span
	// RequireSpan returns the first collected span matching all provided conditions.
	// It fails the test immediately if no matching span is found.
	RequireSpan(testing.TB, ...*SpanMatch) *Span
	// CountSpans returns the total number of spans collected so far.
	CountSpans() int
}

type agent struct {
	mu sync.Mutex

	mux       *http.ServeMux
	addr      string
	endpoints []string

	info  *Info
	spans []*Span
}

// New creates a new mock agent. Register trace handlers with HandleTraces
// before calling Start.
func New() Agent {
	mux := http.NewServeMux()
	a := &agent{
		mux:  mux,
		info: newInfo(),
	}
	mux.HandleFunc("/info", a.handleInfo)
	return a
}

// Info returns the agent info configuration (e.g. sampling rates).
func (a *agent) Info() *Info {
	return a.info
}

// HandleTraces registers a handler that decodes traces arriving at the given
// HTTP pattern (e.g. "/v0.4/traces") and stores the resulting spans.
func (a *agent) HandleTraces(pattern string, handler TraceHandler) {
	a.endpoints = append(a.endpoints, pattern)
	a.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		spans := handler(r.Body)
		a.mu.Lock()
		a.spans = append(a.spans, spans...)
		a.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a.info)
	})
}

func (a *agent) handleInfo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"endpoints": a.endpoints, "client_drop_p0s": true})
}

// Addr returns the agent address to pass to the tracer via WithAgentAddr.
// We return an invalid address because the transport avoids TCP networking.
func (a *agent) Addr() string {
	return a.addr
}

// Transport returns an http.RoundTripper that dispatches requests directly
// to this agent's handler in-process, bypassing OS networking. Using this
// transport eliminates TCP overhead and makes test flushes deterministic.
func (a *agent) Transport() http.RoundTripper {
	return &inProcessRoundTripper{handler: a.mux}
}

type inProcessRoundTripper struct{ handler http.Handler }

func (t *inProcessRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, req)
	return w.Result(), nil
}

func (a *agent) Start(_ testing.TB) error {
	a.addr = "agenttest.invalid:0"
	return nil
}

// FindSpan returns the first collected span matching all provided conditions,
// or nil if none is found.
func (a *agent) FindSpan(matchers ...*SpanMatch) *Span {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.spans {
		match := true
		for _, m := range matchers {
			if !m.Matches(s) {
				match = false
				break
			}
		}
		if match {
			return s
		}
	}
	return nil
}

// RequireSpan returns the first collected span matching all provided conditions.
// It fails the test immediately if no matching span is found.
func (a *agent) RequireSpan(t testing.TB, matchers ...*SpanMatch) *Span {
	t.Helper()
	s := a.FindSpan(matchers...)
	if s == nil {
		a.mu.Lock()
		spans := make([]*Span, len(a.spans))
		copy(spans, a.spans)
		a.mu.Unlock()
		var buf []byte
		for i, sp := range spans {
			buf = fmt.Appendf(buf, "  [%d] spanID=%d parentID=%d name=%q service=%q resource=%q type=%q\n",
				i, sp.SpanID, sp.ParentID, sp.Operation, sp.Service, sp.Resource, sp.Type)
			for _, m := range matchers {
				for _, cond := range m.FailedConditions(sp) {
					buf = fmt.Appendf(buf, "        FAIL: %s\n", cond)
				}
			}
		}
		t.Fatalf("no span found matching the given conditions; collected %d span(s):\n%s", len(spans), buf)
	}
	return s
}

// CountSpans returns the total number of spans collected so far.
func (a *agent) CountSpans() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.spans)
}
