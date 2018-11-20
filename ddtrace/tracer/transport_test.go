package tracer

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

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
		transport := newHTTPTransport(defaultAddress)
		p, err := encode(tc.payload)
		assert.NoError(err)
		err = transport.send(p)
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

func TestTransportResponseError(t *testing.T) {
	assert := assert.New(t)
	ln, err := net.Listen("tcp4", ":0")
	assert.Nil(err)
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(strings.Repeat("X", 1002)))
	}))
	defer ln.Close()
	addr := ln.Addr().String()
	log.Println(addr)
	transport := newHTTPTransport(addr)
	err = transport.send(newPayload())
	want := fmt.Sprintf("%s (Status: Bad Request)", strings.Repeat("X", 1000))
	assert.Equal(want, err.Error())
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
		transport := newHTTPTransport(host)
		p, err := encode(tc.payload)
		assert.NoError(err)
		err = transport.send(p)
		assert.NoError(err)
	}

	receiver.Close()
}
