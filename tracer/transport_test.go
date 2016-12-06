package tracer

import (
	"testing"
	"time"

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
		Start:    time.Now().UnixNano(),
		Duration: time.Second.Nanoseconds(),
		Meta:     map[string]string{"http.host": "192.168.0.1"},
		Metrics:  map[string]float64{"http.monitor": 41.99},
	}
}

// getTestTrace returns a []Span that is composed by ``size`` number of spans
func getTestTrace(size int) []*Span {
	spans := []*Span{}

	for i := 0; i < size; i++ {
		spans = append(spans, getTestSpan())
	}
	return spans
}

func TestTracesAgentIntegration(t *testing.T) {
	assert := assert.New(t)

	testCases := []struct {
		payload []*Span
	}{
		{getTestTrace(1)},
		{getTestTrace(10)},
	}

	for _, tc := range testCases {
		transport := newHTTPTransport("http://localhost:7777/v0.1/spans")
		response, err := transport.Send(tc.payload)
		assert.Nil(err)
		assert.Equal(200, response.StatusCode)
	}
}
