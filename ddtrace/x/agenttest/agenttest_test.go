// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenttest

import (
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// makeSpan is a helper that builds a Span with the Tags map pre-populated to
// match the layout produced by toAgentSpan in ddtrace/tracer/tracertest.go.
func makeSpan(name, service, resource, spanType string, tags map[string]any) *Span {
	s := &Span{
		Operation: name,
		Service:   service,
		Resource:  resource,
		Type:      spanType,
		Meta:      make(map[string]string),
		Metrics:   make(map[string]float64),
		Tags:      make(map[string]any),
	}
	// Populate top-level attrs into Tags (mirrors toAgentSpan).
	s.Tags["name"] = name
	s.Tags["service"] = service
	s.Tags["resource"] = resource
	s.Tags["type"] = spanType
	for k, v := range tags {
		s.Tags[k] = v
	}
	return s
}

// TestSpanMatch_BasicConditions exercises each SpanMatch builder method.
func TestSpanMatch_BasicConditions(t *testing.T) {
	s := makeSpan("http.request", "my-svc", "/api/v1", "web", map[string]any{
		"http.status_code": "200",
		"score":            float64(42),
	})
	s.ParentID = 99

	tests := []struct {
		name    string
		matcher *SpanMatch
		want    bool
	}{
		{"operation match", With().Operation("http.request"), true},
		{"operation mismatch", With().Operation("grpc.client"), false},
		{"service match", With().Service("my-svc"), true},
		{"service mismatch", With().Service("other-svc"), false},
		{"resource match", With().Resource("/api/v1"), true},
		{"resource mismatch", With().Resource("/not/here"), false},
		{"type match", With().Type("web"), true},
		{"type mismatch", With().Type("db"), false},
		{"tag string match", With().Tag("http.status_code", "200"), true},
		{"tag string mismatch", With().Tag("http.status_code", "500"), false},
		{"tag float match", With().Tag("score", float64(42)), true},
		{"tag absent", With().Tag("nonexistent", "x"), false},
		{"parent match", With().ParentOf(99), true},
		{"parent mismatch", With().ParentOf(0), false},
		{"custom condition true", With().Condition("always true", func(*Span) bool { return true }), true},
		{"custom condition false", With().Condition("always false", func(*Span) bool { return false }), false},
		{"all conditions pass", With().Service("my-svc").Operation("http.request").Tag("score", float64(42)), true},
		{"one condition fails", With().Service("my-svc").Operation("http.request").Tag("score", float64(0)), false},
		{"empty matcher always passes", With(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.matcher.Matches(s)
			if got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSpanMatch_FailedConditions verifies that FailedConditions returns only
// the descriptions of predicates that failed.
func TestSpanMatch_FailedConditions(t *testing.T) {
	s := makeSpan("http.request", "my-svc", "/", "web", nil)

	m := With().Service("wrong-svc").Operation("http.request").Resource("wrong-resource")
	failed := m.FailedConditions(s)

	if len(failed) != 2 {
		t.Fatalf("expected 2 failed conditions, got %d: %v", len(failed), failed)
	}
	for _, f := range failed {
		if !strings.Contains(f, "wrong-svc") && !strings.Contains(f, "wrong-resource") {
			t.Errorf("unexpected failed condition description: %q", f)
		}
	}
}

// TestSpanMatch_FailedConditions_Empty verifies no failures when all match.
func TestSpanMatch_FailedConditions_Empty(t *testing.T) {
	s := makeSpan("op", "svc", "res", "web", nil)
	failed := With().Operation("op").FailedConditions(s)
	if len(failed) != 0 {
		t.Errorf("expected no failures, got: %v", failed)
	}
}

// TestAgent_FindSpan_NoMatch returns nil when no span matches.
func TestAgent_FindSpan_NoMatch(t *testing.T) {
	a := New().(*agent)
	a.spans = []*Span{makeSpan("http.request", "svc", "/", "web", nil)}

	got := a.FindSpan(With().Operation("grpc.client"))
	if got != nil {
		t.Errorf("expected nil, got span with operation %q", got.Operation)
	}
}

// TestAgent_FindSpan_FirstMatch returns the first matching span.
func TestAgent_FindSpan_FirstMatch(t *testing.T) {
	a := New().(*agent)
	s1 := makeSpan("http.request", "svc", "/a", "web", nil)
	s2 := makeSpan("http.request", "svc", "/b", "web", nil)
	a.spans = []*Span{s1, s2}

	got := a.FindSpan(With().Operation("http.request"))
	if got != s1 {
		t.Errorf("expected first span, got %p (s2=%p)", got, s2)
	}
}

// TestAgent_FindSpan_MultiCondition exercises the AND-semantics of multiple matchers.
func TestAgent_FindSpan_MultiCondition(t *testing.T) {
	a := New().(*agent)
	s1 := makeSpan("http.request", "svc-a", "/", "web", nil)
	s2 := makeSpan("http.request", "svc-b", "/", "web", nil)
	a.spans = []*Span{s1, s2}

	got := a.FindSpan(With().Operation("http.request").Service("svc-b"))
	if got != s2 {
		t.Errorf("expected svc-b span, got %v", got)
	}
}

// TestAgent_RequireSpan_Found passes when a matching span exists.
func TestAgent_RequireSpan_Found(t *testing.T) {
	a := New().(*agent)
	s := makeSpan("db.query", "postgres", "SELECT 1", "sql", nil)
	a.spans = []*Span{s}

	got := a.RequireSpan(t, With().Operation("db.query"))
	if got != s {
		t.Errorf("RequireSpan returned unexpected span")
	}
}

// failingT is a minimal testing.TB whose Fatalf records the failure and calls
// runtime.Goexit() to terminate the goroutine — mirroring the real testing.T
// behaviour without needing a full test runner.
type failingT struct {
	testing.TB
	mu     sync.Mutex
	failed bool
}

func (f *failingT) Helper() {}
func (f *failingT) Fatalf(_ string, _ ...any) {
	f.mu.Lock()
	f.failed = true
	f.mu.Unlock()
	runtime.Goexit()
}
func (f *failingT) Failed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failed
}

// TestAgent_RequireSpan_NotFound verifies that RequireSpan marks the test as
// failed (via Fatalf) when no collected span matches the conditions.
func TestAgent_RequireSpan_NotFound(t *testing.T) {
	ft := &failingT{}
	a := New().(*agent)
	a.spans = []*Span{makeSpan("http.request", "svc", "/", "web", nil)}

	// Run RequireSpan in its own goroutine so that runtime.Goexit() (called by
	// Fatalf) only terminates that goroutine, not the test goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.RequireSpan(ft, With().Operation("nonexistent"))
	}()
	<-done

	if !ft.Failed() {
		t.Error("expected RequireSpan to mark the test as failed when no span matches")
	}
}

// TestAgent_CountSpans reflects the number of collected spans.
func TestAgent_CountSpans(t *testing.T) {
	a := New().(*agent)
	if n := a.CountSpans(); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
	a.spans = append(a.spans, makeSpan("op", "svc", "r", "web", nil))
	a.spans = append(a.spans, makeSpan("op", "svc", "r", "web", nil))
	if n := a.CountSpans(); n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}

// TestAgent_HandleTraces_InProcess exercises the full handler round-trip via the
// in-process RoundTripper.
func TestAgent_HandleTraces_InProcess(t *testing.T) {
	called := false
	captured := []*Span{}
	handler := func(r io.Reader) []*Span {
		called = true
		s := &Span{Operation: "decoded-op", Service: "svc",
			Tags: map[string]any{"name": "decoded-op"},
			Meta: map[string]string{}, Metrics: map[string]float64{}}
		captured = append(captured, s)
		return captured
	}

	a := New()
	a.HandleTraces("/v0.4/traces", handler)
	if err := a.Start(t); err != nil {
		t.Fatal(err)
	}

	rt := a.Transport()
	body := strings.NewReader(`{}`)
	req, _ := newTestRequest("POST", "http://"+a.Addr()+"/v0.4/traces", body)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if !called {
		t.Error("trace handler was not called")
	}
	if a.CountSpans() != 1 {
		t.Errorf("expected 1 span, got %d", a.CountSpans())
	}
}

// newTestRequest builds a minimal http.Request for round-trip testing.
func newTestRequest(method, url string, body io.Reader) (*http.Request, error) {
	return http.NewRequest(method, url, body)
}
