package agenttest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type TraceHandler func(io.Reader) []*Span

type Info struct {
	Rates map[string]float64 `json:"rate_by_service"`
}

func newInfo() *Info {
	return &Info{
		Rates: make(map[string]float64),
	}
}

func (i *Info) RateByService(service, env string, rate float64) {
	k := fmt.Sprintf("service:%s,env:%s", service, env)
	i.Rates[k] = rate
}

type Agent interface {
	Info() *Info
	HandleTraces(string, TraceHandler)

	Start(testing.TB) error
	Addr() string
	// Transport returns an http.RoundTripper that dispatches requests directly
	// to this agent's handler in-process, bypassing OS networking. Using this
	// transport eliminates TCP overhead and makes test flushes deterministic.
	Transport() http.RoundTripper

	FindSpan(...*SpanMatch) *Span
	RequireSpan(testing.TB, ...*SpanMatch) *Span
	CountSpans() int
}

type agent struct {
	mu sync.Mutex

	mux       *http.ServeMux
	addr      string
	endpoints []string

	info  *Info
	spans []*Span
}

func New() Agent {
	mux := http.NewServeMux()
	a := &agent{
		mux:  mux,
		info: newInfo(),
	}
	mux.HandleFunc("/info", a.handleInfo)
	return a
}

func (a *agent) Info() *Info {
	return a.info
}

func (a *agent) HandleTraces(pattern string, handler TraceHandler) {
	a.endpoints = append(a.endpoints, pattern)
	a.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		spans := handler(r.Body)
		a.mu.Lock()
		a.spans = append(a.spans, spans...)
		a.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a.info)
	})
}

func (a *agent) handleInfo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"endpoints": a.endpoints, "client_drop_p0s": true})
}

func (a *agent) Addr() string {
	return a.addr
}

func (a *agent) Transport() http.RoundTripper {
	return &inProcessRoundTripper{handler: a.mux}
}

type inProcessRoundTripper struct{ handler http.Handler }

func (t *inProcessRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, req)
	return w.Result(), nil
}

func (a *agent) Start(_ testing.TB) error {
	a.addr = "agenttest.invalid:0"
	return nil
}

func (a *agent) FindSpan(matchers ...*SpanMatch) *Span {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.spans {
		match := true
		for _, m := range matchers {
			if !m.Matches(s) {
				match = false
				break
			}
		}
		if match {
			return s
		}
	}
	return nil
}

func (a *agent) RequireSpan(t testing.TB, matchers ...*SpanMatch) *Span {
	t.Helper()
	s := a.FindSpan(matchers...)
	if s == nil {
		t.Fatal("no span found matching the given conditions")
	}
	return s
}

func (a *agent) CountSpans() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.spans)
}
