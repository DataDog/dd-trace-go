// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package testtracer provides a compatibility wrapper over the inspectable
// tracer with the API of the testtracer package that existed before the
// inspectable tracer. Use it to keep existing test suites compiling while you
// migrate them. Write new test suites against ddtrace/x/tracertest and
// ddtrace/x/llmobstest directly.
//
// The wrapper starts the global tracer through tracertest.Bootstrap, backed by
// an in-process mock agent and an in-process LLMObs collector. No request
// leaves the test process. Flushes are synchronous: WaitFor flushes the tracer
// before every check, so spans the test created before the call arrive without
// polling. The retry loop only covers spans that background goroutines create.
package testtracer

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/llmobstest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
)

// AgentInfo defines the response from the agent /info endpoint. The
// inspectable tracer fixes the /info response, so this type exists only so
// existing test code compiles.
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
// MetaStruct and SpanLinks stay nil because the mock agent does not decode
// them.
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
type LLMObsSpan = llmobstest.LLMObsSpan

// LLMObsMetric is an alias for the LLMObs metric type.
type LLMObsMetric = llmobstest.LLMObsMetric

// MockResponseFunc is a function to return mock responses. The inspectable
// tracer owns its in-process transports, so this type exists only so existing
// test code compiles.
type MockResponseFunc func(*http.Request) *http.Response

// Payloads contains all captured payloads organized by type. The span order
// within each field is arbitrary.
type Payloads struct {
	Spans      []Span
	LLMSpans   []LLMObsSpan
	LLMMetrics []LLMObsMetric
}

// WaitCondition is a function that checks if the wait condition is met. It
// receives the current payloads and returns true if waiting should stop.
type WaitCondition func(*Payloads) bool

// TestTracer is an inspectable tracer useful for tests.
type TestTracer struct {
	startError error
	tracer     tracer.Tracer
	agent      agenttest.Agent
	collector  *llmobstest.Collector
}

// Start starts the global tracer through tracertest.Bootstrap with an
// in-process agent and LLMObs collector, and returns a TestTracer that
// inspects the spans the application sends. The tracer stops automatically
// when the test ends.
func Start(t testing.TB, opts ...Option) *TestTracer {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	coll := llmobstest.New(t)
	if cfg.RequestDelay > 0 {
		coll.SetSpanResponseDelay(cfg.RequestDelay)
	}

	startOpts := append([]tracer.StartOption{
		tracer.WithEnv("TestTracer"),
		tracer.WithService("TestTracer"),
		tracer.WithServiceVersion("1.0.0"),
		tracer.WithLogger(&testLogger{T: t}),
		coll.TracerOption(),
	}, cfg.TracerStartOpts...)

	tr, agent, err := tracertest.Bootstrap(t, startOpts...)
	if cfg.RequireNoError {
		require.NoError(t, err)
	}
	return &TestTracer{
		startError: err,
		tracer:     tr,
		agent:      agent,
		collector:  coll,
	}
}

type config struct {
	TracerStartOpts []tracer.StartOption
	RequestDelay    time.Duration
	RequireNoError  bool
}

func defaultConfig() *config {
	return &config{
		TracerStartOpts: nil,
		RequestDelay:    0,
		RequireNoError:  true,
	}
}

// Option configures the TestTracer.
type Option func(*config)

// WithTracerStartOpts sets [tracer.StartOption] values on the tracer.
func WithTracerStartOpts(opts ...tracer.StartOption) Option {
	return func(cfg *config) {
		cfg.TracerStartOpts = append(cfg.TracerStartOpts, opts...)
	}
}

// WithAgentInfoResponse has no effect. The inspectable tracer fixes the agent
// /info response, and the LLMObs collector bypasses the agent capability gate
// that the old mock served through this option. The option exists so existing
// test code compiles.
func WithAgentInfoResponse(AgentInfo) Option {
	return func(*config) {}
}

// WithRequestDelay introduces a fake delay before the LLMObs collector answers
// a span batch. Unlike the delay in the old package, it does not apply to APM
// trace flushes.
func WithRequestDelay(delay time.Duration) Option {
	return func(cfg *config) {
		cfg.RequestDelay = delay
	}
}

// WithMockResponses has no effect. The inspectable tracer owns its in-process
// transports, so the wrapper cannot intercept requests. Tests that depend on
// mock responses must migrate to ddtrace/x/tracertest or ddtrace/x/llmobstest.
// The option exists so existing test code compiles.
func WithMockResponses(MockResponseFunc) Option {
	return func(*config) {}
}

// WithRequireNoTracerStartError controls whether Start fails the test when the
// tracer returns a start error. The default is true.
func WithRequireNoTracerStartError(requireNoErr bool) Option {
	return func(cfg *config) {
		cfg.RequireNoError = requireNoErr
	}
}

// StartError returns the error from the tracer start.
func (tt *TestTracer) StartError() error {
	return tt.startError
}

// Stop has no effect. tracertest.Bootstrap registers the cleanup that stops
// the tracer, the LLMObs subsystem, and the global tracer state when the test
// ends. The method exists so existing test code compiles.
func (tt *TestTracer) Stop() {}

// WaitFor waits for a condition to be met within the specified timeout.
// The condition function receives the current payloads and should return true
// when the wait should stop. It fails the test if the condition is not met
// within the timeout.
func (tt *TestTracer) WaitFor(t testing.TB, timeout time.Duration, cond WaitCondition) *Payloads {
	timeoutChan := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if tt.tracer != nil {
			tt.tracer.Flush()
		}
		p := tt.snapshot()
		if cond(p) {
			return p
		}
		select {
		case <-ticker.C:
		case <-timeoutChan:
			assert.FailNowf(t, "timeout waiting for condition",
				"Current payloads: %d spans, %d LLM spans, %d LLM metrics",
				len(p.Spans), len(p.LLMSpans), len(p.LLMMetrics))
		}
	}
}

// WaitForSpans waits for the specified number of spans to be captured.
// It returns the captured spans or fails the test if the timeout is reached.
// The span order is arbitrary.
func (tt *TestTracer) WaitForSpans(t *testing.T, count int) []Span {
	if count == 0 {
		return nil
	}
	p := tt.WaitFor(t, 5*time.Second, func(p *Payloads) bool {
		return len(p.Spans) >= count
	})
	return p.Spans
}

// WaitForLLMObsSpans waits for the specified number of LLMObs spans to be
// captured. It returns the captured LLMObs spans or fails the test if the
// timeout is reached.
func (tt *TestTracer) WaitForLLMObsSpans(t *testing.T, count int) []LLMObsSpan {
	if count == 0 {
		return nil
	}
	p := tt.WaitFor(t, 5*time.Second, func(p *Payloads) bool {
		return len(p.LLMSpans) >= count
	})
	return p.LLMSpans
}

// WaitForLLMObsMetrics waits for the specified number of LLMObs metrics to be
// captured. It returns the captured LLMObs metrics or fails the test if the
// timeout is reached.
func (tt *TestTracer) WaitForLLMObsMetrics(t *testing.T, count int) []LLMObsMetric {
	if count == 0 {
		return nil
	}
	p := tt.WaitFor(t, 5*time.Second, func(p *Payloads) bool {
		return len(p.LLMMetrics) >= count
	})
	return p.LLMMetrics
}

// SentPayloads returns a copy of all captured payloads. It does not flush;
// call WaitFor or tracer.Flush first when the test created spans after the
// last flush.
func (tt *TestTracer) SentPayloads() Payloads {
	return *tt.snapshot()
}

// snapshot captures every payload collected so far. It does not flush. When
// the tracer start failed, the agent or the collector can be nil, so snapshot
// returns the payloads that exist. A custom agent that does not implement
// agenttest.SpanLister contributes no APM spans; the agent that Bootstrap
// creates always implements it.
func (tt *TestTracer) snapshot() *Payloads {
	p := &Payloads{}
	if tt.collector != nil {
		p.LLMSpans = tt.collector.Spans()
		p.LLMMetrics = tt.collector.Metrics()
	}
	if lister, ok := tt.agent.(agenttest.SpanLister); ok {
		for _, s := range lister.Spans() {
			p.Spans = append(p.Spans, toSpan(s))
		}
	}
	return p
}

// toSpan converts a mock-agent span to the wire-format Span this package
// exposes.
func toSpan(s *agenttest.Span) Span {
	return Span{
		Name:     s.Operation,
		Service:  s.Service,
		Resource: s.Resource,
		Type:     s.Type,
		Start:    s.Start,
		Duration: s.Duration,
		Meta:     s.Meta,
		Metrics:  s.Metrics,
		SpanID:   s.SpanID,
		TraceID:  s.TraceID,
		ParentID: s.ParentID,
		Error:    s.Error,
	}
}

type testLogger struct {
	T testing.TB
}

func (l *testLogger) Log(msg string) {
	l.T.Log(msg)
}
