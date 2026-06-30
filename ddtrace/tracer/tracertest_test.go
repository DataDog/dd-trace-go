// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// TODO: refactor code to become `x/tracertest`. This involves a major
// refactor of the codebase.
package tracer

import (
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/internal"
)

// testAgent is a mock Datadog agent that captures traces over HTTP.
// It handles both v0.4 and v1.0 payload formats, reusing the existing
// DecodeMsg (msgp-generated) for v0.4 and the v1 decode machinery
// (payloadV1.decodeBuffer, decodeTraceChunks, etc.) for v1.0.
type testAgent struct {
	server   *httptest.Server
	mu       sync.Mutex
	spans    []*Span
	requests []string
}

// startTestAgent creates and starts a mock agent. It is closed automatically
// when the test ends via tb.Cleanup.
func startTestAgent(tb testing.TB) *testAgent {
	tb.Helper()
	a := &testAgent{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v0.4/traces", a.handleTracesV04)
	mux.HandleFunc("/v1.0/traces", a.handleTracesV1)
	mux.HandleFunc("/info", a.handleInfo)
	a.server = httptest.NewServer(mux)
	tb.Cleanup(a.server.Close)
	return a
}

// Addr returns the agent's "host:port", suitable for tracer.WithAgentAddr().
func (a *testAgent) Addr() string {
	u, _ := url.Parse(a.server.URL)
	return u.Host
}

// URL returns the full base URL of the agent (e.g. "http://127.0.0.1:12345").
func (a *testAgent) URL() string {
	return a.server.URL
}

// Spans returns a snapshot copy of all spans received so far.
func (a *testAgent) Spans() []*Span {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make([]*Span, len(a.spans))
	copy(cp, a.spans)
	return cp
}

// SpanCount returns the number of spans received so far.
func (a *testAgent) SpanCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.spans)
}

// Requests returns a snapshot copy of trace intake paths with spans received so far.
func (a *testAgent) Requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make([]string, len(a.requests))
	copy(cp, a.requests)
	return cp
}

// Reset clears all received spans and request records.
func (a *testAgent) Reset() {
	a.mu.Lock()
	a.spans = a.spans[:0]
	a.requests = a.requests[:0]
	a.mu.Unlock()
}

func (a *testAgent) recordRequest(path string, spans []*Span) {
	if len(spans) == 0 {
		return
	}
	a.mu.Lock()
	a.requests = append(a.requests, path)
	a.spans = append(a.spans, spans...)
	a.mu.Unlock()
}

func (a *testAgent) handleInfo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"endpoints":["/v0.4/traces","/v1.0/traces","/v0.6/stats"],"client_drop_p0s":true,"span_events":true,"span_meta_structs":true}`))
}

func (a *testAgent) handleTracesV04(w http.ResponseWriter, r *http.Request) {
	reader := msgp.NewReader(r.Body)
	numTraces, err := reader.ReadArrayHeader()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var spans []*Span
	for range numTraces {
		numSpans, err := reader.ReadArrayHeader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for range numSpans {
			s := &Span{}
			if err := s.DecodeMsg(reader); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			spans = append(spans, s)
		}
	}
	a.recordRequest(r.URL.Path, spans)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"rate_by_service":{}}`))
}

func (a *testAgent) handleTracesV1(w http.ResponseWriter, r *http.Request) {
	// Copy the body directly into the payload buffer rather than buffering the
	// whole request with io.ReadAll first: payloadV1.Write appends to p.buf,
	// which decodeBuffer consumes in place, so a separate full-body slice would
	// just be an extra copy.
	p := newPayloadV1()
	if _, err := io.Copy(p, r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := p.decodeBuffer(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var spans []*Span
	for _, chunk := range p.chunks {
		var tid uint64
		if len(chunk.traceID) >= 16 {
			tid = binary.BigEndian.Uint64(chunk.traceID[8:])
		} else if len(chunk.traceID) >= 8 {
			tid = binary.BigEndian.Uint64(chunk.traceID)
		}
		for _, s := range chunk.spans {
			s.traceID = tid
			spans = append(spans, s)
		}
	}
	a.recordRequest(r.URL.Path, spans)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"rate_by_service":{}}`))
}

type testTraceProtocol struct {
	name    string
	version string
	path    string
}

var testTraceProtocols = []testTraceProtocol{
	{name: "v0.4", version: "0.4", path: tracesAPIPath},
	{name: "v1.0", version: "1.0", path: tracesAPIPathV1},
}

// newTracerTest creates a tracer with an httpTransport pointed at the mock agent.
// It sets the global tracer (required for span.Finish to push chunks through the pipeline).
func newTracerTest(tb testing.TB, agent *testAgent, opts ...StartOption) *tracer {
	tb.Helper()
	transport := newHTTPTransport(agent.URL()+tracesAPIPath, agent.URL()+statsAPIPath, internal.DefaultHTTPClient(defaultHTTPTimeout, true), datadogHeaders())
	baseOpts := []StartOption{
		withTransport(transport),
		WithHTTPClient(internal.DefaultHTTPClient(defaultHTTPTimeout, true)),
	}
	tr, err := newTracer(append(baseOpts, opts...)...)
	require.NoError(tb, err)
	setGlobalTracer(tr)
	return tr
}

// newAgentTracerTest creates a tracer configured through agent discovery so tests
// exercise the same protocol selection and HTTP endpoint that production uses.
func newAgentTracerTest(tb testing.TB, agent *testAgent, protocol testTraceProtocol, opts ...StartOption) *tracer {
	tb.Helper()
	tb.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", protocol.version)
	baseOpts := []StartOption{
		WithAgentAddr(agent.Addr()),
		WithHTTPClient(internal.DefaultHTTPClient(defaultHTTPTimeout, true)),
		WithSendRetries(0),
		withNoopStats(),
	}
	tr, err := newTracer(append(baseOpts, opts...)...)
	require.NoError(tb, err)
	setGlobalTracer(tr)
	return tr
}

// flushAgentTracerTest flushes tr and waits until the mock agent has received
// exactly wantSpans spans. Unlike tracer.Flush, this also waits for the async
// HTTP writer goroutine.
func flushAgentTracerTest(tb testing.TB, tr *tracer, agent *testAgent, wantSpans int) {
	tb.Helper()
	deadline := time.Now().Add(time.Second * timeMultiplicator)
	for {
		for len(tr.out) > 0 {
			if time.Now().After(deadline) {
				require.Zero(tb, len(tr.out), "timed out waiting for tracer worker to queue finished spans")
			}
			time.Sleep(time.Millisecond)
		}
		tr.Flush()
		if w, ok := tr.traceWriter.(*agentTraceWriter); ok {
			w.wg.Wait()
		}
		got := agent.SpanCount()
		switch {
		case got == wantSpans:
			return
		case got > wantSpans:
			require.Equal(tb, wantSpans, got)
		}
		if time.Now().After(deadline) {
			require.Equal(tb, wantSpans, got, "timed out waiting for mock agent spans")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stopTracerTest stops the tracer and resets global state.
func stopTracerTest(tr *tracer) {
	tr.Flush()
	if w, ok := tr.traceWriter.(*agentTraceWriter); ok {
		w.wg.Wait()
	}
	tr.Stop()
	setGlobalTracer(&NoopTracer{})
}
