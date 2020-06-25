// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package tracer

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// integration indicates if the test suite should run integration tests.
var integration bool

func TestMain(m *testing.M) {
	_, integration = os.LookupEnv("INTEGRATION")
	os.Exit(m.Run())
}

// getTestSpan returns a Span with different fields set
func getTestSpan() *span {
	return &span{
		TraceID:  42,
		SpanID:   52,
		ParentID: 42,
		Type:     "web",
		Service:  "high.throughput",
		Name:     "sending.events",
		Resource: "SEND /data",
		Start:    1481215590883401105,
		Duration: 1000000000,
		Meta:     map[string]string{"http.host": "192.168.0.1"},
		Metrics:  map[string]float64{"http.monitor": 41.99},
	}
}

// getTestTrace returns a list of traces that is composed by ``traceN`` number
// of traces, each one composed by ``size`` number of spans.
func getTestTrace(traceN, size int) [][]*span {
	var traces [][]*span

	for i := 0; i < traceN; i++ {
		trace := []*span{}
		for j := 0; j < size; j++ {
			trace = append(trace, getTestSpan())
		}
		traces = append(traces, trace)
	}
	return traces
}

type mockDatadogAPIHandler struct {
	t *testing.T
}

func (m mockDatadogAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	assert := assert.New(m.t)

	header := r.Header.Get("X-Datadog-Trace-Count")
	assert.NotEqual("", header, "X-Datadog-Trace-Count header should be here")
	count, err := strconv.Atoi(header)
	assert.Nil(err, "header should be an int")
	assert.NotEqual(0, count, "there should be a non-zero amount of traces")
}

func mockDatadogAPINewServer(t *testing.T) *httptest.Server {
	handler := mockDatadogAPIHandler{t: t}
	server := httptest.NewServer(handler)
	return server
}

func TestTracesAgentIntegration(t *testing.T) {
	if !integration {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}
	assert := assert.New(t)

	testCases := []struct {
		payload [][]*span
	}{
		{getTestTrace(1, 1)},
		{getTestTrace(10, 1)},
		{getTestTrace(1, 10)},
		{getTestTrace(10, 10)},
	}

	for _, tc := range testCases {
		transport := newHTTPTransport(defaultAddress, defaultClient)
		p, err := encode(tc.payload)
		assert.NoError(err)
		_, err = transport.send(p)
		assert.NoError(err)
	}
}

func TestResolveAddr(t *testing.T) {
	for _, tt := range []struct {
		in, envHost, envPort, out string
	}{
		{"host", "", "", fmt.Sprintf("host:%s", defaultPort)},
		{"www.my-address.com", "", "", fmt.Sprintf("www.my-address.com:%s", defaultPort)},
		{"localhost", "", "", fmt.Sprintf("localhost:%s", defaultPort)},
		{":1111", "", "", fmt.Sprintf("%s:1111", defaultHostname)},
		{"", "", "", defaultAddress},
		{"custom:1234", "", "", "custom:1234"},
		{"", "", "", defaultAddress},
		{"", "ip.local", "", fmt.Sprintf("ip.local:%s", defaultPort)},
		{"", "", "1234", fmt.Sprintf("%s:1234", defaultHostname)},
		{"", "ip.local", "1234", "ip.local:1234"},
		{"ip.other", "ip.local", "", fmt.Sprintf("ip.local:%s", defaultPort)},
		{"ip.other:1234", "ip.local", "", "ip.local:1234"},
		{":8888", "", "1234", fmt.Sprintf("%s:1234", defaultHostname)},
		{"ip.other:8888", "", "1234", "ip.other:1234"},
		{"ip.other", "ip.local", "1234", "ip.local:1234"},
		{"ip.other:8888", "ip.local", "1234", "ip.local:1234"},
	} {
		t.Run("", func(t *testing.T) {
			if tt.envHost != "" {
				os.Setenv("DD_AGENT_HOST", tt.envHost)
				defer os.Unsetenv("DD_AGENT_HOST")
			}
			if tt.envPort != "" {
				os.Setenv("DD_TRACE_AGENT_PORT", tt.envPort)
				defer os.Unsetenv("DD_TRACE_AGENT_PORT")
			}
			assert.Equal(t, resolveAddr(tt.in), tt.out)
		})
	}
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
			ln, err := net.Listen("tcp4", ":0")
			assert.Nil(err)
			go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer ln.Close()
			addr := ln.Addr().String()
			transport := newHTTPTransport(addr, defaultClient)
			rc, err := transport.send(newPayload())
			if tt.err != "" {
				assert.Equal(tt.err, err.Error())
				return
			}
			assert.NoError(err)
			slurp, err := ioutil.ReadAll(rc)
			rc.Close()
			assert.NoError(err)
			assert.Equal(tt.body, string(slurp))
		})
	}
}

func TestTraceCountHeader(t *testing.T) {
	assert := assert.New(t)

	testCases := []struct {
		payload [][]*span
	}{
		{getTestTrace(1, 1)},
		{getTestTrace(10, 1)},
		{getTestTrace(100, 10)},
	}

	receiver := mockDatadogAPINewServer(t)
	parsedURL, err := url.Parse(receiver.URL)
	assert.NoError(err)
	host := parsedURL.Host
	_, port, err := net.SplitHostPort(host)
	assert.Nil(err)
	assert.NotEmpty(port, "port should be given, as it's chosen randomly")
	for _, tc := range testCases {
		transport := newHTTPTransport(host, defaultClient)
		p, err := encode(tc.payload)
		assert.NoError(err)
		_, err = transport.send(p)
		assert.NoError(err)
	}

	receiver.Close()
}

type recordingRoundTripper struct {
	reqs []*http.Request
}

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.reqs = append(r.reqs, req)
	return defaultClient.Transport.RoundTrip(req)
}

func TestCustomTransport(t *testing.T) {
	assert := assert.New(t)

	receiver := mockDatadogAPINewServer(t)
	defer receiver.Close()

	parsedURL, err := url.Parse(receiver.URL)
	assert.NoError(err)
	host := parsedURL.Host
	_, port, err := net.SplitHostPort(host)
	assert.Nil(err)
	assert.NotEmpty(port, "port should be given, as it's chosen randomly")

	customRoundTripper := new(recordingRoundTripper)
	transport := newHTTPTransport(host, &http.Client{Transport: customRoundTripper})
	p, err := encode(getTestTrace(1, 1))
	assert.NoError(err)
	_, err = transport.send(p)
	assert.NoError(err)

	// make sure our custom round tripper was used
	assert.Len(customRoundTripper.reqs, 1)
}

func TestWithHTTPClient(t *testing.T) {
	os.Setenv("DD_TRACE_STARTUP_LOGS", "0")
	defer os.Unsetenv("DD_TRACE_STARTUP_LOGS")
	assert := assert.New(t)
	srv := mockDatadogAPINewServer(t)
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	assert.NoError(err)
	rt := new(recordingRoundTripper)
	trc, _ := newTracer(WithAgentAddr(u.Host), WithHTTPClient(&http.Client{Transport: rt}))
	defer trc.Stop()

	p, err := encode(getTestTrace(1, 1))
	assert.NoError(err)
	_, err = trc.config.transport.send(p)
	assert.NoError(err)
	assert.Len(rt.reqs, 1)
}

// TestTransportHTTPRace defines a regression tests where the request body was being
// read even after http.Client.Do returns. See golang/go#33244
func TestTransportHTTPRace(t *testing.T) {
	srv := http.Server{
		Addr: "127.0.0.1:8889",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body.Read(make([]byte, 4096))
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}),
	}
	done := make(chan struct{})
	go func() {
		srv.ListenAndServe()
		done <- struct{}{}
	}()
	trans := &httpTransport{
		traceURL: "http://127.0.0.1:8889/",
		client:   &http.Client{},
	}
	p := newPayload()
	spanList := newSpanList(50)
	for i := 0; i < 100; i++ {
		for j := 0; j < 100; j++ {
			p.push(spanList)
		}
		trans.send(p)
		p.reset()
	}
	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelFunc()
	srv.Shutdown(ctx)
	<-done
}
