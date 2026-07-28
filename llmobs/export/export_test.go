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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/llmobs/export"
)

// fakeTransport records outgoing requests and returns canned responses without
// touching the network, so tests can assert the derived URL, headers and body.
type fakeTransport struct {
	mu        sync.Mutex
	requests  []capturedRequest
	responder func(attempt int, req *http.Request) (int, string)
	// respHeader, when set, adds headers to every response (e.g. Retry-After) on
	// top of the default Content-Type. Used to drive the transport's retry-delay
	// branches; nil leaves responses with just Content-Type.
	respHeader http.Header
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

// blockingTransport blocks each request until its context is done, then returns
// that context's error — modeling a server that never responds, so a mid-flight
// caller cancellation (as opposed to a pre-flight one) drives the transport's
// "cancelled context is not retriable" path. entered is closed once the request
// is in flight, so a test can cancel deterministically without sleeping.
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
// envelopes, one per span — and returns its first element.
func firstReq(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var arr []map[string]any
	require.NoError(t, json.Unmarshal(b, &arr))
	require.NotEmpty(t, arr)
	return arr[0]
}

// allSpans flattens every span across a request body's envelopes, in order. Each
// envelope carries exactly one span (see transport.NewPushSpanEventsRequests), so
// this is the per-request span list.
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

// allMetrics returns the eval metrics in an eval-metric request body, in order.
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

// tagsOf returns a span's tags as a string slice.
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

	span := allSpans(t, fake.captured()[0].body)[0]
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
	assert.Equal(t, "llm", meta["span"].(map[string]any)["kind"]) // nested meta.span.kind (Trajectory + intake schema)
	assert.Equal(t, "llm", meta["span.kind"])                     // flat key (live-tracer parity)
	assert.Equal(t, "hello <b>", meta["input"].(map[string]any)["value"])
	assert.Equal(t, "hi", meta["output"].(map[string]any)["value"])

	// Assert non-escaping on the raw bytes, not the decoded value: json.Unmarshal
	// reverses the escaping, so the assertions above pass whether or not
	// SetEscapeHTML was flipped back on. json.Marshal escapes HTML by default, so
	// its output is exactly the form the body must not contain.
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
	assert.Equal(t, "999", link["span_id"]) // string span-link IDs
	assert.Equal(t, "888", link["trace_id"])

	tags := tagsOf(t, span)
	assert.Contains(t, tags, "ml_app:myapp")
	assert.Contains(t, tags, "env:prod")
	assert.Contains(t, tags, "version:1.2.3")
	assert.Contains(t, tags, "service:svc") // service carried as a tag (intake reads it there)
	// Facets the live path stamps on every span; without them a group-by mixing
	// live and exported spans splits into a tagged and an untagged bucket.
	assert.Contains(t, tags, "source:integration")
	assert.Contains(t, tags, "language:go")
	assert.Contains(t, tags, "error:0")
	assert.True(t, slices.ContainsFunc(tags, func(s string) bool {
		return strings.HasPrefix(s, "ddtrace.version:") && s != "ddtrace.version:"
	}), "expected a non-empty ddtrace.version tag, got %v", tags)
}

// TestSubmitSpans_ErrorSpanShapeMatchesLive locks the meta and tags an errored
// exported span carries. The live path emits the error.message/type/stack triple
// and an error:1 tag alongside error_type; an exported error span that carried
// only status:"error" would be invisible to every error-rate query built on the
// live shape.
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

// TestSubmitSpans_ErrorMessageFallsBackToStatusMessage: a caller that only filled
// StatusMessage still gets an error.message rather than an empty one.
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

// TestSubmitSpans_OKSpanHasNoErrorMeta: the error triple is error-only, so an ok
// span must not gain three empty meta keys.
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

// TestSubmitSpans_ModelNormalizationMatchesLive: the live path lower-cases
// model_provider and fills the missing half of the pair with "custom". Exported
// spans must do the same or "OpenAI" and "openai" become two facets on one intake.
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
	// A span with no model information at all omits both keys.
	assert.NotContains(t, metaOf(3), "model_name")
	assert.NotContains(t, metaOf(3), "model_provider")
}

// TestSubmitSpans_OneEnvelopePerSpan locks the /api/v2/llmobs body shape against
// the only form known to work in production: PushSpanEvents (the live path) posts
// an array of single-span envelopes, because _dd.scope is a per-envelope field
// derived from each span. Packing N spans into one envelope risks intake reading
// only the first, silently losing the rest of every batch.
func TestSubmitSpans_OneEnvelopePerSpan(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	events := make([]export.SpanEvent, 4)
	for i := range events {
		events[i] = export.SpanEvent{TraceID: "t", SpanID: strconv.Itoa(i), Kind: export.KindLLM}
	}
	res, err := c.SubmitSpans(context.Background(), events)
	require.NoError(t, err)
	require.Len(t, res.Requests, 1) // one POST...

	var envelopes []map[string]any
	require.NoError(t, json.Unmarshal(fake.captured()[0].body, &envelopes))
	require.Len(t, envelopes, 4) // ...carrying one envelope per span
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

// TestSubmitEvaluations_Chunking is the eval analogue of TestSubmitSpans_Chunking:
// it exercises the SubmitEvaluations chunk loop and the public WithEvalBatchSize
// option (which is otherwise only ever run as a single default-sized batch). 120
// evals at a batch size of 50 must produce three requests (50/50/20) with correct
// per-chunk Count and monotonic Index, and Sent must total the whole input.
func TestSubmitEvaluations_Chunking(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app", export.WithEvalBatchSize(50))

	evals := make([]export.EvaluationMetric, 120)
	for i := range evals {
		evals[i] = export.EvaluationMetric{SpanID: "s", TraceID: "t", Label: "ok", ScoreValue: ptr(0.5)}
	}
	res, err := c.SubmitEvaluations(context.Background(), evals)
	require.NoError(t, err)
	require.Len(t, res.Requests, 3)
	assert.Equal(t, 50, res.Requests[0].Count)
	assert.Equal(t, 50, res.Requests[1].Count)
	assert.Equal(t, 20, res.Requests[2].Count)
	// Index is monotonic and zero-based across the chunks of one call.
	assert.Equal(t, 0, res.Requests[0].Index)
	assert.Equal(t, 1, res.Requests[1].Index)
	assert.Equal(t, 2, res.Requests[2].Index)
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
	spans := allSpans(t, reqs[0].body)
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

	// The sentinel text and collection error come from the live path's
	// DropSpanEventIO, so a truncated exported span is indistinguishable from a
	// truncated live one.
	span := allSpans(t, fake.captured()[0].body)[0]
	meta := span["meta"].(map[string]any)
	assert.Equal(t, illmobs.DroppedValueText, meta["input"].(map[string]any)["value"])
	assert.Equal(t, illmobs.DroppedValueText, meta["output"].(map[string]any)["value"])
	assert.Contains(t, span["collection_errors"].([]any), illmobs.CollectionErrorDroppedIO)
}

// TestDefaultSizeGuardMatchesLiveLimit: the default guard is the live path's
// per-event limit, not a separate 5 MiB constant. The 242,880-byte gap between
// 5<<20 and 5_000_000 used to let a single span through at a size the SDK's own
// constant says intake rejects.
func TestDefaultSizeGuardMatchesLiveLimit(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	// A span just over the live limit must be truncated by the default guard.
	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{{
		TraceID: "t", SpanID: "s", Kind: export.KindLLM,
		Input: strings.Repeat("x", illmobs.SizeLimitEVPEvent+1),
	}})
	require.NoError(t, err)
	require.Len(t, res.Requests, 1)

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.Equal(t, illmobs.DroppedValueText, span["meta"].(map[string]any)["input"].(map[string]any)["value"])
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
		span := allSpans(t, req.body)[0]
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

	spans := allSpans(t, fake.captured()[0].body)
	require.Len(t, spans, 3)
	assert.Contains(t, tagsOf(t, spans[0]), "ml_app:my-app")
	assert.Contains(t, tagsOf(t, spans[1]), "ml_app:override")
	assert.NotContains(t, tagsOf(t, spans[1]), "ml_app:my-app")
	// An empty "ml_app:" tag must not suppress the required default: it is dropped
	// and replaced with the configured value, leaving no bare empty tag behind.
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

	span := allSpans(t, fake.captured()[0].body)[0]
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
	assert.Equal(t, "defaultapp", m0["ml_app"]) // default applied
	assert.Equal(t, float64(123), m0["timestamp_ms"])
	join := m0["join_on"].(map[string]any)["span"].(map[string]any)
	assert.Equal(t, "s1", join["span_id"])
	assert.Equal(t, "t1", join["trace_id"])

	m1 := metrics[1].(map[string]any)
	assert.Equal(t, "score", m1["metric_type"])
	m3 := metrics[3].(map[string]any)
	assert.Equal(t, "json", m3["metric_type"]) // a structured json_value pairs with metric_type json
	assert.NotNil(t, m3["json_value"])
	m4 := metrics[4].(map[string]any)
	tagJoin := m4["join_on"].(map[string]any)["tag"].(map[string]any)
	assert.Equal(t, "session_id", tagJoin["key"])
	assert.Equal(t, "abc", tagJoin["value"])
}

// TestSubmitEvaluations_NarrativeFieldsReachTheWire covers assessment, reasoning
// and metadata — three fields this PR adds to a wire struct the live eval path
// shares, and which nothing else asserts.
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
	assert.Equal(t, map[string]any{"judge": "gpt-4o", "rubric_version": float64(3)}, m["metadata"])

	// All three are omitempty: a metric that sets none must not emit empty keys.
	fake2 := &fakeTransport{}
	c2 := newClient(t, fake2, "test-app")
	_, err = c2.SubmitEvaluations(context.Background(), []export.EvaluationMetric{{
		SpanID: "s", TraceID: "t", Label: "quality", ScoreValue: ptr(0.75),
	}})
	require.NoError(t, err)
	bare := firstMetric(t, fake2.captured()[0].body)
	assert.NotContains(t, bare, "assessment")
	assert.NotContains(t, bare, "reasoning")
	assert.NotContains(t, bare, "metadata")
}

// TestSubmitEvaluations_WithCallMLApp: the per-call override applies, and a
// per-metric MLApp still wins over it.
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
		{Label: "no-join", ScoreValue: ptr(1.0)},                                                                                                // missing join
		{SpanID: "s", TraceID: "t", TagKey: "k", TagValue: "v", Label: "both", ScoreValue: ptr(1.0)},                                            // both joins
		{SpanID: "s", TraceID: "t", Label: "novalue"},                                                                                           // zero values
		{SpanID: "s", TraceID: "t", Label: "twovalues", ScoreValue: ptr(1.0), BooleanValue: ptr(true)},                                          // two values
		{SpanID: "s", TraceID: "t", Label: "jsonscalarmismatch", MetricType: export.MetricTypeCategorical, JSONValue: map[string]any{"k": "v"}}, // json_value with a scalar metric type
		{SpanID: "s", TraceID: "", Label: "partial", ScoreValue: ptr(1.0)},                                                                      // incomplete span join
		{SpanID: "s", TraceID: "t", Label: "badtype", MetricType: export.MetricType("scores"), ScoreValue: ptr(1.0)},                            // invalid metric type (typo)
		{SpanID: "s", TraceID: "t", Label: "mismatch", MetricType: export.MetricTypeScore, CategoricalValue: ptr("x")},                          // type/value mismatch
		{SpanID: "s", TraceID: "t", Label: "emptyjson", MetricType: export.MetricTypeCategorical, JSONValue: map[string]any{}},                  // empty json value
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
	// Nothing is POSTed: the guard runs before the batch is even validated.
	assert.Empty(t, fake.captured())
	// The rows it never sent are still accounted, so an outbox caller cannot read
	// the result as "delivered" (see the ExportResult accounting invariant).
	require.Len(t, res.Requests, 1)
	assert.ErrorIs(t, res.Requests[0].Err, context.Canceled)
	assert.Equal(t, 0, res.Sent)
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, 1, res.Sent+res.Failed+res.Dropped)
}

// TestSubmitEvaluations_ContextCanceledStopsPromptly mirrors the SubmitSpans
// cancellation guard onto the eval path, which the coverage profile showed was
// never executed despite a comment claiming parity.
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
	assert.Equal(t, 1, res.Sent+res.Failed+res.Dropped)
}

// TestSubmitEvaluations_MidFlightCancelNotRetriable is the eval analogue of
// TestSubmitSpans_MidFlightCancelNotRetriable: a cancel that lands while a
// request is in flight must not be reported as a transient failure, or an outbox
// caller re-enqueues work the caller explicitly abandoned.
func TestSubmitEvaluations_MidFlightCancelNotRetriable(t *testing.T) {
	block := &blockingTransport{entered: make(chan struct{})}
	c, err := export.NewClient("test-app",
		export.WithHTTPClient(&http.Client{Transport: block}),
		export.WithDatadogIntake("datadoghq.com", "test-key"),
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

// TestSubmitSpans_AccountingCoversWholeInputOnCancel locks the ExportResult
// invariant at a batch boundary: the rows in windows the cancel skipped are
// reported as Failed rather than vanishing from the totals.
func TestSubmitSpans_AccountingCoversWholeInputOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	fake := &fakeTransport{responder: func(int, *http.Request) (int, string) {
		once.Do(cancel) // cancel after the first window's POST is recorded
		return 202, "{}"
	}}
	c := newClient(t, fake, "test-app", export.WithSpanBatchSize(2))

	events := make([]export.SpanEvent, 6)
	for i := range events {
		events[i] = export.SpanEvent{TraceID: "t", SpanID: strconv.Itoa(i), Kind: export.KindLLM}
	}
	res, err := c.SubmitSpans(ctx, events)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Len(t, fake.captured(), 1) // only the first window went out
	assert.Equal(t, 2, res.Sent)
	assert.Equal(t, 4, res.Failed) // the 4 rows in the skipped windows
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

	span := allSpans(t, fake.captured()[0].body)[0]
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

	span := allSpans(t, fake.captured()[0].body)
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

	span := allSpans(t, fake.captured()[0].body)
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

	span := allSpans(t, fake.captured()[0].body)[0]
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

	span := allSpans(t, fake.captured()[0].body)[0]
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

	m := allSpans(t, fake.captured()[0].body)[0]["metrics"].(map[string]any)
	assert.Equal(t, float64(10), m["input_tokens"])             // named field wins over Extra
	assert.Equal(t, float64(42), m["billable_character_count"]) // newly-added standard key carried
	assert.Equal(t, 0.25, m["time_to_first_token"])
	assert.Equal(t, float64(7), m["custom_metric"]) // arbitrary reconstructed key not dropped
}

// TestSubmitEvaluations_RejectsUnmarshalableJSON drives the sendEvalBatch encode
// fallback: a metric_type:"json" row whose json_value is not JSON-encodable
// (math.Inf) passes lower() (json value ⟺ json type) and only fails at marshal,
// so it is dropped via dropUnencodableEvals while the batch's other, valid row is
// re-encoded and POSTed. This is the path a type-mismatch row can never reach
// (lower() rejects that earlier — see TestSubmitEvaluations_RejectsTypeValueMismatch).
func TestSubmitEvaluations_RejectsUnmarshalableJSON(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t1", Label: "bad", MetricType: export.MetricTypeJSON, JSONValue: map[string]any{"x": math.Inf(1)}},
		{SpanID: "s2", TraceID: "t2", Label: "ok", ScoreValue: ptr(0.5)},
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 1) // unencodable json_value dropped as a row
	assert.Equal(t, 0, res.ValidationErrors[0].Index)
	assert.Contains(t, res.ValidationErrors[0].Reason, "not JSON-encodable")
	assert.Equal(t, 1, res.Dropped)
	assert.Equal(t, 1, res.Sent)
	assert.Zero(t, res.Failed)

	require.Len(t, fake.captured(), 1) // only the valid row was re-sent
	metrics := decode(t, fake.captured()[0].body)["data"].(map[string]any)["attributes"].(map[string]any)["metrics"].([]any)
	require.Len(t, metrics, 1) // the valid metric still went out
}

// TestSubmitEvaluations_DropsAllUnencodableJSON covers the branch where every row
// in a batch fails to encode: all are dropped via dropUnencodableEvals and no
// request is issued at all (the batch collapses to zero good rows).
func TestSubmitEvaluations_DropsAllUnencodableJSON(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s1", TraceID: "t1", Label: "bad1", MetricType: export.MetricTypeJSON, JSONValue: map[string]any{"x": math.Inf(1)}},
		{SpanID: "s2", TraceID: "t2", Label: "bad2", MetricType: export.MetricTypeJSON, JSONValue: map[string]any{"y": math.Inf(-1)}},
	})
	require.NoError(t, err) // dropped rows are not request failures
	assert.Len(t, res.ValidationErrors, 2)
	assert.Equal(t, 2, res.Dropped)
	assert.Zero(t, res.Sent)
	assert.Zero(t, res.Failed)
	assert.Empty(t, res.Requests) // no good rows left → nothing POSTed
	assert.Empty(t, fake.captured())
}

// TestSubmitEvaluations_RejectsTypeValueMismatch keeps coverage of the lower()
// type-vs-value mismatch guard: a scalar MetricType paired with a JSONValue is
// rejected before any marshal (never reaching dropUnencodableEvals), so it is a
// row-level validation error and nothing is sent.
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
	assert.Empty(t, fake.captured()) // rejected in lower(), never POSTed
}

// TestSubmitSpans_ZeroStartAndDurationOmitFields locks the doc↔wire contract on
// SpanEvent.Start/Duration: both map to omitempty wire fields, so a zero value is
// omitted from the payload, never emitted as a literal 0.
func TestSubmitSpans_ZeroStartAndDurationOmitFields(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	_, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t", SpanID: "s", Kind: export.KindLLM}, // zero Start, zero Duration
	})
	require.NoError(t, err)

	span := allSpans(t, fake.captured()[0].body)[0]
	assert.NotContains(t, span, "start_ns")
	assert.NotContains(t, span, "duration")
}

// TestSubmitSpans_ParentIDPreservedVerbatim guards the caller-assigned-ID
// contract: a non-empty ParentID must reach the wire unchanged (only an empty
// ParentID is normalized to "undefined").
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

// TestSubmitSpans_RetryClassification drives the transport's retry classification
// end-to-end through SubmitSpans: 429 (via the dedicated TooManyRequests clause),
// 408 and 425 (via isRetriableStatus), and a 503 carrying Retry-After (the
// server-advertised-delay branch) are all retried and reported Retriable; other
// 4xx are permanent. Guards against folding the checks together and dropping the
// 429 clause, which would turn a rate-limited backfill into a permanent failure.
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

// TestSubmitSpans_MidFlightCancelNotRetriable guards the "cancelled context is not
// retriable" contract in the transport: a caller cancellation that lands while a
// request is in flight (past SubmitSpans' pre-loop ctx guard, so Post actually
// runs) must report Retriable=false and surface context.Canceled, so an outbox
// caller does not re-enqueue cancelled work.
func TestSubmitSpans_MidFlightCancelNotRetriable(t *testing.T) {
	bt := &blockingTransport{entered: make(chan struct{})}
	c, err := export.NewClient("test-app",
		export.WithHTTPClient(&http.Client{Transport: bt}),
		export.WithDatadogIntake("datadoghq.com", "test-key"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		res *export.ExportResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := c.SubmitSpans(ctx, []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
		done <- outcome{res, err}
	}()

	select {
	case <-bt.entered: // the POST is in flight; cancel mid-request, not pre-flight
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

// TestSubmitSpans_RetriableStatusThenCancelNotRetriable complements the network-
// error case above by covering the transport's post-retry override: a retriable
// 503 is recorded (Retriable=true) and then the caller cancels while the request
// is backing off before the next attempt. The recorded status is retriable, so
// only the "cancelled context is not retriable" override can flip Retriable back
// to false — guarding an outbox caller against re-enqueuing work cancelled
// mid-backoff. The 503 carries a Retry-After so the backoff wait is long enough
// that the cancellation is observed during the wait, not on a fresh attempt.
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
		res *export.ExportResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := c.SubmitSpans(ctx, []export.SpanEvent{{TraceID: "t", SpanID: "s", Kind: export.KindLLM}})
		done <- outcome{res, err}
	}()

	select {
	case <-responded: // a retriable 503 was recorded; cancel during the Retry-After backoff
	case <-time.After(10 * time.Second):
		t.Fatal("transport was never called")
	}
	cancel()

	select {
	case got := <-done:
		require.Error(t, got.err)
		require.Len(t, got.res.Requests, 1)
		assert.Equal(t, 503, got.res.Requests[0].StatusCode)
		assert.False(t, got.res.Requests[0].Retriable) // override cleared the retriable status
	case <-time.After(10 * time.Second):
		t.Fatal("SubmitSpans did not return after cancellation during backoff")
	}
}

// TestSubmitEvaluations_JSONMetricType reproduces Trajectory's range/segment
// markers, which ship metric_type:"json" alongside a json_value object. The
// export API must emit that exact wire shape verbatim (not reject it or relabel
// it as categorical/score/boolean).
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

// TestSubmitEvaluations_JSONMetricTypeRequiresJSONValue guards that metric_type
// json paired with a scalar value (no json_value) is dropped as a row-level
// error rather than emitting a value-less json metric.
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

// TestSubmitSpans_SplitStopsOnCancelBetweenHalves covers a cancel landing inside
// the oversized-batch bisection, the one place the loop guard in SubmitSpans
// cannot see. The right half must not be POSTed, must still be accounted, and the
// call must return an error — an err==nil return here would let an outbox caller
// treat the abandoned span as delivered and drop it permanently.
func TestSubmitSpans_SplitStopsOnCancelBetweenHalves(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	fake := &fakeTransport{responder: func(int, *http.Request) (int, string) {
		once.Do(cancel) // cancel once the first (left-half) POST has been recorded
		return 202, "{}"
	}}
	// Two spans that each fit but together exceed the limit → the batch bisects
	// into one request per span; the cancel lands between the two halves.
	c := newClient(t, fake, "test-app", export.WithMaxSpanPayloadBytes(3000))

	res, err := c.SubmitSpans(ctx, []export.SpanEvent{
		{TraceID: "t1", SpanID: "s1", Kind: export.KindLLM, Input: strings.Repeat("x", 1500)},
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM, Input: strings.Repeat("y", 1500)},
	})
	// A canceled export is an error, not a success.
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	// Only the left half reached the transport; the right half was abandoned.
	assert.Len(t, fake.captured(), 1)
	// The abandoned right-half span is still accounted, so the result covers both
	// inputs and never silently loses s2: the left half sent, the right failed.
	require.Len(t, res.Requests, 2)
	assert.Equal(t, 202, res.Requests[0].StatusCode)
	assert.NoError(t, res.Requests[0].Err)
	assert.ErrorIs(t, res.Requests[1].Err, context.Canceled)
	assert.Equal(t, 2, res.Sent+res.Failed+res.Dropped)
	assert.Equal(t, 1, res.Sent)
	assert.Equal(t, 1, res.Failed)
}
