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

	tracer.Start(
		tracer.WithAgentAddr(srvURL.Host),
		tracer.WithHTTPClient(&internalClient),
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
		tracer.WithLogger(testLogger{t}),
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
	var result trace.Traces
	for _, span := range spansByID {
		if span.ParentID == 0 {
			result = append(result, span)
			continue
		}
		parent, ok := spansByID[span.ParentID]
		if ok {
			parent.Children = append(parent.Children, span)
		}
	}
	return result
}

type testLogger struct {
	*testing.T
}

func (l testLogger) Log(msg string) {
	l.T.Log(msg)
}

var (
	defaultTransport, _ = http.DefaultTransport.(*http.Transport)
	// A copy of the default transport, except it will be marked internal by orchestrion, so it is not traced.
	internalTransport = &http.Transport{
		Proxy:                 defaultTransport.Proxy,
		DialContext:           defaultTransport.DialContext,
		ForceAttemptHTTP2:     defaultTransport.ForceAttemptHTTP2,
		MaxIdleConns:          defaultTransport.MaxIdleConns,
		IdleConnTimeout:       defaultTransport.IdleConnTimeout,
		TLSHandshakeTimeout:   defaultTransport.TLSHandshakeTimeout,
		ExpectContinueTimeout: defaultTransport.ExpectContinueTimeout,
	}

	internalClient = http.Client{Transport: internalTransport}
)
