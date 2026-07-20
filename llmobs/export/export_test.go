// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/llmobs/export"
)

// fakeTransport records outgoing requests and returns canned responses without
// touching the network, so tests can assert the derived URL, headers and body.
type fakeTransport struct {
	mu        sync.Mutex
	requests  []capturedRequest
	responder func(attempt int, req *http.Request) (int, string)
}

type capturedRequest struct {
	url     string
	headers http.Header
	body    []byte
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err // honor context cancellation like a real transport
	}
	f.mu.Lock()
	attempt := len(f.requests)
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	f.requests = append(f.requests, capturedRequest{
		url:     req.URL.String(),
		headers: req.Header.Clone(),
		body:    body,
	})
	f.mu.Unlock()

	code, respBody := 202, "{}"
	if f.responder != nil {
		code, respBody = f.responder(attempt, req)
	}
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(respBody)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func (f *fakeTransport) captured() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// newClient builds a Datadog-route client wired to fake, defaulting site and API
// key. Extra options are appended after the routing/HTTP defaults.
func newClient(t *testing.T, fake *fakeTransport, mlApp string, opts ...export.ClientOption) *export.Client {
	t.Helper()
	all := append([]export.ClientOption{
		export.WithHTTPClient(&http.Client{Transport: fake}),
		export.WithDatadogIntake("datadoghq.com", "test-key"),
	}, opts...)
	c, err := export.NewClient(mlApp, all...)
	require.NoError(t, err)
	return c
}

// newAgentClient builds an Agent-route (EVP proxy) client wired to fake.
func newAgentClient(t *testing.T, fake *fakeTransport, agentURL, mlApp string, opts ...export.ClientOption) *export.Client {
	t.Helper()
	all := append([]export.ClientOption{
		export.WithHTTPClient(&http.Client{Transport: fake}),
		export.WithAgentURL(agentURL),
	}, opts...)
	c, err := export.NewClient(mlApp, all...)
	require.NoError(t, err)
	return c
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

// firstReq decodes a span request body — a JSON array of push-span-events
// requests — and returns its first element.
func firstReq(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var arr []map[string]any
	require.NoError(t, json.Unmarshal(b, &arr))
	require.NotEmpty(t, arr)
	return arr[0]
}

func ptr[T any](v T) *T { return &v }

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestSpanWireShape_Contract locks the exact JSON keys the LLM Obs intake
// depends on. Because SpanEvent maps to this shape nearly 1:1, an accidental
// rename/add/remove of a wire key would silently break external callers; this
// test fails on any such drift. (A live-intake contract test belongs in an
// integration suite; this guards the shape the SDK emits.)
func TestSpanWireShape_Contract(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithEnv("prod"), export.WithVersion("1.2.3"))

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", ParentID: "p", Kind: export.KindLLM, Name: "chat",
		SessionID: "sess", Service: "svc", Start: time.Unix(0, 1), Duration: 2, Status: export.StatusOK,
		ModelName: "gpt", ModelProvider: "openai", Input: "in", Output: "out",
		Metadata:   map[string]any{"k": "v"},
		Metrics:    &export.SpanMetrics{InputTokens: ptr(int64(1))},
		APMTraceID: "apm-1",
		SpanLinks:  []export.SpanLink{{SpanID: "ls", TraceID: "lt", Attributes: map[string]string{"a": "b"}}},
		Tags:       []string{"x:y"},
	}})
	require.NoError(t, err)

	span := firstReq(t, fake.captured()[0].body)["spans"].([]any)[0].(map[string]any)
	assert.ElementsMatch(t, []string{
		"trace_id", "span_id", "parent_id", "session_id", "name", "service",
		"start_ns", "duration", "status", "meta", "metrics", "tags", "span_links", "_dd",
	}, keysOf(span), "top-level span wire keys drifted")

	meta := span["meta"].(map[string]any)
	assert.ElementsMatch(t, []string{
		"span", "span.kind", "model_name", "model_provider", "input", "output", "metadata",
	}, keysOf(meta), "meta wire keys drifted")
	assert.Equal(t, "llm", meta["span"].(map[string]any)["kind"], "nested meta.span.kind (Trajectory + storage schema)")
	assert.Equal(t, "llm", meta["span.kind"], `flat meta."span.kind" (live-tracer parity)`)

	// service is carried both as the top-level field and a service: tag (the intake
	// reads the tag; the storage schema has no top-level service field).
	tags := make([]string, 0, len(span["tags"].([]any)))
	for _, x := range span["tags"].([]any) {
		tags = append(tags, x.(string))
	}
	assert.Contains(t, tags, "service:svc")

	dd := span["_dd"].(map[string]any)
	assert.ElementsMatch(t, []string{"span_id", "trace_id", "apm_trace_id"}, keysOf(dd), "_dd wire keys drifted")

	// The intake envelope itself.
	env := firstReq(t, fake.captured()[0].body)
	assert.ElementsMatch(t, []string{"_dd.stage", "_dd.tracer_version", "event_type", "spans"}, keysOf(env), "envelope wire keys drifted")
}

func TestSubmitSpans_WireShapeAndAuth(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithService("svc"), export.WithEnv("prod"), export.WithVersion("1.2.3"))

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID:    "111",
		SpanID:     "222",
		SessionID:  "sess",
		Kind:       export.KindLLM,
		Name:       "chat",
		Start:      time.Unix(0, 1000),
		Duration:   500,
		Input:      "hello <b>",
		Output:     "hi",
		Metrics:    &export.SpanMetrics{InputTokens: ptr(int64(10))},
		Tags:       []string{"ml_app:myapp"},
		SpanLinks:  []export.SpanLink{{SpanID: "999", TraceID: "888"}},
		APMTraceID: "aabbccdd",
	}})
	require.NoError(t, err)
	require.Zero(t, res.Failed)
	require.Equal(t, 1, res.Sent)
	require.Len(t, res.Requests, 1)
	assert.Equal(t, 202, res.Requests[0].StatusCode)
	assert.Equal(t, 1, res.Requests[0].Attempts)
	assert.Equal(t, 1, res.Requests[0].Count)

	reqs := fake.captured()
	require.Len(t, reqs, 1)
	assert.Equal(t, "https://llmobs-intake.datadoghq.com/api/v2/llmobs", reqs[0].url)
	assert.Equal(t, "test-key", reqs[0].headers.Get("DD-API-KEY"))
	assert.Equal(t, "application/json", reqs[0].headers.Get("Content-Type"))
	assert.Empty(t, reqs[0].headers.Get("X-Datadog-EVP-Subdomain"))

	// The /api/v2/llmobs body must be a JSON array of push-span-events requests.
	var reqArr []map[string]any
	require.NoError(t, json.Unmarshal(reqs[0].body, &reqArr))
	require.Len(t, reqArr, 1)
	body := reqArr[0]
	assert.Equal(t, "raw", body["_dd.stage"])
	assert.Equal(t, "span", body["event_type"])
	assert.NotEmpty(t, body["_dd.tracer_version"])

	spans := body["spans"].([]any)
	require.Len(t, spans, 1)
	span := spans[0].(map[string]any)
	// IDs are strings, preserved verbatim.
	assert.Equal(t, "111", span["trace_id"])
	assert.Equal(t, "222", span["span_id"])
	assert.Equal(t, "undefined", span["parent_id"]) // empty normalized
	assert.Equal(t, "svc", span["service"])
	assert.Equal(t, "chat", span["name"])
	assert.Equal(t, "ok", span["status"])

	meta := span["meta"].(map[string]any)
	assert.Equal(t, "llm", meta["span"].(map[string]any)["kind"])         // nested meta.span.kind (Trajectory + intake schema)
	assert.Equal(t, "llm", meta["span.kind"])                             // flat key (live-tracer parity)
	assert.Equal(t, "hello <b>", meta["input"].(map[string]any)["value"]) // not HTML-escaped
	assert.Equal(t, "hi", meta["output"].(map[string]any)["value"])

	dd := span["_dd"].(map[string]any)
	assert.Equal(t, "222", dd["span_id"])
	assert.Equal(t, "111", dd["trace_id"])
	assert.Equal(t, "aabbccdd", dd["apm_trace_id"])

	link := span["span_links"].([]any)[0].(map[string]any)
	assert.Equal(t, "999", link["span_id"]) // string span-link IDs
	assert.Equal(t, "888", link["trace_id"])

	tags := span["tags"].([]any)
	assert.Contains(t, tags, "ml_app:myapp")
	assert.Contains(t, tags, "env:prod")
	assert.Contains(t, tags, "version:1.2.3")
	assert.Contains(t, tags, "service:svc") // service carried as a tag (intake reads it there)
}

func TestSubmitSpans_Chunking(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithSpanBatchSize(50))

	events := make([]export.SpanEvent, 120)
	for i := range events {
		events[i] = export.SpanEvent{TraceID: "t", SpanID: "s", Kind: export.KindLLM}
	}
	res, err := c.SubmitSpans(context.Background(), events)
	require.NoError(t, err)
	require.Len(t, res.Requests, 3)
	assert.Equal(t, 50, res.Requests[0].Count)
	assert.Equal(t, 50, res.Requests[1].Count)
	assert.Equal(t, 20, res.Requests[2].Count)
	assert.Equal(t, 120, res.Sent)
	assert.Len(t, fake.captured(), 3)
}

func TestSubmitSpans_ValidationDropsInvalidRows(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM},
		{TraceID: "", SpanID: "s2", Kind: export.KindLLM}, // missing trace_id
		{TraceID: "t3", SpanID: "", Kind: export.KindLLM}, // missing span_id
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 2)
	assert.Equal(t, 2, res.Dropped)
	assert.Equal(t, 1, res.Sent)
	assert.Equal(t, 1, res.ValidationErrors[0].Index)
	assert.Equal(t, 2, res.ValidationErrors[1].Index)

	reqs := fake.captured()
	require.Len(t, reqs, 1)
	spans := firstReq(t, reqs[0].body)["spans"].([]any)
	assert.Len(t, spans, 1) // only the valid row was sent
}

func TestSubmitSpans_SizeGuardTruncatesIO(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithMaxSpanPayloadBytes(256))

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM,
		Input:  strings.Repeat("x", 10000),
		Output: strings.Repeat("y", 10000),
	}})
	require.NoError(t, err)
	require.Len(t, res.Requests, 1)

	span := firstReq(t, fake.captured()[0].body)["spans"].([]any)[0].(map[string]any)
	meta := span["meta"].(map[string]any)
	assert.Equal(t, "[dropped: payload too large]", meta["input"].(map[string]any)["value"])
	assert.Equal(t, "[dropped: payload too large]", meta["output"].(map[string]any)["value"])
	assert.Contains(t, span["collection_errors"].([]any), "dropped_io")
}

func TestSubmitSpans_SplitsOversizedBatchInsteadOfDroppingIO(t *testing.T) {
	fake := &fakeTransport{}
	// Two spans that each fit but together exceed the limit: the batch must be
	// split into two requests with input/output preserved (no dropped_io).
	c := newClient(t, fake, "test-app", export.WithMaxSpanPayloadBytes(3000))

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM, Input: strings.Repeat("x", 1500)},
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM, Input: strings.Repeat("y", 1500)},
	})
	require.NoError(t, err)
	require.Len(t, res.Requests, 2) // bisected: one span per request
	assert.Equal(t, 1, res.Requests[0].Count)
	assert.Equal(t, 1, res.Requests[1].Count)

	for _, req := range fake.captured() {
		span := firstReq(t, req.body)["spans"].([]any)[0].(map[string]any)
		assert.NotContains(t, span, "collection_errors") // I/O preserved, not dropped
		assert.NotEmpty(t, span["meta"].(map[string]any)["input"])
	}
}

func TestSubmitSpans_StampsMLAppFromClient(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "my-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM},                                    // no ml_app tag -> stamped
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM, Tags: []string{"ml_app:override"}}, // caller wins
		{TraceID: "t3", SpanID: "s3", Kind: export.KindLLM, Tags: []string{"ml_app:"}},         // empty tag -> treated as absent
	})
	require.NoError(t, err)

	spans := firstReq(t, fake.captured()[0].body)["spans"].([]any)
	tagsOf := func(i int) []any { return spans[i].(map[string]any)["tags"].([]any) }
	assert.Contains(t, tagsOf(0), "ml_app:my-app")
	assert.Contains(t, tagsOf(1), "ml_app:override")
	assert.NotContains(t, tagsOf(1), "ml_app:my-app")
	// An empty "ml_app:" tag must not suppress the required default: it is dropped
	// and replaced with the configured value, leaving no bare empty tag behind.
	assert.Contains(t, tagsOf(2), "ml_app:my-app")
	assert.NotContains(t, tagsOf(2), "ml_app:")
}

func TestSubmitSpans_AgentRoute(t *testing.T) {
	fake := &fakeTransport{}
	c := newAgentClient(t, fake, "http://localhost:8126", "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
	require.NoError(t, err)

	reqs := fake.captured()
	require.Len(t, reqs, 1)
	assert.Equal(t, "http://localhost:8126/evp_proxy/v2/api/v2/llmobs", reqs[0].url)
	assert.Equal(t, "llmobs-intake", reqs[0].headers.Get("X-Datadog-EVP-Subdomain"))
	assert.Empty(t, reqs[0].headers.Get("DD-API-KEY")) // no Datadog auth on agent route
}

func TestSubmitSpans_WithCallServiceOverride(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithService("default-svc"))

	_, err := c.SubmitSpans(context.Background(),
		[]export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}},
		export.WithCallService("call-svc"),
	)
	require.NoError(t, err)

	span := firstReq(t, fake.captured()[0].body)["spans"].([]any)[0].(map[string]any)
	assert.Equal(t, "call-svc", span["service"]) // per-call override wins over the client default
	tags := make([]string, 0, len(span["tags"].([]any)))
	for _, x := range span["tags"].([]any) {
		tags = append(tags, x.(string))
	}
	assert.Contains(t, tags, "service:call-svc")
	assert.NotContains(t, tags, "service:default-svc")
}

func TestSubmitSpans_RetryTransient(t *testing.T) {
	fake := &fakeTransport{responder: func(int, *http.Request) (int, string) { return 500, "boom" }}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
	require.Error(t, err)
	require.Equal(t, 1, res.Failed) // the one event's request failed
	require.Zero(t, res.Sent)
	require.Len(t, res.Requests, 1)
	assert.Greater(t, res.Requests[0].Attempts, 1) // retried
	assert.True(t, res.Requests[0].Retriable)
	assert.Equal(t, 500, res.Requests[0].StatusCode)
	assert.Error(t, res.Requests[0].Err)
}

func TestSubmitSpans_PermanentError(t *testing.T) {
	fake := &fakeTransport{responder: func(int, *http.Request) (int, string) { return 400, "bad" }}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
	require.Error(t, err)
	require.Len(t, res.Requests, 1)
	assert.Equal(t, 1, res.Requests[0].Attempts) // not retried
	assert.False(t, res.Requests[0].Retriable)
	assert.Equal(t, 400, res.Requests[0].StatusCode)
}

func TestSubmitEvaluations_WireShapeVariants(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "defaultapp")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t1", Label: "quality", CategoricalValue: ptr("good"), Timestamp: time.UnixMilli(123)},
		{SpanID: "s2", TraceID: "t2", Label: "score", ScoreValue: ptr(0.9)},
		{SpanID: "s3", TraceID: "t3", Label: "ok", BooleanValue: ptr(true)},
		{SpanID: "s4", TraceID: "t4", Label: "struct", JSONValue: map[string]any{"k": "v"}, MetricType: export.MetricTypeCategorical},
		{TagKey: "session_id", TagValue: "abc", Label: "tagjoin", ScoreValue: ptr(1.0)},
	})
	require.NoError(t, err)
	require.Zero(t, res.Failed)
	require.Equal(t, 5, res.Sent)
	require.Len(t, res.Requests, 1)

	reqs := fake.captured()
	require.Len(t, reqs, 1)
	assert.Equal(t, "https://api.datadoghq.com/api/intake/llm-obs/v2/eval-metric", reqs[0].url)

	body := decode(t, reqs[0].body)
	data := body["data"].(map[string]any)
	assert.Equal(t, "evaluation_metric", data["type"])
	metrics := data["attributes"].(map[string]any)["metrics"].([]any)
	require.Len(t, metrics, 5)

	m0 := metrics[0].(map[string]any)
	assert.Equal(t, "categorical", m0["metric_type"])
	assert.Equal(t, "good", m0["categorical_value"])
	assert.Equal(t, "defaultapp", m0["ml_app"]) // default applied
	assert.Equal(t, float64(123), m0["timestamp_ms"])
	join := m0["join_on"].(map[string]any)["span"].(map[string]any)
	assert.Equal(t, "s1", join["span_id"])
	assert.Equal(t, "t1", join["trace_id"])

	m1 := metrics[1].(map[string]any)
	assert.Equal(t, "score", m1["metric_type"])
	m3 := metrics[3].(map[string]any)
	assert.Equal(t, "categorical", m3["metric_type"])
	assert.NotNil(t, m3["json_value"])
	m4 := metrics[4].(map[string]any)
	tagJoin := m4["join_on"].(map[string]any)["tag"].(map[string]any)
	assert.Equal(t, "session_id", tagJoin["key"])
	assert.Equal(t, "abc", tagJoin["value"])
}

func TestSubmitEvaluations_StampsTracerVersion(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{{
		SpanID: "s", TraceID: "t", Label: "q", ScoreValue: ptr(1.0),
		Tags: []string{"team:ml", "ddtrace.version:bogus"},
	}})
	require.NoError(t, err)

	m := decode(t, fake.captured()[0].body)["data"].(map[string]any)["attributes"].(map[string]any)["metrics"].([]any)[0].(map[string]any)
	tags := make([]string, 0, len(m["tags"].([]any)))
	for _, x := range m["tags"].([]any) {
		tags = append(tags, x.(string))
	}
	assert.Contains(t, tags, "team:ml")                  // caller tag preserved
	assert.NotContains(t, tags, "ddtrace.version:bogus") // stale value stripped
	hasVer := false
	for _, tg := range tags {
		if strings.HasPrefix(tg, "ddtrace.version:") {
			hasVer = true
		}
	}
	assert.True(t, hasVer, "SDK ddtrace.version stamped")
}

func TestSubmitEvaluations_Validation(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{Label: "no-join", ScoreValue: ptr(1.0)},                                                                               // missing join
		{SpanID: "s", TraceID: "t", TagKey: "k", TagValue: "v", Label: "both", ScoreValue: ptr(1.0)},                           // both joins
		{SpanID: "s", TraceID: "t", Label: "novalue"},                                                                          // zero values
		{SpanID: "s", TraceID: "t", Label: "twovalues", ScoreValue: ptr(1.0), BooleanValue: ptr(true)},                         // two values
		{SpanID: "s", TraceID: "t", Label: "jsonnotype", JSONValue: map[string]any{"k": "v"}},                                  // json without metric type
		{SpanID: "s", TraceID: "", Label: "partial", ScoreValue: ptr(1.0)},                                                     // incomplete span join
		{SpanID: "s", TraceID: "t", Label: "badtype", MetricType: export.MetricType("scores"), ScoreValue: ptr(1.0)},           // invalid metric type (typo)
		{SpanID: "s", TraceID: "t", Label: "mismatch", MetricType: export.MetricTypeScore, CategoricalValue: ptr("x")},         // type/value mismatch
		{SpanID: "s", TraceID: "t", Label: "emptyjson", MetricType: export.MetricTypeCategorical, JSONValue: map[string]any{}}, // empty json value
	})
	require.NoError(t, err)
	assert.Len(t, res.ValidationErrors, 9)
	assert.Equal(t, 9, res.Dropped)
	assert.Empty(t, fake.captured()) // nothing valid was sent
}

func TestSubmit_EmptyInput(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, res.Requests)

	res, err = c.SubmitEvaluations(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, res.Requests)
	assert.Empty(t, fake.captured())
}

func TestNewClient_RequiresAPIKeyForDirectRoute(t *testing.T) {
	_, err := export.NewClient("app", export.WithDatadogIntake("datadoghq.com", ""))
	assert.Error(t, err)
}

func TestNewClient_RequiresMLApp(t *testing.T) {
	_, err := export.NewClient("", export.WithDatadogIntake("datadoghq.com", "k"))
	assert.Error(t, err) // ml_app is required for LLM Obs data
}

func TestNewClient_RequiresExactlyOneRoute(t *testing.T) {
	// No route selected.
	_, err := export.NewClient("app")
	assert.Error(t, err)

	// Both routes selected.
	_, err = export.NewClient("app",
		export.WithDatadogIntake("datadoghq.com", "k"),
		export.WithAgentURL("http://localhost:8126"),
	)
	assert.Error(t, err)
}

// TestSubmitSpans_ConcurrentDoesNotMutateCaller guards against the client
// mutating the caller's Tags backing array (and racing) while stamping env/version.
// Run with -race.
func TestSubmitSpans_ConcurrentDoesNotMutateCaller(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithEnv("prod"), export.WithVersion("1.0"))

	// Spare-capacity slice shared across the exported events.
	shared := make([]string, 1, 8)
	shared[0] = "ml_app:x"
	ev := export.SpanEvent{TraceID: "t", SpanID: "s", Kind: export.KindLLM, Tags: shared}

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{ev})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	// The caller's slice must be untouched (still just its one tag).
	assert.Equal(t, []string{"ml_app:x"}, shared)
}

func TestSubmitSpans_AgentRouteTrimsTrailingSlash(t *testing.T) {
	fake := &fakeTransport{}
	c := newAgentClient(t, fake, "http://localhost:8126/", "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8126/evp_proxy/v2/api/v2/llmobs", fake.captured()[0].url)
}

func TestSubmitSpans_ContextCanceledStopsPromptly(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := c.SubmitSpans(ctx, []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	// A canceled context stops the export before POSTing, so it neither sends nor
	// accumulates artificial request failures.
	assert.Empty(t, res.Requests)
	assert.Empty(t, fake.captured())
}

func TestNewClient_RejectsBadAgentURLScheme(t *testing.T) {
	for _, bad := range []string{"htt://localhost:8126", "ftp://host", "localhost:8126"} {
		_, err := export.NewClient("app", export.WithAgentURL(bad))
		assert.Error(t, err, "agent URL %q should be rejected", bad)
	}
}

func TestSubmitEvaluations_RejectsNonFiniteScore(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t1", Label: "nan", ScoreValue: ptr(math.NaN())},
		{SpanID: "s2", TraceID: "t2", Label: "inf", ScoreValue: ptr(math.Inf(1))},
		{SpanID: "s3", TraceID: "t3", Label: "ok", ScoreValue: ptr(0.5)},
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 2) // NaN and Inf rejected as rows
	assert.Equal(t, 0, res.ValidationErrors[0].Index)
	assert.Equal(t, 1, res.ValidationErrors[1].Index)

	// The one valid metric was still sent (a bad row does not poison the chunk).
	metrics := decode(t, fake.captured()[0].body)["data"].(map[string]any)["attributes"].(map[string]any)["metrics"].([]any)
	require.Len(t, metrics, 1)
}

func TestSubmitSpans_StampsSessionIDTag(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t", SpanID: "s", Kind: export.KindLLM, SessionID: "sess-1"},
	})
	require.NoError(t, err)

	span := firstReq(t, fake.captured()[0].body)["spans"].([]any)[0].(map[string]any)
	assert.Contains(t, span["tags"].([]any), "session_id:sess-1") // tag-join parity with the live path
	assert.Equal(t, "sess-1", span["session_id"])                 // top-level still set
}

func TestSubmitSpans_DropsMissingKind(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM}, // valid
		{TraceID: "t2", SpanID: "s2"},                       // missing kind -> dropped
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1)
	assert.Equal(t, 1, res.ValidationErrors[0].Index)

	span := firstReq(t, fake.captured()[0].body)["spans"].([]any)
	require.Len(t, span, 1) // only the valid span was sent
}

func TestSubmitSpans_RejectsNonFiniteMetric(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM, Metrics: &export.SpanMetrics{EstimatedTotalCost: ptr(math.Inf(1))}},
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM}, // valid
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1) // the non-finite cost row is dropped, not fatal
	assert.Equal(t, 0, res.ValidationErrors[0].Index)

	span := firstReq(t, fake.captured()[0].body)["spans"].([]any)
	require.Len(t, span, 1) // the valid span still went out
}

func TestSubmitSpans_SessionIDOverridesStaleTag(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM, SessionID: "new",
		Tags: []string{"session_id:old", "team:ml"},
	}})
	require.NoError(t, err)

	span := firstReq(t, fake.captured()[0].body)["spans"].([]any)[0].(map[string]any)
	tags := make([]string, 0, len(span["tags"].([]any)))
	for _, x := range span["tags"].([]any) {
		tags = append(tags, x.(string))
	}
	assert.Contains(t, tags, "session_id:new")    // structured SessionID is source of truth
	assert.NotContains(t, tags, "session_id:old") // stale caller tag replaced
	assert.Contains(t, tags, "team:ml")           // unrelated tag preserved
	assert.Equal(t, "new", span["session_id"])    // top-level agrees with the tag
}

func TestSubmitSpans_ServiceTagReplacesStale(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithService("svc"))

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM,
		Tags: []string{"service:stale", "team:ml"},
	}})
	require.NoError(t, err)

	span := firstReq(t, fake.captured()[0].body)["spans"].([]any)[0].(map[string]any)
	tags := make([]string, 0, len(span["tags"].([]any)))
	for _, x := range span["tags"].([]any) {
		tags = append(tags, x.(string))
	}
	assert.Contains(t, tags, "service:svc")      // resolved service is authoritative
	assert.NotContains(t, tags, "service:stale") // stale caller tag replaced
	assert.Contains(t, tags, "team:ml")          // unrelated tag preserved
	assert.Equal(t, "svc", span["service"])      // top-level field agrees with the tag
}

func TestSubmitSpans_MetricsPreservesExtraAndStandardKeys(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM,
		Metrics: &export.SpanMetrics{
			InputTokens:            ptr(int64(10)),
			BillableCharacterCount: ptr(int64(42)),
			TimeToFirstToken:       ptr(0.25),
			Extra: map[string]float64{
				"custom_metric": 7,
				"input_tokens":  999, // collides with the named field -> named wins
			},
		},
	}})
	require.NoError(t, err)

	m := firstReq(t, fake.captured()[0].body)["spans"].([]any)[0].(map[string]any)["metrics"].(map[string]any)
	assert.Equal(t, float64(10), m["input_tokens"])             // named field wins over Extra
	assert.Equal(t, float64(42), m["billable_character_count"]) // newly-added standard key carried
	assert.Equal(t, 0.25, m["time_to_first_token"])
	assert.Equal(t, float64(7), m["custom_metric"]) // arbitrary reconstructed key not dropped
}

func TestSubmitEvaluations_RejectsUnmarshalableJSON(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t1", Label: "bad", MetricType: export.MetricTypeCategorical, JSONValue: map[string]any{"x": math.Inf(1)}},
		{SpanID: "s2", TraceID: "t2", Label: "ok", ScoreValue: ptr(0.5)},
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1) // unmarshalable json_value dropped as a row
	assert.Equal(t, 0, res.ValidationErrors[0].Index)

	metrics := decode(t, fake.captured()[0].body)["data"].(map[string]any)["attributes"].(map[string]any)["metrics"].([]any)
	require.Len(t, metrics, 1) // the valid metric still went out
}
