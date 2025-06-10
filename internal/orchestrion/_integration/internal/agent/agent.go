// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type MockAgent struct {
	T        *testing.T
	mu       sync.RWMutex
	payloads []pb.Traces
	srv      *httptest.Server
}

func (m *MockAgent) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	m.T.Logf("mockagent: handling request: %s", req.URL.String())

	// Put a custom tag on the span generated to skip it in assertions.
	span, _ := tracer.SpanFromContext(req.Context())
	span.SetTag("mockagent.span", true)

	switch req.URL.Path {
	case "/v0.4/traces":
		m.handleTraces(req)
	default:
		m.T.Logf("mockagent: handler not implemented for path: %s", req.URL.String())
	}

	w.WriteHeader(200)
	_, err := w.Write([]byte("{}"))
	assert.NoError(m.T, err)
}

func (m *MockAgent) handleTraces(req *http.Request) {
	var payload pb.Traces
	err := decodeRequest(req, &payload)
	require.NoError(m.T, err)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.payloads = append(m.payloads, payload)
}

func decodeRequest(req *http.Request, dest *pb.Traces) error {
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	defer req.Body.Close()

	_, err = dest.UnmarshalMsg(b)
	return err
}

func New(t *testing.T) *MockAgent {
	return &MockAgent{T: t}
}

func (m *MockAgent) Start(t *testing.T) {
	m.T.Log("mockagent: starting")

	srv := httptest.NewServer(m)
	m.srv = srv
	t.Cleanup(srv.Close)

	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	// Neutralize API Security sampling (always-keep), to prevent tests becoming flaky.
	t.Setenv("DD_API_SECURITY_SAMPLE_DELAY", "0")

	tracer.Start(
		tracer.WithAgentAddr(srvURL.Host),
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
		tracer.WithLogger(testLogger{t}),
		tracer.WithAppSecEnabled(true),
	)
	t.Cleanup(tracer.Stop)
}

func (m *MockAgent) Traces(t *testing.T) trace.Traces {
	m.T.Log("mockagent: fetching spans")

	tracer.Flush()
	tracer.Stop()
	m.srv.Close()

	m.mu.RLock()
	defer m.mu.RUnlock()

	spansByID := make(map[trace.ID]*trace.Trace)
	for _, payload := range m.payloads {
		for _, spans := range payload {
			for _, span := range spans {
				b, err := json.Marshal(span)
				require.NoError(t, err)

				var tr trace.Trace
				err = json.Unmarshal(b, &tr)
				require.NoError(t, err)

				spansByID[trace.ID(span.SpanID)] = &tr
			}
		}
	}

	// If span are filtered we remove all their children as well
	keptSpansByID := make(map[trace.ID]*trace.Trace)
	for id, span := range spansByID {
		if filterInternalSpans(t, span, m.srv.URL) {
			keptSpansByID[id] = span
			continue
		}

		t.Logf("mockagent: filtering out span %d: %s", id, span.String())
	}

	var result trace.Traces
	for _, span := range keptSpansByID {
		if span.ParentID == 0 {
			result = append(result, span)
			continue
		}
		parent, ok := keptSpansByID[span.ParentID]
		if ok {
			parent.Children = append(parent.Children, span)
		}
	}
	return result
}

// filterInternalSpans recursively checks the spans in the trace to ensure that that no spans a created as a result of
// connection to the agent or any agentless HTTP call and also filters out any spans that are created by the testing framework
func filterInternalSpans(t *testing.T, trace *trace.Trace, agentHost string) bool {
	t.Helper()
	if trace == nil {
		return false
	}

	if _, ok := trace.Meta["mockagent.span"]; ok {
		// This span is created by the mock agent to skip it in assertions
		return false
	}

	// Make sure no spans are created from a connection to the agent
	if strings.Contains(trace.Meta["http.url"], agentHost) {
		assert.Fail(t, "trace should not contain the agent host URL %s: %s", agentHost, trace.String())
		return false
	}

	// Make sure no spans are created from any agentless http call
	if strings.Contains(trace.Meta["http.url"], "datadoghq") {
		assert.Fail(t, "trace should not contain a datadog URL: %s", trace.String())
		return false
	}

	return true
}

type testLogger struct {
	*testing.T
}

func (l testLogger) Log(msg string) {
	l.T.Log(msg)
}
