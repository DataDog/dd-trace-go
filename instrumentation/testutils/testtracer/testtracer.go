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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type AgentConfig struct {
	StatsdPort int `json:"statsd_port"`
}

type AgentInfo struct {
	Endpoints          []string    `json:"endpoints"`
	ClientDropP0s      bool        `json:"client_drop_p0s"`
	FeatureFlags       []string    `json:"feature_flags"`
	PeerTags           []string    `json:"peer_tags"`
	SpanMetaStruct     bool        `json:"span_meta_structs"`
	ObfuscationVersion int         `json:"obfuscation_version"`
	Config             AgentConfig `json:"config"`
}

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

type SpanLink struct {
	TraceID     uint64            `json:"trace_id"`
	TraceIDHigh uint64            `json:"trace_id_high"`
	SpanID      uint64            `json:"span_id"`
	Attributes  map[string]string `json:"attributes"`
	Tracestate  string            `json:"tracestate"`
	Flags       uint32            `json:"flags"`
}

type mockTransport struct {
	*testing.T
	spansChan chan Span
	mu        sync.RWMutex
	finished  bool
	agentInfo AgentInfo
}

func (rt *mockTransport) RoundTrip(r *http.Request) (*http.Response, error) {
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
	default:
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

type TestTracer struct {
	Spans        <-chan Span
	roundTripper *mockTransport
}

func (tt TestTracer) WaitForSpans(t *testing.T, count int) []Span {
	if count == 0 {
		return nil
	}
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

func (tt TestTracer) Stop() {
	tt.roundTripper.Stop()
	tracer.Stop()
}

func Start(t *testing.T, opts ...tracer.StartOption) TestTracer {
	spansChan := make(chan Span)
	rt := &mockTransport{
		T:         t,
		spansChan: spansChan,
		agentInfo: AgentInfo{
			Endpoints:          nil,
			ClientDropP0s:      false,
			FeatureFlags:       nil,
			PeerTags:           nil,
			SpanMetaStruct:     false,
			ObfuscationVersion: 0,
			Config: AgentConfig{
				StatsdPort: 0,
			},
		},
	}
	httpClient := &http.Client{
		Transport: rt,
	}
	tt := TestTracer{Spans: spansChan, roundTripper: rt}
	t.Cleanup(tt.Stop)

	startOpts := append([]tracer.StartOption{
		tracer.WithEnv("test"),
		tracer.WithService("mocktracerv2"),
		tracer.WithServiceVersion("1.0.0"),
		tracer.WithHTTPClient(httpClient),
		tracer.WithLogger(&testLogger{T: t}),
	}, opts...)

	err := tracer.Start(startOpts...)
	require.NoError(t, err)

	return tt
}

type testLogger struct {
	*testing.T
}

func (l *testLogger) Log(msg string) {
	l.T.Log(msg)
}
