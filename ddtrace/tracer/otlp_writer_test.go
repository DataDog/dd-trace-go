// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/DataDog/dd-trace-go/v2/internal"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// testOTLPServer is a test HTTP server that captures OTLP payloads.
type testOTLPServer struct {
	*httptest.Server
	mu       sync.Mutex
	payloads [][]byte
	// failCount controls how many requests return 500 before succeeding.
	failCount int32
}

func newTestOTLPServer() *testOTLPServer {
	s := &testOTLPServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if remaining := atomic.AddInt32(&s.failCount, -1); remaining >= 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.payloads = append(s.payloads, body)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return s
}

func (s *testOTLPServer) getPayloads() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([][]byte, len(s.payloads))
	copy(cp, s.payloads)
	return cp
}

func (s *testOTLPServer) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.payloads)
}

func newTestOTLPWriter(t *testing.T, srv *testOTLPServer, opts ...StartOption) *otlpTraceWriter {
	t.Helper()
	cfg, err := newTestConfig(append(opts, func(c *config) {
		c.ddTransport = &simpleTransport{}
	})...)
	require.NoError(t, err)
	resource := buildResource(cfg.internalConfig)
	scope := &otlpcommon.InstrumentationScope{Name: "dd-trace-go", Version: version.Tag}
	baseSize := proto.Size(&otlptrace.TracesData{
		ResourceSpans: []*otlptrace.ResourceSpans{{
			Resource: resource,
			ScopeSpans: []*otlptrace.ScopeSpans{{
				Scope: scope,
			}},
		}},
	})
	return &otlpTraceWriter{
		config:    cfg,
		transport: newOTLPTransport(srv.Client(), srv.URL, nil),
		resource:  resource,
		scope:     scope,
		spans:     make([]*otlptrace.Span, 0),
		buffSize:  baseSize,
		baseSize:  baseSize,
		climit:    make(chan struct{}, concurrentConnectionLimit),
	}
}

func TestOTLPWriterImplementsTraceWriter(t *testing.T) {
	assert.Implements(t, (*traceWriter)(nil), &otlpTraceWriter{})
}

func TestOTLPWriterAdd(t *testing.T) {
	srv := newTestOTLPServer()
	defer srv.Close()
	w := newTestOTLPWriter(t, srv)

	spans := []*Span{
		newSpan("op1", "svc", "res1", 1, 1, 0),
		newSpan("op2", "svc", "res2", 2, 1, 1),
	}
	w.add(spans)

	w.mu.Lock()
	assert.Equal(t, 2, len(w.spans))
	w.mu.Unlock()
}

func TestOTLPWriterAddMultiple(t *testing.T) {
	srv := newTestOTLPServer()
	defer srv.Close()
	w := newTestOTLPWriter(t, srv)

	w.add([]*Span{newSpan("op1", "svc", "res", 1, 1, 0)})
	w.add([]*Span{newSpan("op2", "svc", "res", 2, 1, 0)})
	w.add([]*Span{newSpan("op3", "svc", "res", 3, 1, 0)})

	w.mu.Lock()
	assert.Equal(t, 3, len(w.spans))
	w.mu.Unlock()
}

func TestOTLPWriterFlushEmpty(t *testing.T) {
	srv := newTestOTLPServer()
	defer srv.Close()
	w := newTestOTLPWriter(t, srv)

	w.flush()
	w.wg.Wait()

	assert.Equal(t, 0, srv.requestCount())
}

func TestOTLPWriterFlush(t *testing.T) {
	srv := newTestOTLPServer()
	defer srv.Close()
	w := newTestOTLPWriter(t, srv)

	w.add([]*Span{
		newSpan("op1", "svc", "res", 1, 1, 0),
		newSpan("op2", "svc", "res", 2, 1, 0),
	})
	w.flush()
	w.wg.Wait()

	payloads := srv.getPayloads()
	require.Equal(t, 1, len(payloads))

	var tracesData otlptrace.TracesData
	err := proto.Unmarshal(payloads[0], &tracesData)
	require.NoError(t, err)

	rs := tracesData.ResourceSpans
	require.Equal(t, 1, len(rs))
	require.Equal(t, 1, len(rs[0].ScopeSpans))

	scope := rs[0].ScopeSpans[0].Scope
	require.NotNil(t, scope)
	assert.Equal(t, "dd-trace-go", scope.Name)
	assert.Equal(t, version.Tag, scope.Version)

	assert.Equal(t, 2, len(rs[0].ScopeSpans[0].Spans))
}

func TestOTLPWriterFlushClearsSpans(t *testing.T) {
	srv := newTestOTLPServer()
	defer srv.Close()
	w := newTestOTLPWriter(t, srv)

	w.add([]*Span{newSpan("op1", "svc", "res", 1, 1, 0)})
	w.flush()
	w.wg.Wait()

	w.mu.Lock()
	assert.Equal(t, 0, len(w.spans))
	w.mu.Unlock()

	// Second flush should be a no-op
	w.flush()
	w.wg.Wait()
	assert.Equal(t, 1, srv.requestCount())
}

func TestOTLPWriterFlushOnSize(t *testing.T) {
	t.Run("single large span triggers flush", func(t *testing.T) {
		srv := newTestOTLPServer()
		defer srv.Close()
		w := newTestOTLPWriter(t, srv)

		bigSpan := newSpan("op", "svc", "res", 1, 1, 0)
		bigSpan.meta["big"] = strings.Repeat("X", payloadSizeLimit+1)
		w.add([]*Span{bigSpan})
		w.wg.Wait()

		assert.GreaterOrEqual(t, srv.requestCount(), 1)
		w.mu.Lock()
		assert.Equal(t, 0, len(w.spans))
		w.mu.Unlock()
	})

	t.Run("many small spans accumulate past limit", func(t *testing.T) {
		srv := newTestOTLPServer()
		defer srv.Close()
		w := newTestOTLPWriter(t, srv)

		// Each span has ~1KB of meta, so we need ~payloadSizeLimit/1024 spans.
		spanSize := 1024
		numSpans := (payloadSizeLimit / spanSize) + 1
		for i := range numSpans {
			s := newSpan("op", "svc", "res", uint64(i+1), 1, 0)
			s.meta["data"] = strings.Repeat("X", spanSize)
			w.add([]*Span{s})
		}
		w.wg.Wait()

		assert.GreaterOrEqual(t, srv.requestCount(), 1)
	})

	t.Run("under limit does not trigger flush", func(t *testing.T) {
		srv := newTestOTLPServer()
		defer srv.Close()
		w := newTestOTLPWriter(t, srv)

		w.add([]*Span{newSpan("op", "svc", "res", 1, 1, 0)})

		assert.Equal(t, 0, srv.requestCount())
		w.mu.Lock()
		assert.Equal(t, 1, len(w.spans))
		w.mu.Unlock()
	})
}

func TestOTLPWriterFlushRetries(t *testing.T) {
	testcases := []struct {
		configRetries int
		failCount     int
		tracesSent    bool
		expAttempts   int
	}{
		{configRetries: 0, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 0, failCount: 1, tracesSent: false, expAttempts: 1},

		{configRetries: 1, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 1, failCount: 1, tracesSent: true, expAttempts: 2},
		{configRetries: 1, failCount: 2, tracesSent: false, expAttempts: 2},

		{configRetries: 2, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 2, failCount: 1, tracesSent: true, expAttempts: 2},
		{configRetries: 2, failCount: 2, tracesSent: true, expAttempts: 3},
		{configRetries: 2, failCount: 3, tracesSent: false, expAttempts: 3},
	}

	for _, tc := range testcases {
		name := fmt.Sprintf("retries=%d/fails=%d", tc.configRetries, tc.failCount)
		t.Run(name, func(t *testing.T) {
			var totalRequests int32
			srv := newTestOTLPServer()
			atomic.StoreInt32(&srv.failCount, int32(tc.failCount))
			defer srv.Close()

			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&totalRequests, 1)
				srv.Server.Config.Handler.ServeHTTP(w, r)
			})
			countingSrv := httptest.NewServer(mux)
			defer countingSrv.Close()

			w := newTestOTLPWriter(t, srv, func(c *config) {
				c.sendRetries = tc.configRetries
				c.internalConfig.SetRetryInterval(time.Millisecond, internalconfig.OriginCode)
			})
			w.transport = newOTLPTransport(countingSrv.Client(), countingSrv.URL, nil)

			w.add([]*Span{newSpan("op", "svc", "res", 1, 1, 0)})
			w.flush()
			w.wg.Wait()

			assert.Equal(t, int32(tc.expAttempts), atomic.LoadInt32(&totalRequests))
			assert.Equal(t, tc.tracesSent, len(srv.getPayloads()) > 0)
		})
	}
}

func TestOTLPWriterStop(t *testing.T) {
	srv := newTestOTLPServer()
	defer srv.Close()
	w := newTestOTLPWriter(t, srv)

	w.add([]*Span{newSpan("op", "svc", "res", 1, 1, 0)})
	w.stop()

	assert.Equal(t, 1, len(srv.getPayloads()))
}

func TestOTLPWriterConcurrency(t *testing.T) {
	srv := newTestOTLPServer()
	defer srv.Close()
	w := newTestOTLPWriter(t, srv)

	const numAdders = 20
	const spansPerAdder = 50
	const numFlushers = 10

	start := make(chan struct{})
	var wg sync.WaitGroup

	var spansAdded int32

	for range numAdders {
		wg.Go(func() {
			<-start
			for range spansPerAdder {
				w.add([]*Span{newSpan("op", "svc", "res", randUint64(), randUint64(), 0)})
				atomic.AddInt32(&spansAdded, 1)
			}
		})
	}

	for range numFlushers {
		wg.Go(func() {
			<-start
			for range 10 {
				w.flush()
			}
		})
	}

	close(start)
	wg.Wait()

	w.stop()

	assert.Equal(t, int32(numAdders*spansPerAdder), atomic.LoadInt32(&spansAdded))

	// Verify all sent payloads are valid protobuf
	totalSpans := 0
	for _, data := range srv.getPayloads() {
		var td otlptrace.TracesData
		err := proto.Unmarshal(data, &td)
		require.NoError(t, err)
		for _, rs := range td.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				totalSpans += len(ss.Spans)
			}
		}
	}
	assert.Equal(t, numAdders*spansPerAdder, totalSpans)
}

func TestOTLPWriterBuffSizeTracking(t *testing.T) {
	srv := newTestOTLPServer()
	defer srv.Close()
	w := newTestOTLPWriter(t, srv)

	t.Run("initial buffSize equals baseSize", func(t *testing.T) {
		w.mu.Lock()
		assert.Equal(t, w.baseSize, w.buffSize)
		assert.Greater(t, w.baseSize, 0)
		w.mu.Unlock()
	})

	t.Run("add increases buffSize", func(t *testing.T) {
		w.mu.Lock()
		before := w.buffSize
		w.mu.Unlock()

		w.add([]*Span{newSpan("op", "svc", "res", 1, 1, 0)})

		w.mu.Lock()
		assert.Greater(t, w.buffSize, before)
		w.mu.Unlock()
	})

	t.Run("flush resets buffSize to baseSize", func(t *testing.T) {
		w.flush()
		w.wg.Wait()

		w.mu.Lock()
		assert.Equal(t, w.baseSize, w.buffSize)
		w.mu.Unlock()
	})

	t.Run("buffSize approximates actual marshal size", func(t *testing.T) {
		spans := []*Span{
			newSpan("op1", "svc", "res", 10, 10, 0),
			newSpan("op2", "svc", "res", 20, 10, 10),
			newSpan("op3", "svc", "res", 30, 10, 10),
		}
		w.add(spans)

		w.mu.Lock()
		estimated := w.buffSize
		spansCopy := make([]*otlptrace.Span, len(w.spans))
		copy(spansCopy, w.spans)
		w.mu.Unlock()

		actual := proto.Size(&otlptrace.TracesData{
			ResourceSpans: []*otlptrace.ResourceSpans{{
				Resource: w.resource,
				ScopeSpans: []*otlptrace.ScopeSpans{{
					Scope: w.scope,
					Spans: spansCopy,
				}},
			}},
		})
		// The estimate is baseSize + sum(proto.Size(span)), which slightly
		// undercounts because it doesn't include the varint length prefix for
		// each span in the repeated field. Verify it's close but not over.
		assert.InDelta(t, actual, estimated, float64(actual)*0.05,
			"estimated %d should be within 5%% of actual %d", estimated, actual)

		w.flush()
		w.wg.Wait()
	})
}

// TestOTLPWriterDoesNotReuseAgentHTTPClient verifies that the OTLP writer
// creates its own HTTP client rather than reusing c.httpClient, which may
// have a UDS dialer configured for the Datadog agent.
func TestOTLPWriterDoesNotReuseAgentHTTPClient(t *testing.T) {
	srv := newTestOTLPServer()
	defer srv.Close()

	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", srv.URL)

	cfg, err := newTestConfig(func(c *config) {
		// Simulate a UDS-based agent: set c.httpClient to a UDS client
		// pointing at a non-existent socket. If the OTLP writer reuses
		// this client, its requests will fail.
		c.httpClient = internal.UDSClient("/tmp/nonexistent-agent.sock", 5*time.Second)
		c.ddTransport = &simpleTransport{}
	})
	require.NoError(t, err)

	w := newOTLPTraceWriter(cfg)

	w.add([]*Span{newBasicSpan("uds-regression-test")})
	w.stop()

	assert.Equal(t, 1, srv.requestCount(), "OTLP writer should reach TCP server, not the UDS agent socket")
}
