package test

import (
	"github.com/DataDog/dd-trace-go/tracer"
	"net/http"
)

// GetTestTracer returns a tracer with a dummy Transport that serves
// for contribs testing
func GetTestTracer() (*tracer.Tracer, *DummyTransport) {
	transport := &DummyTransport{}
	tracer := tracer.NewTracerTransport(transport)
	return tracer, transport
}

// DummyTransport is a transport that just buffers spans and encoding
type DummyTransport struct {
	traces   [][]*tracer.Span
	services map[string]tracer.Service
}

func (t *DummyTransport) SendTraces(traces [][]*tracer.Span) (*http.Response, error) {
	t.traces = append(t.traces, traces...)
	return nil, nil
}

func (t *DummyTransport) SendServices(services map[string]tracer.Service) (*http.Response, error) {
	t.services = services
	return nil, nil
}

func (t *DummyTransport) Traces() [][]*tracer.Span {
	traces := t.traces
	t.traces = nil
	return traces
}

func (t *DummyTransport) SetHeader(key, value string) {}
