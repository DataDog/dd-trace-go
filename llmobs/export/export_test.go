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
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/llmobs/export"
)

const testAPIKey = "0123456789abcdef0123456789abcdef"

type fakeTransport struct {
	mu         sync.Mutex
	requests   []capturedRequest
	responder  func(attempt int, req *http.Request) (int, string)
	respHeader http.Header
}

type capturedRequest struct {
	url     string
	headers http.Header
	body    []byte
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
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
	header := http.Header{"Content-Type": []string{"application/json"}}
	for k, vs := range f.respHeader {
		for _, v := range vs {
			header.Add(k, v)
		}
	}
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(respBody)),
		Header:     header,
	}, nil
}

type blockingTransport struct {
	once    sync.Once
	entered chan struct{}
}

func (b *blockingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	b.once.Do(func() { close(b.entered) })
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func (f *fakeTransport) captured() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func newClient(t *testing.T, fake *fakeTransport, mlApp string, opts ...export.ClientOption) *export.Client {
	t.Helper()
	all := append([]export.ClientOption{
		export.WithHTTPClient(&http.Client{Transport: fake}),
		export.WithDatadogIntake("datadoghq.com", testAPIKey),
	}, opts...)
	c, err := export.NewClient(mlApp, all...)
	require.NoError(t, err)
	return c
}

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

func firstReq(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var arr []map[string]any
	require.NoError(t, json.Unmarshal(b, &arr))
	require.NotEmpty(t, arr)
	return arr[0]
}

func allSpans(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var arr []map[string]any
	require.NoError(t, json.Unmarshal(b, &arr))
	spans := make([]map[string]any, 0, len(arr))
	for _, env := range arr {
		for _, s := range env["spans"].([]any) {
			spans = append(spans, s.(map[string]any))
		}
	}
	return spans
}

func allMetrics(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	raw := decode(t, b)["data"].(map[string]any)["attributes"].(map[string]any)["metrics"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		out = append(out, m.(map[string]any))
	}
	return out
}

func firstMetric(t *testing.T, b []byte) map[string]any {
	t.Helper()
	metrics := allMetrics(t, b)
	require.NotEmpty(t, metrics)
	return metrics[0]
}

func tagsOf(t *testing.T, span map[string]any) []string {
	t.Helper()
	raw, ok := span["tags"].([]any)
	require.True(t, ok, "span has no tags")
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestSpanWireShape_Contract(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithEnv("prod"), export.WithVersion("1.2.3"))

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", ParentID: "p", Kind: export.KindLLM, Name: "chat",
		SessionID: "sess", Service: "svc", Start: time.Unix(0, 1), Duration: 2, Status: export.StatusOK,
		ModelName: "gpt", ModelProvider: "openai", Input: "in", Output: "out",
		Metadata:   map[string]any{"k": "v"},
		Metrics:    map[string]float64{"input_tokens": 1},
		APMTraceID: "apm-1",
		SpanLinks:  []export.SpanLink{{SpanID: "22", TraceID: "11", Attributes: map[string]string{"a": "b"}}},
		Tags:       []string{"x:y"},
	}})
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.ElementsMatch(t, []string{
		"trace_id", "span_id", "parent_id", "session_id", "name",
		"start_ns", "duration", "status", "meta", "metrics", "tags", "span_links", "_dd",
	}, keysOf(span), "top-level span wire keys drifted")

	meta := span["meta"].(map[string]any)
	assert.ElementsMatch(t, []string{
		"span.kind", "model_name", "model_provider", "input", "output", "metadata",
	}, keysOf(meta), "meta wire keys drifted")
	assert.Equal(t, "llm", meta["span.kind"])
	assert.NotContains(t, span, "service")

	tags := make([]string, 0, len(span["tags"].([]any)))
	for _, x := range span["tags"].([]any) {
		tags = append(tags, x.(string))
	}
	assert.Contains(t, tags, "service:svc")

	dd := span["_dd"].(map[string]any)
	assert.ElementsMatch(t, []string{"span_id", "trace_id", "apm_trace_id"}, keysOf(dd), "_dd wire keys drifted")

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
		Metrics:    map[string]float64{"input_tokens": 10},
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
	assert.Equal(t, []int{0}, res.Requests[0].InputIndices)

	reqs := fake.captured()
	require.Len(t, reqs, 1)
	assert.Equal(t, "https://llmobs-intake.datadoghq.com/api/v2/llmobs", reqs[0].url)
	assert.Equal(t, testAPIKey, reqs[0].headers.Get("DD-API-KEY"))
	assert.Equal(t, "application/json", reqs[0].headers.Get("Content-Type"))
	assert.Empty(t, reqs[0].headers.Get("X-Datadog-EVP-Subdomain"))

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
	assert.Equal(t, "111", span["trace_id"])
	assert.Equal(t, "222", span["span_id"])
	assert.Equal(t, "undefined", span["parent_id"])
	assert.NotContains(t, span, "service")
	assert.Equal(t, "chat", span["name"])
	assert.Equal(t, "ok", span["status"])

	meta := span["meta"].(map[string]any)
	assert.Equal(t, "llm", meta["span.kind"])
	assert.Equal(t, "hello <b>", meta["input"].(map[string]any)["value"])
	assert.Equal(t, "hi", meta["output"].(map[string]any)["value"])

	// JSON decoding hides HTML escaping, so inspect the raw request.
	raw := string(reqs[0].body)
	assert.Contains(t, raw, `"value":"hello <b>"`, "input must reach the wire unescaped")
	escaped, err := json.Marshal("hello <b>")
	require.NoError(t, err)
	assert.NotContains(t, raw, string(escaped), "HTML escaping must stay off")

	dd := span["_dd"].(map[string]any)
	assert.Equal(t, "222", dd["span_id"])
	assert.Equal(t, "111", dd["trace_id"])
	assert.Equal(t, "aabbccdd", dd["apm_trace_id"])

	link := span["span_links"].([]any)[0].(map[string]any)
	assert.Equal(t, "999", link["span_id"])
	assert.Equal(t, "888", link["trace_id"])

	tags := tagsOf(t, span)
	assert.Contains(t, tags, "ml_app:myapp")
	assert.Contains(t, tags, "env:prod")
	assert.Contains(t, tags, "version:1.2.3")
	assert.Contains(t, tags, "service:svc")
	assert.Contains(t, tags, "source:integration")
	assert.Contains(t, tags, "language:go")
	assert.Contains(t, tags, "error:0")
	assert.True(t, slices.ContainsFunc(tags, func(s string) bool {
		return strings.HasPrefix(s, "ddtrace.version:") && s != "ddtrace.version:"
	}), "expected a non-empty ddtrace.version tag, got %v", tags)
}

func TestSubmitSpans_AcceptsExistingSpanRepresentation(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID:  "trace",
		SpanID:   "span",
		ParentID: "parent",
		Name:     "already-built",
		StartNS:  123,
		Duration: 456,
		Status:   export.StatusError,
		Meta: map[string]any{
			"span.kind":     string(export.KindLLM),
			"input":         map[string]any{"value": "existing input"},
			"error.message": "existing error",
			"error.type":    "existing.type",
			"error.stack":   "existing stack",
		},
		Metrics: map[string]float64{"input_tokens": 3},
		DDAttributes: export.DDAttributes{
			APMTraceID: "apm",
		},
		SpanLinks: []export.SpanLink{{
			TraceID: "123",
			SpanID:  "456",
		}},
	}})
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.Equal(t, float64(123), span["start_ns"])
	assert.Equal(t, float64(456), span["duration"])
	assert.Equal(t, "existing input", span["meta"].(map[string]any)["input"].(map[string]any)["value"])
	assert.Equal(t, "existing error", span["meta"].(map[string]any)["error.message"])
	assert.Equal(t, "existing.type", span["meta"].(map[string]any)["error.type"])
	assert.Equal(t, float64(3), span["metrics"].(map[string]any)["input_tokens"])
	assert.Equal(t, "apm", span["_dd"].(map[string]any)["apm_trace_id"])
	assert.Equal(t, "456", span["span_links"].([]any)[0].(map[string]any)["span_id"])
}

func TestSubmitSpans_ErrorSpanShapeMatchesLive(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM,
		Status:       export.StatusError,
		ErrorMessage: "upstream refused",
		ErrorType:    "*errors.errorString",
		ErrorStack:   "goroutine 1 [running]",
	}})
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.Equal(t, "error", span["status"])
	meta := span["meta"].(map[string]any)
	assert.Equal(t, "upstream refused", meta["error.message"])
	assert.Equal(t, "*errors.errorString", meta["error.type"])
	assert.Equal(t, "goroutine 1 [running]", meta["error.stack"])

	tags := tagsOf(t, span)
	assert.Contains(t, tags, "error:1")
	assert.Contains(t, tags, "error_type:*errors.errorString")
}

func TestSubmitSpans_ErrorMessageFallsBackToStatusMessage(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM,
		Status: export.StatusError, StatusMessage: "boom",
	}})
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.Equal(t, "boom", span["meta"].(map[string]any)["error.message"])
}

func TestSubmitSpans_OKSpanHasNoErrorMeta(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM, ErrorMessage: "ignored",
	}})
	require.NoError(t, err)

	meta := allSpans(t, fake.captured()[0].body)[0]["meta"].(map[string]any)
	assert.NotContains(t, meta, "error.message")
	assert.NotContains(t, meta, "error.type")
	assert.NotContains(t, meta, "error.stack")
	assert.Contains(t, tagsOf(t, allSpans(t, fake.captured()[0].body)[0]), "error:0")
}

func TestSubmitSpans_ModelNormalizationMatchesLive(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM, ModelName: "gpt-4o", ModelProvider: "OpenAI"},
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM, ModelName: "gpt-4o"},
		{TraceID: "t3", SpanID: "s3", Kind: export.KindLLM, ModelProvider: "Anthropic"},
		{TraceID: "t4", SpanID: "s4", Kind: export.KindWorkflow},
	})
	require.NoError(t, err)

	spans := allSpans(t, fake.captured()[0].body)
	require.Len(t, spans, 4)
	metaOf := func(i int) map[string]any { return spans[i]["meta"].(map[string]any) }

	assert.Equal(t, "gpt-4o", metaOf(0)["model_name"])
	assert.Equal(t, "openai", metaOf(0)["model_provider"], "provider must be lower-cased")
	assert.Equal(t, "gpt-4o", metaOf(1)["model_name"])
	assert.Equal(t, "custom", metaOf(1)["model_provider"], "missing provider defaults to custom")
	assert.Equal(t, "custom", metaOf(2)["model_name"], "missing name defaults to custom")
	assert.Equal(t, "anthropic", metaOf(2)["model_provider"])
	assert.NotContains(t, metaOf(3), "model_name")
	assert.NotContains(t, metaOf(3), "model_provider")
}

func TestSubmitSpans_ModelGateMatchesLive(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t", SpanID: "s1", Kind: export.KindWorkflow, ModelName: "gpt-4o"},
		{TraceID: "t", SpanID: "s2", Kind: export.KindWorkflow, ModelProvider: "OpenAI"},
		{TraceID: "t", SpanID: "s3", Kind: export.KindEmbedding, ModelName: "text-embed-3"},
	})
	require.NoError(t, err)

	spans := allSpans(t, fake.captured()[0].body)
	require.Len(t, spans, 3)
	metaOf := func(i int) map[string]any { return spans[i]["meta"].(map[string]any) }

	assert.NotContains(t, metaOf(0), "model_name")
	assert.NotContains(t, metaOf(0), "model_provider")
	assert.Equal(t, "custom", metaOf(1)["model_name"])
	assert.Equal(t, "openai", metaOf(1)["model_provider"])
	assert.Equal(t, "text-embed-3", metaOf(2)["model_name"])
	assert.Equal(t, "custom", metaOf(2)["model_provider"])
}

func TestSubmitSpans_ErrorSpanWithNoDetailMatchesLive(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t", SpanID: "s", Kind: export.KindLLM, Status: export.StatusError},
	})
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	meta := span["meta"].(map[string]any)
	assert.Equal(t, "", meta["error.message"])
	assert.Equal(t, "", meta["error.type"])
	assert.Equal(t, "", meta["error.stack"])
	assert.Equal(t, "error", span["status"])
	assert.Contains(t, tagsOf(t, span), "error:1")
}

func TestSubmitSpans_CancelAfterLastPOSTIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeTransport{responder: func(int, *http.Request) (int, string) {
		cancel()
		return 202, "{}"
	}}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(ctx, []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
	require.NoError(t, err, "every row was delivered; a late cancel is not a failure")
	assert.Equal(t, 1, res.Sent)
	assert.Equal(t, 0, res.Failed)
	require.Len(t, res.Requests, 1)
	assert.NoError(t, res.Requests[0].Err)
}

func TestSubmitEvaluations_CancelAfterLastPOSTIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeTransport{responder: func(int, *http.Request) (int, string) {
		cancel()
		return 202, "{}"
	}}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(ctx, []export.EvaluationMetric{
		{SpanID: "s", TraceID: "t", Label: "quality", ScoreValue: ptr(0.9)},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Sent)
	assert.Equal(t, 0, res.Failed)
}

func TestNewClient_FallsBackToEnv(t *testing.T) {
	t.Run("both from env", func(t *testing.T) {
		t.Setenv("DD_SITE", "datadoghq.eu")
		t.Setenv("DD_API_KEY", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

		fake := &fakeTransport{}
		c, err := export.NewClient("app",
			export.WithHTTPClient(&http.Client{Transport: fake}),
			export.WithDatadogIntake("", ""),
		)
		require.NoError(t, err)
		_, err = c.SubmitSpans(context.Background(), []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
		require.NoError(t, err)

		req := fake.captured()[0]
		assert.Equal(t, "https://llmobs-intake.datadoghq.eu/api/v2/llmobs", req.url)
		assert.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", req.headers.Get("DD-API-KEY"))
	})

	t.Run("explicit arguments win over env", func(t *testing.T) {
		t.Setenv("DD_SITE", "datadoghq.eu")
		t.Setenv("DD_API_KEY", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

		fake := &fakeTransport{}
		c, err := export.NewClient("app",
			export.WithHTTPClient(&http.Client{Transport: fake}),
			export.WithDatadogIntake("us3.datadoghq.com", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		)
		require.NoError(t, err)
		_, err = c.SubmitSpans(context.Background(), []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
		require.NoError(t, err)

		req := fake.captured()[0]
		assert.Equal(t, "https://llmobs-intake.us3.datadoghq.com/api/v2/llmobs", req.url)
		assert.Equal(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", req.headers.Get("DD-API-KEY"))
	})

	t.Run("no key anywhere is still an error", func(t *testing.T) {
		t.Setenv("DD_API_KEY", "")
		_, err := export.NewClient("app", export.WithDatadogIntake("datadoghq.com", ""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DD_API_KEY")
	})
}

func TestSubmitSpans_OneEnvelopePerSpan(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	events := make([]export.SpanEvent, 4)
	for i := range events {
		events[i] = export.SpanEvent{TraceID: "t", SpanID: strconv.Itoa(i), Kind: export.KindLLM}
	}
	res, err := c.SubmitSpans(context.Background(), events)
	require.NoError(t, err)
	require.Len(t, res.Requests, 1)

	var envelopes []map[string]any
	require.NoError(t, json.Unmarshal(fake.captured()[0].body, &envelopes))
	require.Len(t, envelopes, 4)
	for i, env := range envelopes {
		assert.Equal(t, "raw", env["_dd.stage"])
		assert.Equal(t, "span", env["event_type"])
		assert.NotEmpty(t, env["_dd.tracer_version"])
		spans := env["spans"].([]any)
		require.Len(t, spans, 1, "envelope %d must carry exactly one span", i)
		assert.Equal(t, strconv.Itoa(i), spans[0].(map[string]any)["span_id"], "span order preserved")
	}
}

func TestSubmitSpans_Chunking(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	events := make([]export.SpanEvent, 120)
	for i := range events {
		events[i] = export.SpanEvent{TraceID: "t", SpanID: "s", Kind: export.KindLLM}
	}
	res, err := c.SubmitSpans(context.Background(), events)
	require.NoError(t, err)
	require.Len(t, res.Requests, 3)
	assert.Len(t, res.Requests[0].InputIndices, 50)
	assert.Len(t, res.Requests[1].InputIndices, 50)
	assert.Len(t, res.Requests[2].InputIndices, 20)
	assert.Equal(t, 0, res.Requests[0].InputIndices[0])
	assert.Equal(t, 119, res.Requests[2].InputIndices[19])
	assert.Equal(t, 120, res.Sent)
	assert.Len(t, fake.captured(), 3)
}

func TestSubmitEvaluations_Chunking(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	evals := make([]export.EvaluationMetric, 2020)
	for i := range evals {
		evals[i] = export.EvaluationMetric{SpanID: "s", TraceID: "t", Label: "ok", ScoreValue: ptr(0.5)}
	}
	res, err := c.SubmitEvaluations(context.Background(), evals)
	require.NoError(t, err)
	require.Len(t, res.Requests, 3)
	assert.Len(t, res.Requests[0].InputIndices, 1000)
	assert.Len(t, res.Requests[1].InputIndices, 1000)
	assert.Len(t, res.Requests[2].InputIndices, 20)
	assert.Equal(t, 0, res.Requests[0].InputIndices[0])
	assert.Equal(t, 2019, res.Requests[2].InputIndices[19])
	assert.Equal(t, 2020, res.Sent)
	assert.Len(t, fake.captured(), 3)
}

func TestSubmitEvaluations_SplitsOversizedBatch(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")
	value := strings.Repeat("x", illmobs.SizeLimitEVPEvent/2)

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t1", Label: "one", JSONValue: map[string]any{"value": value}},
		{SpanID: "s2", TraceID: "t2", Label: "two", JSONValue: map[string]any{"value": value}},
	})
	require.NoError(t, err)
	require.Len(t, res.Requests, 2)
	assert.Equal(t, []int{0}, res.Requests[0].InputIndices)
	assert.Equal(t, []int{1}, res.Requests[1].InputIndices)
	assert.Equal(t, 2, res.Sent)
}

func TestSubmitEvaluations_DropsOversizedRow(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{{
		SpanID:     "s",
		TraceID:    "t",
		Label:      "large",
		ScoreValue: ptr(1.0),
		Metadata:   map[string]any{"value": strings.Repeat("x", illmobs.SizeLimitEVPEvent)},
	}})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1)
	assert.Equal(t, export.CodeTooLarge, res.ValidationErrors[0].Code)
	assert.Empty(t, fake.captured())
}

func TestSubmitSpans_AcceptsEveryExportableKind(t *testing.T) {
	kinds := []export.Kind{
		export.KindLLM,
		export.KindAgent,
		export.KindWorkflow,
		export.KindTask,
		export.KindTool,
		export.KindEmbedding,
		export.KindRetrieval,
	}
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")
	events := make([]export.SpanEvent, 0, len(kinds))
	for i, kind := range kinds {
		events = append(events, export.SpanEvent{
			TraceID: "t",
			SpanID:  strconv.Itoa(i),
			Kind:    kind,
		})
	}

	res, err := c.SubmitSpans(context.Background(), events)
	require.NoError(t, err)
	assert.Empty(t, res.ValidationErrors)
	assert.Equal(t, len(kinds), res.Sent)
}

func TestSubmitSpans_ValidationDropsInvalidRows(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM},
		{SpanID: "s2", Kind: export.KindLLM},
		{TraceID: "t3", Kind: export.KindLLM},
		{TraceID: "t4", SpanID: "s4", Kind: "banana"},
		{TraceID: "t5", SpanID: "s5", Kind: export.KindLLM, Status: "kinda-ok"},
		{TraceID: "t6", SpanID: "s6", Kind: export.Kind(illmobs.SpanKindExperiment)},
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 5)
	assert.Equal(t, 5, res.Dropped)
	assert.Equal(t, 1, res.Sent)
	assert.Equal(t, []export.ErrorCode{
		export.CodeMissingID,
		export.CodeMissingID,
		export.CodeInvalidKind,
		export.CodeInvalidStatus,
		export.CodeInvalidKind,
	}, []export.ErrorCode{
		res.ValidationErrors[0].Code,
		res.ValidationErrors[1].Code,
		res.ValidationErrors[2].Code,
		res.ValidationErrors[3].Code,
		res.ValidationErrors[4].Code,
	})

	reqs := fake.captured()
	require.Len(t, reqs, 1)
	spans := allSpans(t, reqs[0].body)
	assert.Len(t, spans, 1)
}

func TestSubmitSpans_ValidatesCanonicalSpanLinkIDs(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{
			TraceID: "t0",
			SpanID:  "s0",
			Kind:    export.KindLLM,
			SpanLinks: []export.SpanLink{{
				TraceID:     "18446744073709551615",
				TraceIDHigh: "0",
				SpanID:      "42",
			}},
		},
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM, SpanLinks: []export.SpanLink{{TraceID: "01", SpanID: "2"}}},
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM, SpanLinks: []export.SpanLink{{TraceID: "1", SpanID: "+2"}}},
		{TraceID: "t3", SpanID: "s3", Kind: export.KindLLM, SpanLinks: []export.SpanLink{{TraceID: "1", SpanID: "2", TraceIDHigh: "18446744073709551616"}}},
		{TraceID: "t4", SpanID: "s4", Kind: export.KindLLM, SpanLinks: []export.SpanLink{{SpanID: "2"}}},
		{TraceID: "t5", SpanID: "s5", Kind: export.KindLLM, SpanLinks: []export.SpanLink{{TraceID: "1"}}},
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 5)
	assert.Equal(t, 1, res.Sent)
	assert.Equal(t, 5, res.Dropped)
	for _, validation := range res.ValidationErrors {
		assert.Equal(t, export.CodeInvalidLink, validation.Code)
	}
}

func TestDefaultSizeGuardMatchesLiveLimit(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM,
		Input: strings.Repeat("x", illmobs.SizeLimitEVPEvent+1),
	}})
	require.NoError(t, err)
	require.Len(t, res.Requests, 1)

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.Equal(t, "[This value has been dropped because this span's size exceeds the 1MB size limit.]", span["meta"].(map[string]any)["input"].(map[string]any)["value"])
}

func TestSubmitSpans_DropsOversizedSpanWithoutIO(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t",
		SpanID:  "s",
		Kind:    export.KindLLM,
		Metadata: map[string]any{
			"value": strings.Repeat("x", illmobs.SizeLimitEVPEvent),
		},
	}})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1)
	assert.Equal(t, export.CodeTooLarge, res.ValidationErrors[0].Code)
	assert.Empty(t, fake.captured())
}

func TestSubmitSpans_SplitsOversizedBatchInsteadOfDroppingIO(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")
	value := strings.Repeat("x", illmobs.SizeLimitEVPEvent/2)

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM, Input: value},
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM, Input: value},
	})
	require.NoError(t, err)
	require.Len(t, res.Requests, 2)
	assert.Equal(t, []int{0}, res.Requests[0].InputIndices)
	assert.Equal(t, []int{1}, res.Requests[1].InputIndices)

	for _, req := range fake.captured() {
		span := allSpans(t, req.body)[0]
		assert.NotContains(t, span, "collection_errors")
		assert.NotEmpty(t, span["meta"].(map[string]any)["input"])
	}
}

func TestSubmitSpans_StampsMLAppFromClient(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "my-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM},
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM, Tags: []string{"ml_app:override"}},
		{TraceID: "t3", SpanID: "s3", Kind: export.KindLLM, Tags: []string{"ml_app:"}},
	})
	require.NoError(t, err)

	spans := allSpans(t, fake.captured()[0].body)
	require.Len(t, spans, 3)
	assert.Contains(t, tagsOf(t, spans[0]), "ml_app:my-app")
	assert.Contains(t, tagsOf(t, spans[1]), "ml_app:override")
	assert.NotContains(t, tagsOf(t, spans[1]), "ml_app:my-app")
	assert.Contains(t, tagsOf(t, spans[2]), "ml_app:my-app")
	assert.NotContains(t, tagsOf(t, spans[2]), "ml_app:")
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
	assert.Empty(t, reqs[0].headers.Get("DD-API-KEY"))
}

func TestSubmitSpans_WithCallServiceOverride(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithService("default-svc"))

	_, err := c.SubmitSpans(context.Background(),
		[]export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}},
		export.WithCallService("call-svc"),
	)
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.NotContains(t, span, "service")
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
	require.Equal(t, 1, res.Failed)
	require.Zero(t, res.Sent)
	require.Len(t, res.Requests, 1)
	assert.Greater(t, res.Requests[0].Attempts, 1)
	assert.True(t, res.Requests[0].Retriable)
	assert.Equal(t, 500, res.Requests[0].StatusCode)
	assert.Equal(t, "boom", res.Requests[0].ResponseSnippet)
	assert.Error(t, res.Requests[0].Err)
	assert.ErrorIs(t, err, res.Requests[0].Err)
}

func TestSubmitSpans_PermanentError(t *testing.T) {
	fake := &fakeTransport{responder: func(int, *http.Request) (int, string) { return 400, "bad" }}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
	require.Error(t, err)
	require.Len(t, res.Requests, 1)
	assert.Equal(t, 1, res.Requests[0].Attempts)
	assert.False(t, res.Requests[0].Retriable)
	assert.Equal(t, 400, res.Requests[0].StatusCode)
	assert.Equal(t, "bad", res.Requests[0].ResponseSnippet)
	assert.ErrorIs(t, err, res.Requests[0].Err)
}

func TestSubmitSpans_ResponseSnippetIsBoundedUTF8(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		want     string
		wantSize int
	}{
		{
			name: "invalid bytes",
			body: string([]byte{' ', 'o', 'k', 0xff, ' '}),
			want: "ok",
		},
		{
			name:     "multibyte boundary",
			body:     "a" + strings.Repeat("é", 300),
			wantSize: 511,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTransport{responder: func(int, *http.Request) (int, string) {
				return http.StatusBadRequest, test.body
			}}
			c := newClient(t, fake, "test-app")

			res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
				TraceID: "t",
				SpanID:  "s",
				Kind:    export.KindLLM,
			}})
			require.Error(t, err)
			require.Len(t, res.Requests, 1)
			snippet := res.Requests[0].ResponseSnippet
			assert.True(t, utf8.ValidString(snippet))
			if test.want != "" {
				assert.Equal(t, test.want, snippet)
			}
			if test.wantSize != 0 {
				assert.Len(t, snippet, test.wantSize)
			}
		})
	}
}

func TestSubmitEvaluations_WireShapeVariants(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "defaultapp")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t1", Label: "quality", CategoricalValue: ptr("good"), Timestamp: time.UnixMilli(123)},
		{SpanID: "s2", TraceID: "t2", Label: "score", ScoreValue: ptr(0.9)},
		{SpanID: "s3", TraceID: "t3", Label: "ok", BooleanValue: ptr(true)},
		{SpanID: "s4", TraceID: "t4", Label: "struct", JSONValue: map[string]any{"k": "v"}, MetricType: export.MetricTypeJSON},
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
	assert.Equal(t, "defaultapp", m0["ml_app"])
	assert.Equal(t, float64(123), m0["timestamp_ms"])
	join := m0["join_on"].(map[string]any)["span"].(map[string]any)
	assert.Equal(t, "s1", join["span_id"])
	assert.Equal(t, "t1", join["trace_id"])

	m1 := metrics[1].(map[string]any)
	assert.Equal(t, "score", m1["metric_type"])
	assert.Greater(t, m1["timestamp_ms"].(float64), float64(0))
	m3 := metrics[3].(map[string]any)
	assert.Equal(t, "json", m3["metric_type"])
	assert.NotNil(t, m3["json_value"])
	m4 := metrics[4].(map[string]any)
	tagJoin := m4["join_on"].(map[string]any)["tag"].(map[string]any)
	assert.Equal(t, "session_id", tagJoin["key"])
	assert.Equal(t, "abc", tagJoin["value"])
}

func TestSubmitEvaluations_NarrativeFieldsReachTheWire(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{{
		SpanID: "s", TraceID: "t", Label: "quality", ScoreValue: ptr(0.75),
		Assessment: "mostly correct",
		Reasoning:  "cited two of three sources",
		Metadata:   map[string]any{"judge": "gpt-4o", "rubric_version": float64(3)},
	}})
	require.NoError(t, err)

	m := firstMetric(t, fake.captured()[0].body)
	assert.Equal(t, "mostly correct", m["assessment"])
	assert.Equal(t, "cited two of three sources", m["reasoning"])
	assert.Equal(t, map[string]any{"judge": "gpt-4o", "rubric_version": float64(3)}, m["eval_metric_metadata"])

	fake2 := &fakeTransport{}
	c2 := newClient(t, fake2, "test-app")
	_, err = c2.SubmitEvaluations(context.Background(), []export.EvaluationMetric{{
		SpanID: "s", TraceID: "t", Label: "quality", ScoreValue: ptr(0.75),
	}})
	require.NoError(t, err)
	bare := firstMetric(t, fake2.captured()[0].body)
	assert.NotContains(t, bare, "assessment")
	assert.NotContains(t, bare, "reasoning")
	assert.NotContains(t, bare, "eval_metric_metadata")
}

func TestSubmitEvaluations_WithCallMLApp(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "client-app")

	_, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t", Label: "q", ScoreValue: ptr(1.0)},
		{SpanID: "s2", TraceID: "t", Label: "q", ScoreValue: ptr(1.0), MLApp: "row-app"},
	}, export.WithCallMLApp("call-app"))
	require.NoError(t, err)

	metrics := allMetrics(t, fake.captured()[0].body)
	require.Len(t, metrics, 2)
	assert.Equal(t, "call-app", metrics[0]["ml_app"])
	assert.Equal(t, "row-app", metrics[1]["ml_app"])
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
	assert.Contains(t, tags, "team:ml")
	assert.NotContains(t, tags, "ddtrace.version:bogus")
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
		{SpanID: "s", TraceID: "t", ScoreValue: ptr(1.0)},
		{Label: "no-join", ScoreValue: ptr(1.0)},
		{SpanID: "s", TraceID: "t", TagKey: "k", TagValue: "v", Label: "both", ScoreValue: ptr(1.0)},
		{SpanID: "s", TraceID: "t", Label: "novalue"},
		{SpanID: "s", TraceID: "t", Label: "twovalues", ScoreValue: ptr(1.0), BooleanValue: ptr(true)},
		{SpanID: "s", TraceID: "t", Label: "jsonscalarmismatch", MetricType: export.MetricTypeCategorical, JSONValue: map[string]any{"k": "v"}},
		{SpanID: "s", Label: "partial", ScoreValue: ptr(1.0)},
		{SpanID: "s", TraceID: "t", Label: "badtype", MetricType: export.MetricType("scores"), ScoreValue: ptr(1.0)},
		{SpanID: "s", TraceID: "t", Label: "mismatch", MetricType: export.MetricTypeScore, CategoricalValue: ptr("x")},
		{SpanID: "s", TraceID: "t", Label: "emptyjson", MetricType: export.MetricTypeCategorical, JSONValue: map[string]any{}},
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 10)
	assert.Equal(t, 10, res.Dropped)
	assert.Equal(t, []export.ErrorCode{
		export.CodeMissingLabel,
		export.CodeInvalidJoin,
		export.CodeInvalidJoin,
		export.CodeInvalidValue,
		export.CodeInvalidValue,
		export.CodeTypeMismatch,
		export.CodeInvalidJoin,
		export.CodeTypeMismatch,
		export.CodeTypeMismatch,
		export.CodeInvalidValue,
	}, []export.ErrorCode{
		res.ValidationErrors[0].Code,
		res.ValidationErrors[1].Code,
		res.ValidationErrors[2].Code,
		res.ValidationErrors[3].Code,
		res.ValidationErrors[4].Code,
		res.ValidationErrors[5].Code,
		res.ValidationErrors[6].Code,
		res.ValidationErrors[7].Code,
		res.ValidationErrors[8].Code,
		res.ValidationErrors[9].Code,
	})
	assert.Contains(t, res.ValidationErrors[0].Error(), "row 0 rejected (missing_label)")
	assert.Empty(t, fake.captured())
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
	t.Setenv("DD_API_KEY", "")
	_, err := export.NewClient("app", export.WithDatadogIntake("datadoghq.com", ""))
	assert.Error(t, err)

	_, err = export.NewClient("app", export.WithDatadogIntake("datadoghq.com", "invalid"))
	assert.Error(t, err)
}

func TestNewClient_RequiresMLApp(t *testing.T) {
	_, err := export.NewClient("", export.WithDatadogIntake("datadoghq.com", testAPIKey))
	assert.Error(t, err)
}

func TestNewClient_RequiresExactlyOneRoute(t *testing.T) {
	_, err := export.NewClient("app")
	assert.Error(t, err)

	_, err = export.NewClient("app",
		export.WithDatadogIntake("datadoghq.com", testAPIKey),
		export.WithAgentURL("http://localhost:8126"),
	)
	assert.Error(t, err)
}

func TestSubmitSpans_ConcurrentDoesNotMutateCaller(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithEnv("prod"), export.WithVersion("1.0"))

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
	assert.Empty(t, fake.captured())
	require.Len(t, res.Requests, 1)
	assert.ErrorIs(t, res.Requests[0].Err, context.Canceled)
	assert.Equal(t, []int{0}, res.Requests[0].InputIndices)
	assert.True(t, res.Requests[0].Retriable)
	assert.Equal(t, 0, res.Sent)
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, 1, res.Sent+res.Failed+res.Dropped)
}

func TestSubmitEvaluations_ContextCanceledStopsPromptly(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := c.SubmitEvaluations(ctx, []export.EvaluationMetric{
		{SpanID: "s", TraceID: "t", Label: "quality", ScoreValue: ptr(0.9)},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, fake.captured())
	require.Len(t, res.Requests, 1)
	assert.ErrorIs(t, res.Requests[0].Err, context.Canceled)
	assert.Equal(t, []int{0}, res.Requests[0].InputIndices)
	assert.True(t, res.Requests[0].Retriable)
	assert.Equal(t, 1, res.Sent+res.Failed+res.Dropped)
}

func TestSubmitEvaluations_MidFlightCancelNotRetriable(t *testing.T) {
	block := &blockingTransport{entered: make(chan struct{})}
	c, err := export.NewClient("test-app",
		export.WithHTTPClient(&http.Client{Transport: block}),
		export.WithDatadogIntake("datadoghq.com", testAPIKey),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-block.entered
		cancel()
	}()

	res, err := c.SubmitEvaluations(ctx, []export.EvaluationMetric{
		{SpanID: "s", TraceID: "t", Label: "quality", ScoreValue: ptr(0.9)},
	})
	require.Error(t, err)
	require.Len(t, res.Requests, 1)
	assert.False(t, res.Requests[0].Retriable, "a caller cancellation is not a transient failure")
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, 1, res.Sent+res.Failed+res.Dropped)
}

func TestSubmitSpans_AccountingCoversWholeInputOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	fake := &fakeTransport{responder: func(int, *http.Request) (int, string) {
		once.Do(cancel)
		return 202, "{}"
	}}
	c := newClient(t, fake, "test-app")

	events := make([]export.SpanEvent, 51)
	for i := range events {
		events[i] = export.SpanEvent{TraceID: "t", SpanID: strconv.Itoa(i), Kind: export.KindLLM}
	}
	res, err := c.SubmitSpans(ctx, events)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Len(t, fake.captured(), 1)
	require.Len(t, res.Requests, 2)
	assert.Equal(t, 50, res.Sent)
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, []int{50}, res.Requests[1].InputIndices)
	assert.Equal(t, len(events), res.Sent+res.Failed+res.Dropped)
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
	require.Len(t, res.ValidationErrors, 2)
	assert.Equal(t, 0, res.ValidationErrors[0].Index)
	assert.Equal(t, 1, res.ValidationErrors[1].Index)

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

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.Contains(t, span["tags"].([]any), "session_id:sess-1")
	assert.Equal(t, "sess-1", span["session_id"])
}

func TestSubmitSpans_DropsMissingKind(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM},
		{TraceID: "t2", SpanID: "s2"},
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1)
	assert.Equal(t, 1, res.ValidationErrors[0].Index)

	span := allSpans(t, fake.captured()[0].body)
	require.Len(t, span, 1)
}

func TestSubmitSpans_RejectsNonFiniteMetric(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM, Metrics: map[string]float64{"estimated_total_cost": math.Inf(1)}},
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM},
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1)
	assert.Equal(t, 0, res.ValidationErrors[0].Index)

	span := allSpans(t, fake.captured()[0].body)
	require.Len(t, span, 1)
}

func TestSubmitSpans_SessionIDOverridesStaleTag(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM, SessionID: "new",
		Tags: []string{"session_id:old", "team:ml"},
	}})
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	tags := make([]string, 0, len(span["tags"].([]any)))
	for _, x := range span["tags"].([]any) {
		tags = append(tags, x.(string))
	}
	assert.Contains(t, tags, "session_id:new")
	assert.NotContains(t, tags, "session_id:old")
	assert.Contains(t, tags, "team:ml")
	assert.Equal(t, "new", span["session_id"])
}

func TestSubmitSpans_ServiceTagReplacesStale(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithService("svc"))

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM,
		Tags: []string{"service:stale", "team:ml"},
	}})
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	tags := make([]string, 0, len(span["tags"].([]any)))
	for _, x := range span["tags"].([]any) {
		tags = append(tags, x.(string))
	}
	assert.Contains(t, tags, "service:svc")
	assert.NotContains(t, tags, "service:stale")
	assert.Contains(t, tags, "team:ml")
	assert.NotContains(t, span, "service")
}

func TestSubmitSpans_UsesExistingMetricsMap(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM,
		Metrics: map[string]float64{
			"input_tokens":             10,
			"billable_character_count": 42,
			"time_to_first_token":      0.25,
			"custom_metric":            7,
		},
	}})
	require.NoError(t, err)

	m := allSpans(t, fake.captured()[0].body)[0]["metrics"].(map[string]any)
	assert.Equal(t, float64(10), m["input_tokens"])
	assert.Equal(t, float64(42), m["billable_character_count"])
	assert.Equal(t, 0.25, m["time_to_first_token"])
	assert.Equal(t, float64(7), m["custom_metric"])
}

func TestSubmitEvaluations_RejectsUnmarshalableJSON(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t1", Label: "bad", MetricType: export.MetricTypeJSON, JSONValue: map[string]any{"x": math.Inf(1)}},
		{SpanID: "s2", TraceID: "t2", Label: "ok", ScoreValue: ptr(0.5)},
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1)
	assert.Equal(t, 0, res.ValidationErrors[0].Index)
	assert.Contains(t, res.ValidationErrors[0].Reason, "not JSON-encodable")
	assert.Equal(t, 1, res.Dropped)
	assert.Equal(t, 1, res.Sent)
	assert.Zero(t, res.Failed)

	require.Len(t, fake.captured(), 1)
	metrics := decode(t, fake.captured()[0].body)["data"].(map[string]any)["attributes"].(map[string]any)["metrics"].([]any)
	require.Len(t, metrics, 1)
}

func TestSubmitEvaluations_DropsAllUnencodableJSON(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t1", Label: "bad1", MetricType: export.MetricTypeJSON, JSONValue: map[string]any{"x": math.Inf(1)}},
		{SpanID: "s2", TraceID: "t2", Label: "bad2", MetricType: export.MetricTypeJSON, JSONValue: map[string]any{"y": math.Inf(-1)}},
	})
	require.NoError(t, err)
	assert.Len(t, res.ValidationErrors, 2)
	assert.Equal(t, 2, res.Dropped)
	assert.Zero(t, res.Sent)
	assert.Zero(t, res.Failed)
	assert.Empty(t, res.Requests)
	assert.Empty(t, fake.captured())
}

func TestSubmitEvaluations_RejectsTypeValueMismatch(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t1", Label: "bad", MetricType: export.MetricTypeCategorical, JSONValue: map[string]any{"x": 1}},
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1)
	assert.Equal(t, 0, res.ValidationErrors[0].Index)
	assert.Contains(t, res.ValidationErrors[0].Reason, "does not match")
	assert.Empty(t, fake.captured())
}

func TestSubmitSpans_ZeroStartAndDurationOmitFields(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t", SpanID: "s", Kind: export.KindLLM},
	})
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.NotContains(t, span, "start_ns")
	assert.NotContains(t, span, "duration")
}

func TestSubmitSpans_ParentIDPreservedVerbatim(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t", SpanID: "s", ParentID: "p123", Kind: export.KindLLM},
	})
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.Equal(t, "p123", span["parent_id"])
}

func TestSubmitSpans_RetryClassification(t *testing.T) {
	cases := []struct {
		name          string
		code          int
		retryAfter    string
		wantRetriable bool
	}{
		{name: "429 too many requests", code: 429, wantRetriable: true},
		{name: "408 request timeout", code: 408, wantRetriable: true},
		{name: "425 too early", code: 425, wantRetriable: true},
		{name: "503 with retry-after", code: 503, retryAfter: "1", wantRetriable: true},
		{name: "400 bad request", code: 400, wantRetriable: false},
		{name: "404 not found", code: 404, wantRetriable: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeTransport{responder: func(int, *http.Request) (int, string) { return tc.code, "err" }}
			if tc.retryAfter != "" {
				fake.respHeader = http.Header{"Retry-After": []string{tc.retryAfter}}
			}
			c := newClient(t, fake, "test-app")

			res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
			require.Error(t, err)
			require.Len(t, res.Requests, 1)
			assert.Equal(t, tc.code, res.Requests[0].StatusCode)
			assert.Equal(t, tc.wantRetriable, res.Requests[0].Retriable)
			if tc.wantRetriable {
				assert.Greater(t, res.Requests[0].Attempts, 1, "retriable status should be retried")
			} else {
				assert.Equal(t, 1, res.Requests[0].Attempts, "permanent status must not be retried")
			}
		})
	}
}

func TestSubmitSpans_MidFlightCancelNotRetriable(t *testing.T) {
	bt := &blockingTransport{entered: make(chan struct{})}
	c, err := export.NewClient("test-app",
		export.WithHTTPClient(&http.Client{Transport: bt}),
		export.WithDatadogIntake("datadoghq.com", testAPIKey),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		res *export.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := c.SubmitSpans(ctx, []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
		done <- outcome{res, err}
	}()

	select {
	case <-bt.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("RoundTrip was never entered")
	}
	cancel()

	select {
	case got := <-done:
		require.Error(t, got.err)
		require.Len(t, got.res.Requests, 1)
		assert.False(t, got.res.Requests[0].Retriable)
		assert.ErrorIs(t, got.res.Requests[0].Err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("SubmitSpans did not return after mid-flight cancellation")
	}
}

func TestSubmitSpans_RetriableStatusThenCancelNotRetriable(t *testing.T) {
	var once sync.Once
	responded := make(chan struct{})
	fake := &fakeTransport{
		respHeader: http.Header{"Retry-After": []string{"2"}},
		responder: func(int, *http.Request) (int, string) {
			once.Do(func() { close(responded) })
			return 503, "unavailable"
		},
	}
	c := newClient(t, fake, "test-app")

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		res *export.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := c.SubmitSpans(ctx, []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
		done <- outcome{res, err}
	}()

	select {
	case <-responded:
	case <-time.After(10 * time.Second):
		t.Fatal("transport was never called")
	}
	cancel()

	select {
	case got := <-done:
		require.Error(t, got.err)
		require.Len(t, got.res.Requests, 1)
		assert.Equal(t, 503, got.res.Requests[0].StatusCode)
		assert.False(t, got.res.Requests[0].Retriable)
	case <-time.After(10 * time.Second):
		t.Fatal("SubmitSpans did not return after cancellation during backoff")
	}
}

func TestSubmitEvaluations_JSONMetricType(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{{
		SpanID: "s1", TraceID: "t1", Label: "range",
		MetricType: export.MetricTypeJSON,
		JSONValue:  map[string]any{"turn_start": float64(1), "turn_end": float64(4), "outcome": "ok"},
		Timestamp:  time.UnixMilli(123),
	}})
	require.NoError(t, err)
	require.Zero(t, res.Failed)
	require.Equal(t, 1, res.Sent)

	m := decode(t, fake.captured()[0].body)["data"].(map[string]any)["attributes"].(map[string]any)["metrics"].([]any)[0].(map[string]any)
	assert.Equal(t, "json", m["metric_type"])
	jv := m["json_value"].(map[string]any)
	assert.Equal(t, float64(4), jv["turn_end"])
	assert.Equal(t, "ok", jv["outcome"])
}

func TestSubmitEvaluations_JSONMetricTypeRequiresJSONValue(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{{
		SpanID: "s1", TraceID: "t1", Label: "bad", MetricType: export.MetricTypeJSON, ScoreValue: ptr(0.5),
	}})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1)
	assert.Empty(t, fake.captured())
}

func TestSubmitSpans_SplitStopsOnCancelBetweenHalves(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	fake := &fakeTransport{responder: func(int, *http.Request) (int, string) {
		once.Do(cancel)
		return 202, "{}"
	}}
	c := newClient(t, fake, "test-app")
	value := strings.Repeat("x", illmobs.SizeLimitEVPEvent/2)

	res, err := c.SubmitSpans(ctx, []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM, Input: value},
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM, Input: value},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Len(t, fake.captured(), 1)
	require.Len(t, res.Requests, 2)
	assert.Equal(t, 202, res.Requests[0].StatusCode)
	assert.NoError(t, res.Requests[0].Err)
	assert.ErrorIs(t, res.Requests[1].Err, context.Canceled)
	assert.Equal(t, []int{1}, res.Requests[1].InputIndices)
	assert.Equal(t, 2, res.Sent+res.Failed+res.Dropped)
	assert.Equal(t, 1, res.Sent)
	assert.Equal(t, 1, res.Failed)
}
