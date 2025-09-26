// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package testtracer provides a wrapper over the ddtrace/tracer package with a mocked transport that allows to inspect
// traces while keeping the rest of the tracer logic the same.
package testtracer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	llmobstransport "github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// chanSize is a big enough channel size so we never block sending while the test is running.
const chanSize = 10_0000

// AgentInfo defines the response from the agent /info endpoint.
type AgentInfo struct {
	Endpoints          []string    `json:"endpoints"`
	ClientDropP0s      bool        `json:"client_drop_p0s"`
	FeatureFlags       []string    `json:"feature_flags"`
	PeerTags           []string    `json:"peer_tags"`
	SpanMetaStruct     bool        `json:"span_meta_structs"`
	ObfuscationVersion int         `json:"obfuscation_version"`
	Config             AgentConfig `json:"config"`
}

// AgentConfig defines the agent config.
type AgentConfig struct {
	StatsdPort int `json:"statsd_port"`
}

// Span defines a span with the same format as it is sent to the agent.
type Span struct {
	Name       string             `json:"name"`
	Service    string             `json:"service"`
	Resource   string             `json:"resource"`
	Type       string             `json:"type"`
	Start      int64              `json:"start"`
	Duration   int64              `json:"duration"`
	Meta       map[string]string  `json:"meta"`
	MetaStruct map[string]any     `json:"meta_struct"`
	Metrics    map[string]float64 `json:"metrics"`
	SpanID     uint64             `json:"span_id"`
	TraceID    uint64             `json:"trace_id"`
	ParentID   uint64             `json:"parent_id"`
	Error      int32              `json:"error"`
	SpanLinks  []SpanLink         `json:"span_links"`
}

// SpanLink defines a span link with the same format as it is sent to the agent.
type SpanLink struct {
	TraceID     uint64            `json:"trace_id"`
	TraceIDHigh uint64            `json:"trace_id_high"`
	SpanID      uint64            `json:"span_id"`
	Attributes  map[string]string `json:"attributes"`
	Tracestate  string            `json:"tracestate"`
	Flags       uint32            `json:"flags"`
}

type LLMObsSpan = llmobstransport.LLMObsSpanEvent

// TestTracer is an inspectable tracer useful for tests.
type TestTracer struct {
	Spans        <-chan Span
	LLMSpans     <-chan LLMObsSpan
	roundTripper *mockTransport
}

// Start calls [tracer.Start] with a mocked transport and provides a new [TestTracer] that allows to inspect
// the spans produced by this application.
func Start(t testing.TB, opts ...Option) *TestTracer {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	spansChan := make(chan Span, chanSize)
	llmobsSpansChan := make(chan LLMObsSpan, chanSize)
	rt := &mockTransport{
		T:               t,
		spansChan:       spansChan,
		llmobsSpansChan: llmobsSpansChan,
		agentInfo:       cfg.AgentInfoResponse,
	}
	httpClient := &http.Client{
		Transport: rt,
	}
	tt := &TestTracer{
		Spans:        spansChan,
		LLMSpans:     llmobsSpansChan,
		roundTripper: rt,
	}
	t.Cleanup(tt.Stop)

	startOpts := append([]tracer.StartOption{
		tracer.WithEnv("TestTracer"),
		tracer.WithService("TestTracer"),
		tracer.WithServiceVersion("1.0.0"),
		tracer.WithHTTPClient(httpClient),
		tracer.WithLogger(&testLogger{T: t}),
	}, cfg.TracerStartOpts...)

	err := tracer.Start(startOpts...)
	require.NoError(t, err)

	return tt
}

type config struct {
	TracerStartOpts   []tracer.StartOption
	AgentInfoResponse AgentInfo
	RequestDelay      time.Duration
}

func defaultConfig() *config {
	return &config{
		TracerStartOpts:   nil,
		AgentInfoResponse: AgentInfo{},
		RequestDelay:      0,
	}
}

type Option func(*config)

// WithTracerStartOpts allows to set [tracer.StartOption] on the tracer.
func WithTracerStartOpts(opts ...tracer.StartOption) Option {
	return func(cfg *config) {
		cfg.TracerStartOpts = opts
	}
}

// WithAgentInfoResponse sets a custom /info agent response. It can be used to enable/disable certain features
// from the tracer that depend on whether the agent supports them or not.
func WithAgentInfoResponse(response AgentInfo) Option {
	return func(cfg *config) {
		cfg.AgentInfoResponse = response
	}
}

// WithRequestDelay introduces a fake delay in all requests.
func WithRequestDelay(delay time.Duration) Option {
	return func(cfg *config) {
		cfg.RequestDelay = delay
	}
}

// Stop stops the tracer. It should be called after the test finishes.
func (tt *TestTracer) Stop() {
	tt.roundTripper.Stop()
	tracer.Stop()
}

// WaitForSpans returns when receiving a number of Span equal to count. It fails the test if it did not receive
// that number of spans after 5 seconds.
func (tt *TestTracer) WaitForSpans(t *testing.T, count int) []Span {
	if count == 0 {
		return nil
	}
	// force a flush so we don't need to wait for the default flush interval.
	tracer.Flush()

	timeoutChan := time.After(5 * time.Second)
	spans := make([]Span, 0)

	for {
		select {
		case span := <-tt.Spans:
			spans = append(spans, span)
			if len(spans) == count {
				return spans
			}
		case <-timeoutChan:
			assert.FailNowf(t, "timeout waiting for spans", "got: %d, want: %d", len(spans), count)
		}
	}
}

// WaitForLLMObsSpans returns when receiving a number of LLMSpan equal to count. It fails the test if it did not receive
// that number of spans after 5 seconds.
func (tt *TestTracer) WaitForLLMObsSpans(t *testing.T, count int) []LLMObsSpan {
	if count == 0 {
		return nil
	}
	// force a flush so we don't need to wait for the default flush interval.
	tracer.Flush()

	timeoutChan := time.After(5 * time.Second)
	spans := make([]LLMObsSpan, 0)

	for {
		select {
		case span := <-tt.LLMSpans:
			spans = append(spans, span)
			if len(spans) == count {
				return spans
			}
		case <-timeoutChan:
			assert.FailNowf(t, "timeout waiting for LLMObs spans", "got: %d, want: %d", len(spans), count)
		}
	}
}

type mockTransport struct {
	T               testing.TB
	spansChan       chan Span
	llmobsSpansChan chan LLMObsSpan
	mu              sync.RWMutex
	finished        bool
	agentInfo       AgentInfo
	requestDelay    time.Duration
}

func (rt *mockTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	time.Sleep(rt.requestDelay)
	return rt.handleRequest(r), nil
}

func (rt *mockTransport) Stop() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.finished {
		return
	}
	rt.finished = true
	close(rt.spansChan)
	close(rt.llmobsSpansChan)
}

func (rt *mockTransport) handleRequest(r *http.Request) *http.Response {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.finished {
		return rt.emptyResponse(r)
	}

	switch r.URL.Path {
	case "/v0.4/traces":
		return rt.handleTraces(r)
	case "/info":
		return rt.handleInfo(r)
	case "/evp_proxy/v2/api/v2/llmobs", "/api/v2/llmobs":
		return rt.handleLLMObsSpanEvents(r)
	case "/v0.7/config", "/telemetry/proxy/api/v2/apmtelemetry":
		// known cases, no need to log these
		return rt.emptyResponse(r)
	default:
		rt.T.Logf("testtracer: received request to a non-implemented path: %s", r.URL.Path)
		return rt.emptyResponse(r)
	}
}

func (rt *mockTransport) emptyResponse(r *http.Request) *http.Response {
	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Request:    r,
	}
	resp.Header.Set("Content-Type", "application/json")
	return resp
}

func (rt *mockTransport) handleInfo(r *http.Request) *http.Response {
	data, err := json.Marshal(rt.agentInfo)
	require.NoError(rt.T, err)

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(data)),
		Request:    r,
	}
	resp.Header.Set("Content-Type", "application/json")
	return resp
}

func (rt *mockTransport) handleTraces(r *http.Request) (resp *http.Response) {
	resp = rt.emptyResponse(r)

	req := r.Clone(context.Background())
	defer req.Body.Close()

	buf, err := io.ReadAll(req.Body)
	require.NoError(rt.T, err)

	var payload bytes.Buffer
	_, err = msgp.UnmarshalAsJSON(&payload, buf)
	require.NoError(rt.T, err)

	var traces [][]Span
	err = json.Unmarshal(payload.Bytes(), &traces)
	require.NoError(rt.T, err)

	if len(traces) == 0 {
		return
	}
	for _, spans := range traces {
		for _, span := range spans {
			rt.spansChan <- span
		}
	}
	return
}

func (rt *mockTransport) handleLLMObsSpanEvents(r *http.Request) (resp *http.Response) {
	resp = rt.emptyResponse(r)

	req := r.Clone(context.Background())
	defer req.Body.Close()

	buf, err := io.ReadAll(req.Body)
	require.NoError(rt.T, err)

	var payload []llmobstransport.PushSpanEventsRequest
	err = json.Unmarshal(buf, &payload)
	require.NoError(rt.T, err)

	for _, p := range payload {
		for _, span := range p.Spans {
			rt.llmobsSpansChan <- *span
		}
	}
	return
}

type testLogger struct {
	T testing.TB
}

func (l *testLogger) Log(msg string) {
	l.T.Log(msg)
}
