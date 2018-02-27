package tracer

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

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

func getTestServices() map[string]service {
	return map[string]service{
		"svc1": service{Name: "scv1", App: "a", AppType: "b"},
		"svc2": service{Name: "scv2", App: "c", AppType: "d"},
	}
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
		response, err := transport.sendTraces(tc.payload)
		assert.NoError(err)
		assert.NotNil(response)
		assert.Equal(200, response.StatusCode)
	}
}

func TestAPIDowngrade(t *testing.T) {
	assert := assert.New(t)
	transport := newHTTPTransport(defaultAddress)
	transport.traceURL = "http://localhost:8126/v0.0/traces"

	// if we get a 404 we should downgrade the API
	traces := getTestTrace(2, 2)
	response, err := transport.sendTraces(traces)
	assert.NoError(err)
	assert.NotNil(response)
	assert.Equal(200, response.StatusCode)
}

func TestEncoderDowngrade(t *testing.T) {
	assert := assert.New(t)
	transport := newHTTPTransport(defaultAddress)
	transport.traceURL = "http://localhost:8126/v0.2/traces"

	// if we get a 415 because of a wrong encoder, we should downgrade the encoder
	traces := getTestTrace(2, 2)
	response, err := transport.sendTraces(traces)
	assert.NoError(err)
	assert.NotNil(response)
	assert.Equal(200, response.StatusCode)
}

func TestTransportServices(t *testing.T) {
	assert := assert.New(t)

	transport := newHTTPTransport(defaultAddress)

	response, err := transport.sendServices(getTestServices())
	assert.NoError(err)
	assert.NotNil(response)
	assert.Equal(200, response.StatusCode)
}

func TestTransportServicesDowngrade_0_0(t *testing.T) {
	assert := assert.New(t)

	transport := newHTTPTransport(defaultAddress)
	transport.serviceURL = "http://localhost:8126/v0.0/services"

	response, err := transport.sendServices(getTestServices())
	assert.NoError(err)
	assert.NotNil(response)
	assert.Equal(200, response.StatusCode)
}

func TestTransportServicesDowngrade_0_2(t *testing.T) {
	assert := assert.New(t)

	transport := newHTTPTransport(defaultAddress)
	transport.serviceURL = "http://localhost:8126/v0.2/services"

	response, err := transport.sendServices(getTestServices())
	assert.NoError(err)
	assert.NotNil(response)
	assert.Equal(200, response.StatusCode)
}

func TestTransportContentType(t *testing.T) {
	assert := assert.New(t)
	transport := newHTTPTransport(defaultAddress)
	assert.Equal("application/msgpack", transport.encoding.contentType())
}

func TestTransportDowngrade(t *testing.T) {
	assert := assert.New(t)
	transport := newHTTPTransport(defaultAddress)
	transport.apiDowngrade()
	assert.Equal("application/json", transport.encoding.contentType())
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
		response, err := transport.sendTraces(tc.payload)
		assert.NoError(err)
		assert.NotNil(response)
		assert.Equal(200, response.StatusCode)
	}

	receiver.Close()
}
