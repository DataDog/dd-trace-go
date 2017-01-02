package tracer

import (
	"io/ioutil"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
)

// getTestSpan returns a Span with different fields set
func getTestSpan() *Span {
	return &Span{
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
func getTestTrace(traceN, size int) [][]*Span {
	var traces [][]*Span

	for i := 0; i < traceN; i++ {
		trace := []*Span{}
		for j := 0; j < size; j++ {
			trace = append(trace, getTestSpan())
		}
		traces = append(traces, trace)
	}
	return traces
}

func TestTracesAgentIntegration(t *testing.T) {
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
		transport := newHTTPTransport(defaultHostname, defaultPort)
		response, err := transport.Send(tc.payload)
		assert.Nil(err)
		assert.NotNil(response)
		assert.Equal(200, response.StatusCode)
	}
}

func TestAPIDowngrade(t *testing.T) {
	assert := assert.New(t)
	transport := newHTTPTransport(defaultHostname, defaultPort)
	transport.url = "http://localhost:7777/v0.0/traces"

	// if we get a 404 we should downgrade the API
	traces := getTestTrace(2, 2)
	response, err := transport.Send(traces)
	assert.Nil(err)
	assert.NotNil(response)
	assert.Equal(200, response.StatusCode)
}

func TestEncoderDowngrade(t *testing.T) {
	assert := assert.New(t)
	transport := newHTTPTransport(defaultHostname, defaultPort)
	transport.url = "http://localhost:7777/v0.2/traces"

	// if we get a 415 because of a wrong encoder, we should downgrade the encoder
	traces := getTestTrace(2, 2)
	response, err := transport.Send(traces)
	assert.Nil(err)
	assert.NotNil(response)
	assert.Equal(200, response.StatusCode)
}

func TestTransportHeaders(t *testing.T) {
	assert := assert.New(t)
	transport := newHTTPTransport(defaultHostname, defaultPort)

	// msgpack is the default Header
	contentType := transport.headers["Content-Type"]
	assert.Equal("application/msgpack", contentType)
}

func TestTransportEncoderPool(t *testing.T) {
	assert := assert.New(t)
	transport := newHTTPTransport(defaultHostname, defaultPort)

	// MsgpackEncoder is the default encoder of the pool
	encoder := transport.pool.Borrow()
	assert.Equal("application/msgpack", encoder.ContentType())
}

func TestTransportSwitchEncoder(t *testing.T) {
	assert := assert.New(t)
	transport := newHTTPTransport(defaultHostname, defaultPort)
	transport.changeEncoder(JSON_ENCODER)

	// MsgpackEncoder is the default encoder of the pool
	encoder := transport.pool.Borrow()
	contentType := transport.headers["Content-Type"]
	assert.Equal("application/json", encoder.ContentType())
	assert.Equal("application/json", contentType)
}

type envLookup func(key string) (string, bool)
type env map[string]string

// var fakeEnv = make(map[string]string)

func fakeLookup(fakeEnv env) envLookup {
	return func(key string) (string, bool) {
		s, ok := fakeEnv[key]
		return s, ok
	}
}

// Run TestCustomTarget in a separate process since it messes with global state.
func TestCustomTarget(t *testing.T) {
	if os.Getenv("TEST") == "TestCustomTarget" {
		testCustomTarget(t)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCustomTarget")
	cmd.Env = append(os.Environ(), "TEST=TestCustomTarget")

	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	rawBytes, _ := ioutil.ReadAll(stdout)
	output := string(rawBytes)

	err := cmd.Wait()
	if err != nil {
		e, ok := err.(*exec.ExitError)
		if !ok || !e.Success() {
			t.Fatalf("Subprocess completed with %v\n%s", err, output)
		}
	}
}

func testCustomTarget(t *testing.T) {
	assert := assert.New(t)
	fakeEnv := make(env)
	lookup := fakeLookup(fakeEnv)

	// Test defaults
	URL := "http://localhost:7777/v0.3/traces"
	initTransportVars(lookup)
	assert.Equal(URL, defaultDeliveryURL)

	// Test custom target
	URL = "http://foo:9000/v0.3/traces"
	fakeEnv[agentHostnameVar] = "foo"
	fakeEnv[agentPortVar] = "9000"
	initTransportVars(lookup)
	assert.Equal(URL, defaultDeliveryURL)
}
