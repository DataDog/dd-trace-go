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

// LLMObsSpan is an alias for the LLMObs span event type.
type LLMObsSpan = llmobstransport.LLMObsSpanEvent

// LLMObsMetric is an alias for the LLMObs metric type.
type LLMObsMetric = llmobstransport.LLMObsMetric

// MockResponseFunc is a function to return mock responses.
type MockResponseFunc func(*http.Request) *http.Response

// Payloads contains all captured payloads organized by type.
type Payloads struct {
	mu         sync.RWMutex
	Spans      []Span
	LLMSpans   []LLMObsSpan
	LLMMetrics []LLMObsMetric
}

// WaitCondition is a function that checks if the wait condition is met.
// It receives the current payloads and returns true if waiting should stop.
type WaitCondition func(*Payloads) bool

// TestTracer is an inspectable tracer useful for tests.
type TestTracer struct {
	payloads     *Payloads
	roundTripper *mockTransport
}

// Start calls [tracer.Start] with a mocked transport and provides a new [TestTracer] that allows to inspect
// the spans produced by this application.
func Start(t testing.TB, opts ...Option) *TestTracer {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	payloadChan := make(chan any)
	payloads := &Payloads{}

	rt := &mockTransport{
		T:            t,
		payloadChan:  payloadChan,
		agentInfo:    cfg.AgentInfoResponse,
		mockResponse: cfg.MockResponse,
		requestDelay: cfg.RequestDelay,
	}
	httpClient := &http.Client{
		Transport: rt,
	}
	tt := &TestTracer{
		payloads:     payloads,
		roundTripper: rt,
	}

	// Start payload collector goroutine
	go tt.collectPayloads(payloadChan)
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
	MockResponse      MockResponseFunc
}

func defaultConfig() *config {
	return &config{
		TracerStartOpts:   nil,
		AgentInfoResponse: AgentInfo{},
		RequestDelay:      0,
		MockResponse:      nil,
	}
}

// Option configures the TestTracer.
type Option func(*config)

// WithTracerStartOpts allows to set [tracer.StartOption] on the tracer.
func WithTracerStartOpts(opts ...tracer.StartOption) Option {
	return func(cfg *config) {
		cfg.TracerStartOpts = append(cfg.TracerStartOpts, opts...)
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

// WithMockResponses allows setting a custom request handler for mocking HTTP responses.
// If the provided function returns nil, it fallbacks to the default behavior of returning empty 200 responses.
func WithMockResponses(mr MockResponseFunc) Option {
	return func(cfg *config) {
		cfg.MockResponse = mr
	}
}

// collectPayloads runs in a goroutine and collects payloads from the channel
func (tt *TestTracer) collectPayloads(payloadChan <-chan any) {
	for payload := range payloadChan {
		tt.payloads.mu.Lock()
		switch p := payload.(type) {
		case Span:
			tt.payloads.Spans = append(tt.payloads.Spans, p)
		case LLMObsSpan:
			tt.payloads.LLMSpans = append(tt.payloads.LLMSpans, p)
		case LLMObsMetric:
			tt.payloads.LLMMetrics = append(tt.payloads.LLMMetrics, p)
		}
		tt.payloads.mu.Unlock()
	}
}

// Stop stops the tracer. It should be called after the test finishes.
func (tt *TestTracer) Stop() {
	tt.roundTripper.Stop()
	tracer.Stop()
}

// WaitFor waits for a condition to be met within the specified timeout.
// The condition function receives the current payloads and should return true when the wait should stop.
// It fails the test if the condition is not met within the timeout.
func (tt *TestTracer) WaitFor(t testing.TB, timeout time.Duration, cond WaitCondition) *Payloads {
	// Force a flush so we don't need to wait for the default flush interval
	tracer.Flush()

	timeoutChan := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tt.payloads.mu.RLock()
			if cond(tt.payloads) {
				tt.payloads.mu.RUnlock()
				return tt.payloads
			}
			tt.payloads.mu.RUnlock()
		case <-timeoutChan:
			tt.payloads.mu.RLock()
			assert.FailNowf(t, "timeout waiting for condition",
				"Current payloads: %d spans, %d LLM spans, %d LLM metrics",
				len(tt.payloads.Spans), len(tt.payloads.LLMSpans), len(tt.payloads.LLMMetrics))
			tt.payloads.mu.RUnlock()
		}
	}
}

// WaitForSpans waits for the specified number of spans to be captured.
// It returns the captured spans or fails the test if the timeout is reached.
func (tt *TestTracer) WaitForSpans(t *testing.T, count int) []Span {
	if count == 0 {
		return nil
	}
	p := tt.WaitFor(t, 5*time.Second, func(p *Payloads) bool {
		return len(p.Spans) >= count
	})
	return p.Spans
}

// WaitForLLMObsSpans waits for the specified number of LLMObs spans to be captured.
// It returns the captured LLMObs spans or fails the test if the timeout is reached.
func (tt *TestTracer) WaitForLLMObsSpans(t *testing.T, count int) []LLMObsSpan {
	if count == 0 {
		return nil
	}
	p := tt.WaitFor(t, 5*time.Second, func(p *Payloads) bool {
		return len(p.LLMSpans) >= count
	})
	return p.LLMSpans
}

// WaitForLLMObsMetrics waits for the specified number of LLMObs metrics to be captured.
// It returns the captured LLMObs metrics or fails the test if the timeout is reached.
func (tt *TestTracer) WaitForLLMObsMetrics(t *testing.T, count int) []LLMObsMetric {
	if count == 0 {
		return nil
	}
	p := tt.WaitFor(t, 5*time.Second, func(p *Payloads) bool {
		return len(p.LLMMetrics) >= count
	})
	return p.LLMMetrics
}

// SentPayloads returns a thread-safe copy of all captured payloads.
func (tt *TestTracer) SentPayloads() Payloads {
	tt.payloads.mu.RLock()
	defer tt.payloads.mu.RUnlock()

	return Payloads{
		Spans:      append([]Span(nil), tt.payloads.Spans...),
		LLMSpans:   append([]LLMObsSpan(nil), tt.payloads.LLMSpans...),
		LLMMetrics: append([]LLMObsMetric(nil), tt.payloads.LLMMetrics...),
	}
}

type mockTransport struct {
	T            testing.TB
	payloadChan  chan<- any
	mu           sync.RWMutex
	finished     bool
	agentInfo    AgentInfo
	requestDelay time.Duration
	mockResponse MockResponseFunc
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
	close(rt.payloadChan)
}

var noLogPaths = []string{
	"/v0.7/config",
	"/telemetry/proxy/api/v2/apmtelemetry",
	"/api/unstable/llm-obs/v1/",
}

func (rt *mockTransport) handleRequest(r *http.Request) *http.Response {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.finished {
		return rt.emptyResponse(r)
	}

	var (
		resp            *http.Response
		respOverwritten = false
	)
	if rt.mockResponse != nil {
		resp = rt.mockResponse(r)
		respOverwritten = true
	}
	if resp == nil {
		resp = rt.emptyResponse(r)
	}

	switch r.URL.Path {
	case "/v0.4/traces":
		rt.handleTraces(r)
	case "/info":
		if !respOverwritten {
			resp = rt.handleInfo(r)
		}
	case "/evp_proxy/v2/api/v2/llmobs", "/api/v2/llmobs":
		rt.handleLLMObsSpanEvents(r)
	case "/evp_proxy/v2/api/intake/llm-obs/v2/eval-metric", "/api/intake/llm-obs/v2/eval-metric":
		rt.handleLLMObsEvalMetrics(r)
	case "/v0.7/config", "/telemetry/proxy/api/v2/apmtelemetry":
		// known cases, no need to log these
	default:
		logWarn := true
		for _, p := range noLogPaths {
			if r.URL.Path == p || strings.Contains(r.URL.Path, p) {
				logWarn = false
				break
			}
		}
		if logWarn {
			rt.T.Logf("testtracer: received request to a non-implemented path: %s", r.URL.Path)
		}
	}
	return resp
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

func (rt *mockTransport) handleTraces(r *http.Request) {
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
			rt.payloadChan <- span
		}
	}
}

func (rt *mockTransport) handleLLMObsSpanEvents(r *http.Request) {
	req := r.Clone(context.Background())
	defer req.Body.Close()

	buf, err := io.ReadAll(req.Body)
	require.NoError(rt.T, err)

	var payload []llmobstransport.PushSpanEventsRequest
	err = json.Unmarshal(buf, &payload)
	require.NoError(rt.T, err)

	for _, p := range payload {
		for _, span := range p.Spans {
			rt.payloadChan <- *span
		}
	}
}

func (rt *mockTransport) handleLLMObsEvalMetrics(r *http.Request) {
	req := r.Clone(context.Background())
	defer req.Body.Close()

	buf, err := io.ReadAll(req.Body)
	require.NoError(rt.T, err)

	var payload llmobstransport.PushMetricsRequest
	err = json.Unmarshal(buf, &payload)
	require.NoError(rt.T, err)

	for _, metric := range payload.Data.Attributes.Metrics {
		rt.payloadChan <- *metric
	}
}

type testLogger struct {
	T testing.TB
}

func (l *testLogger) Log(msg string) {
	l.T.Log(msg)
}
