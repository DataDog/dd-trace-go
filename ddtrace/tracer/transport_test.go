// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"

	tinternal "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer/internal"
	"github.com/DataDog/dd-trace-go/v2/internal"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getTestSpan returns a Span with different fields set
func getTestSpan() *Span {
	return &Span{
		traceID:    42,
		spanID:     52,
		parentID:   42,
		spanType:   "web",
		service:    "high.throughput",
		name:       "sending.events",
		resource:   "SEND /data",
		start:      1481215590883401105,
		duration:   1000000000,
		meta:       tinternal.NewSpanMetaFromMap(map[string]string{"http.host": "192.168.0.1"}),
		metaStruct: map[string]any{"_dd.appsec.json": map[string]any{"triggers": []any{map[string]any{"id": "1"}}}},
		metrics:    map[string]float64{"http.monitor": 41.99},
	}
}

// getTestTrace returns a list of traces that is composed by “traceN“ number
// of traces, each one composed by “size“ number of spans.
func getTestTrace(traceN, size int) [][]*Span {
	var traces [][]*Span

	for range traceN {
		trace := []*Span{}
		for range size {
			trace = append(trace, getTestSpan())
		}
		traces = append(traces, trace)
	}
	return traces
}

func encode(traces [][]*Span) (payload, error) {
	p := newPayload(traceProtocolV04)
	for _, t := range traces {
		if _, err := p.push(t); err != nil {
			return p, err
		}
	}
	return p, nil
}

func TestTracesAgentIntegration(t *testing.T) {
	if !integration {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}
	assert := assert.New(t)

	testCases := []struct {
		payload [][]*Span
	}{
		{getTestTrace(1, 1)},
		{getTestTrace(10, 1)},
		{getTestTrace(1, 10)},
		{getTestTrace(10, 10)},
	}

	for _, tc := range testCases {
		transport := newHTTPTransport(defaultURL+tracesAPIPath, defaultURL+statsAPIPath, internal.DefaultHTTPClient(defaultHTTPTimeout, false), datadogHeaders())
		p, err := encode(tc.payload)
		assert.NoError(err)
		body, err := transport.send(p)
		assert.NoError(err)
		defer body.Close()
	}
}

func TestResolveAgentAddr(t *testing.T) {
	for _, tt := range []struct {
		inOpt            StartOption
		envHost, envPort string
		out              *url.URL
	}{
		{nil, "", "", &url.URL{Scheme: "http", Host: defaultAddress}},
		{nil, "ip.local", "", &url.URL{Scheme: "http", Host: fmt.Sprintf("ip.local:%s", defaultPort)}},
		{nil, "", "1234", &url.URL{Scheme: "http", Host: fmt.Sprintf("%s:1234", defaultHostname)}},
		{nil, "ip.local", "1234", &url.URL{Scheme: "http", Host: "ip.local:1234"}},
		{WithAgentAddr("host:1243"), "", "", &url.URL{Scheme: "http", Host: "host:1243"}},
		{WithAgentAddr("ip.other:9876"), "ip.local", "", &url.URL{Scheme: "http", Host: "ip.other:9876"}},
		{WithAgentAddr("ip.other:1234"), "", "9876", &url.URL{Scheme: "http", Host: "ip.other:1234"}},
		{WithAgentAddr("ip.other:8888"), "ip.local", "1234", &url.URL{Scheme: "http", Host: "ip.other:8888"}},
	} {
		t.Run("", func(t *testing.T) {
			if tt.envHost != "" {
				t.Setenv("DD_AGENT_HOST", tt.envHost)
			}
			if tt.envPort != "" {
				t.Setenv("DD_TRACE_AGENT_PORT", tt.envPort)
			}
			// Use CreateNew directly to test URL resolution without triggering
			// loadAgentFeatures, which would make real HTTP calls to the configured URL.
			c := new(config)
			c.internalConfig = internalconfig.CreateNew()
			if tt.inOpt != nil {
				tt.inOpt(c)
			}
			assert.Equal(t, tt.out, c.internalConfig.RawAgentURL())
		})
	}

	t.Run("UDS", func(t *testing.T) {
		old := internal.DefaultTraceAgentUDSPath
		d, err := os.Getwd()
		require.NoError(t, err)
		internal.DefaultTraceAgentUDSPath = d // Choose a file we know will exist
		defer func() { internal.DefaultTraceAgentUDSPath = old }()
		c := new(config)
		c.internalConfig = internalconfig.CreateNew()
		assert.Equal(t, &url.URL{Scheme: "unix", Path: d}, c.internalConfig.RawAgentURL())
	})
}

func TestTransportResponse(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		body   string
		err    string
	}{
		"ok": {
			status: http.StatusOK,
			body:   "Hello world!",
		},
		"bad": {
			status: http.StatusBadRequest,
			body:   strings.Repeat("X", 1002),
			err:    fmt.Sprintf("%s (Status: Bad Request)", strings.Repeat("X", 1000)),
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			transport := newHTTPTransport(srv.URL+tracesAPIPath, srv.URL+statsAPIPath, internal.DefaultHTTPClient(defaultHTTPTimeout, false), datadogHeaders())
			rc, err := transport.send(newPayload(traceProtocolV04))
			if tt.err != "" {
				assert.Equal(tt.err, err.Error())
				return
			}
			assert.NoError(err)
			slurp, err := io.ReadAll(rc)
			rc.Close()
			assert.NoError(err)
			assert.Equal(tt.body, string(slurp))
		})
	}
}

func TestFetchAgentFeaturesContainerTagsHash(t *testing.T) {
	t.Cleanup(func() { processtags.SetContainerTagsHash("") })
	processtags.SetContainerTagsHash("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/info", r.URL.Path)
		w.Header().Set(containerTagsHashHeader, "info-container-hash")
		w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true,"config":{}}`))
	}))
	defer srv.Close()

	agentURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	features, err := fetchAgentFeatures(context.Background(), agentURL, srv.Client())
	require.NoError(t, err)

	assert.True(t, features.Stats)
	assert.Equal(t, "info-container-hash", processtags.ContainerTagsHash())
}

func TestTraceCountHeader(t *testing.T) {
	assert := assert.New(t)

	testCases := []struct {
		payload [][]*Span
	}{
		{getTestTrace(1, 1)},
		{getTestTrace(10, 1)},
		{getTestTrace(100, 10)},
	}

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/info" {
			return
		}
		header := r.Header.Get("X-Datadog-Trace-Count")
		assert.NotEqual("", header, "X-Datadog-Trace-Count header should be here")
		count, err := strconv.Atoi(header)
		assert.Nil(err, "header should be an int")
		assert.NotEqual(0, count, "there should be a non-zero amount of traces")
	}))
	defer srv.Close()
	for _, tc := range testCases {
		transport := newHTTPTransport(srv.URL+tracesAPIPath, srv.URL+statsAPIPath, internal.DefaultHTTPClient(defaultHTTPTimeout, false), datadogHeaders())
		p, err := encode(tc.payload)
		assert.NoError(err)
		_, err = transport.send(p)
		assert.NoError(err)
	}
	assert.Equal(hits, len(testCases))
}

type recordingRoundTripper struct {
	reqs []*http.Request
	rt   http.RoundTripper
}

// wrapRecordingRoundTripper wraps the client Transport with one that records all
// requests sent over the transport.
func wrapRecordingRoundTripper(client *http.Client) *recordingRoundTripper {
	rt := &recordingRoundTripper{rt: client.Transport}
	client.Transport = rt
	if rt.rt == nil {
		// Follow http.(*Client).Transport semantics.
		rt.rt = http.DefaultTransport
	}
	return rt
}

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.reqs = append(r.reqs, req)
	return r.rt.RoundTrip(req)
}

func TestCustomTransport(t *testing.T) {
	assert := assert.New(t)

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		hits++
	}))
	defer srv.Close()

	c := &http.Client{}
	crt := wrapRecordingRoundTripper(c)
	transport := newHTTPTransport(srv.URL+tracesAPIPath, srv.URL+statsAPIPath, c, datadogHeaders())
	p, err := encode(getTestTrace(1, 1))
	assert.NoError(err)
	_, err = transport.send(p)
	assert.NoError(err)

	// make sure our custom round tripper was used
	assert.Len(crt.reqs, 1)
	assert.Equal(hits, 1)
}

type ErrTransport struct{}

func (t *ErrTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("error in RoundTripper")
}

type ErrResponseTransport struct{}

func (t *ErrResponseTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 400}, nil
}

type OkTransport struct{}

func (t *OkTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200}, nil
}

func TestApiErrorsMetric(t *testing.T) {
	t.Run("traces error", func(t *testing.T) {
		assert := assert.New(t)
		c := &http.Client{
			Transport: &ErrTransport{},
		}
		var tg statsdtest.TestStatsdClient
		trc, err := newTracer(WithHTTPClient(c), withStatsdClient(&tg))
		assert.NoError(err)
		setGlobalTracer(trc)
		defer trc.Stop()

		p, err := encode(getTestTrace(1, 1))
		assert.NoError(err)

		// We're expecting an error
		_, err = trc.config.ddTransport.send(p)
		assert.Error(err)
		calls := statsdtest.FilterCallsByName(tg.IncrCalls(), "datadog.tracer.api.errors")
		assert.Len(calls, 1)
		call := calls[0]
		assert.Equal([]string{"reason:network_failure", "endpoint:" + tracesAPIPath}, call.Tags())

	})
	t.Run("traces response with err code", func(t *testing.T) {
		assert := assert.New(t)
		c := &http.Client{
			Transport: &ErrResponseTransport{},
		}
		var tg statsdtest.TestStatsdClient
		trc, err := newTracer(WithHTTPClient(c), withStatsdClient(&tg))
		assert.NoError(err)
		setGlobalTracer(trc)
		defer trc.Stop()

		p, err := encode(getTestTrace(1, 1))
		assert.NoError(err)

		_, err = trc.config.ddTransport.send(p)
		assert.Error(err)

		calls := statsdtest.FilterCallsByName(tg.IncrCalls(), "datadog.tracer.api.errors")
		assert.Len(calls, 1)
		call := calls[0]
		assert.Equal([]string{"reason:server_response_400", "endpoint:" + tracesAPIPath}, call.Tags())
	})
	t.Run("stats error", func(t *testing.T) {
		assert := assert.New(t)
		c := &http.Client{
			Transport: &ErrTransport{},
		}
		var tg statsdtest.TestStatsdClient
		trc, err := newTracer(WithHTTPClient(c), withStatsdClient(&tg))
		assert.NoError(err)
		setGlobalTracer(trc)
		defer trc.Stop()

		// We're expecting an error
		err = trc.config.ddTransport.sendStats(&pb.ClientStatsPayload{}, 1)
		assert.Error(err)
		calls := statsdtest.FilterCallsByName(tg.IncrCalls(), "datadog.tracer.api.errors")
		assert.Len(calls, 1)
		call := calls[0]
		assert.Equal([]string{"reason:network_failure", "endpoint:" + statsAPIPath}, call.Tags())
	})
	t.Run("stats response with err code", func(t *testing.T) {
		assert := assert.New(t)
		c := &http.Client{
			Transport: &ErrResponseTransport{},
		}
		var tg statsdtest.TestStatsdClient
		trc, err := newTracer(WithHTTPClient(c), withStatsdClient(&tg))
		assert.NoError(err)
		setGlobalTracer(trc)
		defer trc.Stop()

		err = trc.config.ddTransport.sendStats(&pb.ClientStatsPayload{}, 1)
		assert.Error(err)

		calls := statsdtest.FilterCallsByName(tg.IncrCalls(), "datadog.tracer.api.errors")
		assert.Len(calls, 1)
		call := calls[0]
		assert.Equal([]string{"reason:server_response_400", "endpoint:" + statsAPIPath}, call.Tags())
	})
	t.Run("successful send - no metric", func(t *testing.T) {
		assert := assert.New(t)
		var tg statsdtest.TestStatsdClient
		c := &http.Client{
			Transport: &OkTransport{},
		}
		trc, err := newTracer(WithHTTPClient(c), withStatsdClient(&tg))
		assert.NoError(err)
		setGlobalTracer(trc)
		defer trc.Stop()

		p, err := encode(getTestTrace(1, 1))
		assert.NoError(err)

		_, err = trc.config.ddTransport.send(p)
		assert.NoError(err)

		calls := statsdtest.FilterCallsByName(tg.IncrCalls(), "datadog.tracer.api.errors")
		assert.Len(calls, 0)
	})
}

func TestWithHTTPClient(t *testing.T) {
	// disable instrumentation telemetry to prevent flaky number of requests
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DD_TRACE_STARTUP_LOGS", "0")
	assert := assert.New(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method == http.MethodGet {
			return
		}
		cl := r.Header.Get("Content-Length")
		assert.NotZero(cl)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	assert.NoError(err)
	c := &http.Client{}
	rt := wrapRecordingRoundTripper(c)
	trc, err := newTracer(WithAgentTimeout(2), WithAgentAddr(u.Host), WithHTTPClient(c))
	defer trc.Stop()
	assert.NoError(err)

	p, err := encode(getTestTrace(1, 1))
	assert.NoError(err)
	_, err = trc.config.ddTransport.send(p)
	assert.NoError(err)
	assert.Len(rt.reqs, 2)
	assert.Contains(rt.reqs[0].URL.Path, "/info")
	assert.Contains(rt.reqs[1].URL.Path, "/traces")
	assert.NotZero(rt.reqs[1].ContentLength)
	assert.Equal(hits, 2)
}

func TestWithUDS(t *testing.T) {
	// disable instrumentation telemetry to prevent flaky number of requests
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DD_TRACE_STARTUP_LOGS", "0")
	assert := assert.New(t)
	dir, err := os.MkdirTemp("", "socket")
	if err != nil {
		t.Fatal(err)
	}
	udsPath := filepath.Join(dir, "apm.socket")
	defer os.RemoveAll(udsPath)
	unixListener, err := net.Listen("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	var hits int
	srv := http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		hits++
	})}
	go srv.Serve(unixListener)
	defer srv.Close()

	trc, err := newTracer(WithUDS(udsPath))
	rt := wrapRecordingRoundTripper(trc.config.httpClient)
	defer trc.Stop()
	assert.NoError(err)

	p, err := encode(getTestTrace(1, 1))
	assert.NoError(err)
	body, err := trc.config.ddTransport.send(p)
	assert.NoError(err)
	defer body.Close()
	// There are 2 requests, but one happens on tracer startup before we wrap the round tripper.
	// This is OK for this test, since we just want to check that WithUDS allows communication
	// between a server and client over UDS. hits tells us that there were 2 requests received.
	assert.Len(rt.reqs, 1)
	assert.Equal(hits, 2)
}

// TestUDSTransportRecoversFromStaleIdleConn reproduces and verifies the fix for
// the failure mode reported in APMS-19533: the trace-agent silently
// drops idle keep-alive UDS connections under backpressure, so the next request
// reusing such a connection from the client's idle pool fails with
// `write: broken pipe` or `read: connection reset by peer`.
//
// The fix has two layers:
//  1. req.GetBody + Idempotency-Key let net/http's stdlib request-replay path
//     treat the POST as idempotent and silently retry on a fresh conn —
//     covers most cases (notably read-after-write failures).
//  2. doWithStaleConnRetry adds a small application-level retry for the
//     residual EPIPE/ECONNRESET window where stdlib refuses to retry because
//     some bytes were already written. See golang/go#19943,
//     net/http.Request.isReplayable, and net/http.persistConn.shouldRetryRequest.
//
// On baseline this test reports ~30-45% failures in the concurrent burst.
// With the fix every request succeeds because the stale-conn race is recovered
// transparently.
func TestUDSTransportRecoversFromStaleIdleConn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping UDS stale-idle recovery test in short mode")
	}

	dir, err := os.MkdirTemp("", "uds-stale-idle")
	require.NoError(t, err)
	defer os.RemoveAll(dir)
	socketPath := filepath.Join(dir, "trace.sock")

	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	// Each accepted conn handles exactly one request and then closes the
	// underlying conn abruptly — without a `Connection: close` header and
	// without graceful shutdown — so the client still believes the conn is
	// keep-alive and may pick it up again from its idle pool. This mirrors the
	// trace-agent's behavior under load.
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				serveOneRequestRawHTTP(c)
				// Brief delay so the response reaches the client and the conn
				// is returned to the idle pool, then this defer closes it
				// abruptly underneath the pool.
				time.Sleep(time.Millisecond)
			}(c)
		}
	}()

	transport := newHTTPTransport(
		"http://localhost"+tracesAPIPath,
		"http://localhost"+statsAPIPath,
		internal.UDSClient(socketPath, 5*time.Second),
		datadogHeaders(),
	)
	defer func() {
		// Close the listener first — causes the accept goroutine to exit on
		// the next Accept() call. Then drain serveDone so we know the goroutine
		// has fully exited before we return. Finally, close any idle connections
		// in the client transport so that the stdlib persistConn.readLoop
		// goroutines are also released; without this step goleak (used by the
		// multi-OS test matrix) reports leaked goroutines on Windows.
		ln.Close()
		<-serveDone
		transport.client.CloseIdleConnections()
	}()

	const (
		numGoroutines = 20
		requestsEach  = 20
	)

	t.Run("send", func(t *testing.T) {
		var (
			wg       sync.WaitGroup
			errs     atomic.Int64
			firstErr atomic.Value // error
		)
		for range numGoroutines {
			wg.Go(func() {
				for range requestsEach {
					p, err := encode(getTestTrace(1, 1))
					if err != nil {
						errs.Add(1)
						firstErr.CompareAndSwap(nil, err)
						continue
					}
					body, err := transport.send(p)
					if err != nil {
						errs.Add(1)
						firstErr.CompareAndSwap(nil, err)
						continue
					}
					io.Copy(io.Discard, body)
					body.Close()
				}
			})
		}
		wg.Wait()
		if n := errs.Load(); n > 0 {
			t.Errorf("%d/%d send calls failed despite Idempotency-Key + GetBody; first error: %v",
				n, numGoroutines*requestsEach, firstErr.Load())
		}
	})

	t.Run("sendStats", func(t *testing.T) {
		var (
			wg       sync.WaitGroup
			errs     atomic.Int64
			firstErr atomic.Value // error
		)
		for range numGoroutines {
			wg.Go(func() {
				for range requestsEach {
					if err := transport.sendStats(&pb.ClientStatsPayload{}, 1); err != nil {
						errs.Add(1)
						firstErr.CompareAndSwap(nil, err)
					}
				}
			})
		}
		wg.Wait()
		if n := errs.Load(); n > 0 {
			t.Errorf("%d/%d sendStats calls failed despite Idempotency-Key; first error: %v",
				n, numGoroutines*requestsEach, firstErr.Load())
		}
	})
}

// serveOneRequestRawHTTP reads a single HTTP/1.1 request from c (request line,
// headers, body) and writes back a minimal keep-alive 200 response. It does NOT
// close the conn — the caller is expected to do that after the response is
// flushed, simulating an abrupt server-side close on a keep-alive connection.
func serveOneRequestRawHTTP(c net.Conn) {
	br := bufio.NewReader(c)
	// Read request line.
	if _, err := br.ReadString('\n'); err != nil {
		return
	}
	// Read headers until the blank line, tracking Content-Length.
	var contentLen int
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if line == "\r\n" || line == "\n" {
			break
		}
		if cl, ok := parseContentLengthHeader(line); ok {
			contentLen = cl
		}
	}
	// Drain the body so the response write doesn't race with an in-flight write
	// on the client side. This assumes the request always carries a
	// Content-Length, which is true today because send() sets
	// req.ContentLength = int64(stats.size) explicitly. A future send method
	// that uses chunked encoding (ContentLength = -1) would not be drained
	// here and could cause spurious EPIPE failures in this test.
	if contentLen > 0 {
		if _, err := io.CopyN(io.Discard, br, int64(contentLen)); err != nil {
			return
		}
	}
	_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
}

func parseContentLengthHeader(line string) (int, bool) {
	line = strings.TrimRight(line, "\r\n")
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	return n, err == nil
}

func TestExternalEnvironment(t *testing.T) {
	t.Setenv("DD_EXTERNAL_ENV", "it-false,cn-nginx-webserver,pu-75a2b6d5-3949-4afb-ad0d-92ff0674e759")
	assert := assert.New(t)
	found := false
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		extEnv := r.Header.Get("Datadog-External-Env")
		if extEnv == "" {
			return
		}
		assert.Equal("it-false,cn-nginx-webserver,pu-75a2b6d5-3949-4afb-ad0d-92ff0674e759", extEnv)
		found = true
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	assert.NoError(err)
	c := &http.Client{}
	trc, err := newTracer(WithAgentTimeout(2), WithAgentAddr(u.Host), WithHTTPClient(c))
	assert.NoError(err)
	defer trc.Stop()

	p, err := encode(getTestTrace(1, 1))
	assert.NoError(err)
	_, err = trc.config.ddTransport.send(p)
	assert.NoError(err)
	assert.True(found)
}

func TestDefaultHeaders(t *testing.T) {
	assert := assert.New(t)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			return
		}
		assert.Equal(r.Header.Get("Datadog-Meta-Lang"), "go")
		assert.NotEqual(r.Header.Get("Datadog-Meta-Lang-Version"), "")
		assert.NotEqual(r.Header.Get("Datadog-Meta-Lang-Interpreter"), "")
		assert.NotEqual(r.Header.Get("Datadog-Meta-Tracer-Version"), "")
		assert.Equal(r.Header.Get("Content-Type"), "application/msgpack")
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	assert.NoError(err)
	c := &http.Client{}
	trc, err := newTracer(WithAgentTimeout(2), WithAgentAddr(u.Host), WithHTTPClient(c))
	assert.NoError(err)
	defer trc.Stop()

	// Test traces endpoint
	p, err := encode(getTestTrace(1, 1))
	assert.NoError(err)
	_, err = trc.config.ddTransport.send(p)
	assert.NoError(err)

	// Now stats endpoint
	err = trc.config.ddTransport.sendStats(&pb.ClientStatsPayload{}, 1)
	assert.NoError(err)
}

func TestClientComputedStatsHeader(t *testing.T) {
	t.Run("header-not-set-when-client-drop-p0s-not-supported", func(t *testing.T) {
		// When the agent does not support client_drop_p0s,
		// the Datadog-Client-Computed-Stats header should NOT be set
		assert := assert.New(t)
		var headerValue string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/info" {
				w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":false}`))
				return
			}
			headerValue = r.Header.Get("Datadog-Client-Computed-Stats")
		}))
		defer srv.Close()

		u, err := url.Parse(srv.URL)
		assert.NoError(err)
		trc, err := newTracer(WithAgentAddr(u.Host), WithStatsComputation(true))
		assert.NoError(err)
		setGlobalTracer(trc)
		defer trc.Stop()

		p, err := encode(getTestTrace(1, 1))
		assert.NoError(err)
		_, err = trc.config.ddTransport.send(p)
		assert.NoError(err)
		assert.Empty(headerValue, "Datadog-Client-Computed-Stats header should not be set when client_drop_p0s is not supported")
	})

	t.Run("header-not-set-when-stats-endpoint-not-supported", func(t *testing.T) {
		// When the agent does not support the /v0.6/stats endpoint,
		// the Datadog-Client-Computed-Stats header should NOT be set
		assert := assert.New(t)
		var headerValue string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/info" {
				w.Write([]byte(`{"endpoints":[],"client_drop_p0s":true}`))
				return
			}
			headerValue = r.Header.Get("Datadog-Client-Computed-Stats")
		}))
		defer srv.Close()

		u, err := url.Parse(srv.URL)
		assert.NoError(err)
		trc, err := newTracer(WithAgentAddr(u.Host), WithStatsComputation(true))
		assert.NoError(err)
		setGlobalTracer(trc)
		defer trc.Stop()

		p, err := encode(getTestTrace(1, 1))
		assert.NoError(err)
		_, err = trc.config.ddTransport.send(p)
		assert.NoError(err)
		assert.Empty(headerValue, "Datadog-Client-Computed-Stats header should not be set when stats endpoint is not supported")
	})

	t.Run("header-set-when-both-conditions-met", func(t *testing.T) {
		// When both conditions are met (stats endpoint + client_drop_p0s),
		// the Datadog-Client-Computed-Stats header should be set to "t"
		assert := assert.New(t)
		var headerValue string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/info" {
				w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true}`))
				return
			}
			headerValue = r.Header.Get("Datadog-Client-Computed-Stats")
		}))
		defer srv.Close()

		u, err := url.Parse(srv.URL)
		assert.NoError(err)
		trc, err := newTracer(WithAgentAddr(u.Host), WithStatsComputation(true))
		assert.NoError(err)
		setGlobalTracer(trc)
		defer trc.Stop()

		p, err := encode(getTestTrace(1, 1))
		assert.NoError(err)
		_, err = trc.config.ddTransport.send(p)
		assert.NoError(err)
		assert.Equal("t", headerValue, "Datadog-Client-Computed-Stats header should be set to 't' when both conditions are met")
	})
}

// TestConcurrentTraceFlushOverUDS verifies that multiple goroutines can send trace
// payloads concurrently through the HTTP transport backed by a real UDS socket without
// errors. This exercises the connection pool fix (MaxIdleConnsPerHost=100) under
// realistic end-to-end conditions rather than just asserting config values.
func TestConcurrentTraceFlushOverUDS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent UDS transport test in short mode")
	}

	dir, err := os.MkdirTemp("", "uds-transport-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	socketPath := filepath.Join(dir, "traces.socket")
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	var received atomic.Int64
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			received.Add(1)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"rate_by_service":{}}`)) //nolint:errcheck
		}),
	}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	udsURL := internal.UnixDataSocketURL(socketPath).String()
	client := internal.UDSClient(socketPath, 5*time.Second)
	transport := newHTTPTransport(udsURL+tracesAPIPath, udsURL+statsAPIPath, client, datadogHeaders())

	const numGoroutines = 20

	start := make(chan struct{})
	errs := make([]error, numGoroutines)
	var wg sync.WaitGroup

	for i := range numGoroutines {
		wg.Go(func() {
			<-start
			p, encErr := encode(getTestTrace(1, 1))
			if encErr != nil {
				errs[i] = encErr
				return
			}
			body, sendErr := transport.send(p)
			if sendErr != nil {
				errs[i] = sendErr
				return
			}
			body.Close()
		})
	}

	close(start)
	wg.Wait()

	for i, e := range errs {
		assert.NoError(t, e, "goroutine %d send failed", i)
	}
	assert.Equal(t, int64(numGoroutines), received.Load(),
		"server should have received all %d trace payloads", numGoroutines)
}

type stubTransport struct {
	err   error
	calls int
}

func (s *stubTransport) send(payload) (io.ReadCloser, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(strings.NewReader("OK")), nil
}

func (s *stubTransport) sendStats(*pb.ClientStatsPayload, int) error {
	s.calls++
	return s.err
}

func (s *stubTransport) endpoint() string { return "stub://" }

func TestFallbackTransport(t *testing.T) {
	t.Run("send: primary succeeds", func(t *testing.T) {
		primary := &stubTransport{}
		fallback := &stubTransport{}
		ft := &fallbackTransport{primary: primary, fallback: fallback}
		_, err := ft.send(nil)
		assert.NoError(t, err)
		assert.Equal(t, 1, primary.calls)
		assert.Equal(t, 0, fallback.calls)
	})

	t.Run("send: ENOENT falls back", func(t *testing.T) {
		primary := &stubTransport{err: fmt.Errorf("dial: %w", syscall.ENOENT)}
		fallback := &stubTransport{}
		ft := &fallbackTransport{primary: primary, fallback: fallback}
		_, err := ft.send(nil)
		assert.NoError(t, err)
		assert.Equal(t, 1, fallback.calls)
	})

	t.Run("send: ECONNREFUSED falls back", func(t *testing.T) {
		primary := &stubTransport{err: fmt.Errorf("dial: %w", syscall.ECONNREFUSED)}
		fallback := &stubTransport{}
		ft := &fallbackTransport{primary: primary, fallback: fallback}
		_, err := ft.send(nil)
		assert.NoError(t, err)
		assert.Equal(t, 1, fallback.calls)
	})

	t.Run("send: HTTP error does not fall back", func(t *testing.T) {
		primary := &stubTransport{err: errors.New("400 Bad Request")}
		fallback := &stubTransport{}
		ft := &fallbackTransport{primary: primary, fallback: fallback}
		_, err := ft.send(nil)
		assert.Error(t, err)
		assert.Equal(t, 0, fallback.calls)
	})

	t.Run("sendStats: primary succeeds", func(t *testing.T) {
		primary := &stubTransport{}
		fallback := &stubTransport{}
		ft := &fallbackTransport{primary: primary, fallback: fallback}
		err := ft.sendStats(nil, 0)
		assert.NoError(t, err)
		assert.Equal(t, 1, primary.calls)
		assert.Equal(t, 0, fallback.calls)
	})

	t.Run("sendStats: ENOENT falls back", func(t *testing.T) {
		primary := &stubTransport{err: fmt.Errorf("dial: %w", syscall.ENOENT)}
		fallback := &stubTransport{}
		ft := &fallbackTransport{primary: primary, fallback: fallback}
		err := ft.sendStats(nil, 0)
		assert.NoError(t, err)
		assert.Equal(t, 1, fallback.calls)
	})

	t.Run("sendStats: ECONNREFUSED falls back", func(t *testing.T) {
		primary := &stubTransport{err: fmt.Errorf("dial: %w", syscall.ECONNREFUSED)}
		fallback := &stubTransport{}
		ft := &fallbackTransport{primary: primary, fallback: fallback}
		err := ft.sendStats(nil, 0)
		assert.NoError(t, err)
		assert.Equal(t, 1, fallback.calls)
	})

	t.Run("sendStats: HTTP error does not fall back", func(t *testing.T) {
		primary := &stubTransport{err: errors.New("400 Bad Request")}
		fallback := &stubTransport{}
		ft := &fallbackTransport{primary: primary, fallback: fallback}
		err := ft.sendStats(nil, 0)
		assert.Error(t, err)
		assert.Equal(t, 0, fallback.calls)
	})
}
