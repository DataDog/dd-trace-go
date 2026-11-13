// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package aws_lambda

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

const (
	functionName = "test-fn"
	functionARN  = "arn:aws:lambda:us-east-1:123456789012:function:" + functionName
	serviceName  = "lambda-test"
)

// invocation is what the test expects back from one event.
type invocation struct {
	event string
	// body is the expected response body. Ignored when err is set.
	body string
	// err means the runtime receives an error instead of a response.
	err bool
}

// skipUnlessLinux skips tests that build mainapp, which are slow, on
// platforms Lambda doesn't run on.
func skipUnlessLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("AWS Lambda only runs on Linux")
	}
}

func TestLambda(t *testing.T) {
	skipUnlessLinux(t)
	bin := maintest.BuildWithOrchestrion(t, "./aws_lambda/mainapp")

	okTwice := []invocation{{event: `{}`, body: `"ok"`}, {event: `{}`, body: `"ok"`}}
	tests := []struct {
		variant string
		invokes []invocation
		// skipsHandler means aws-lambda-go fails before calling the handler,
		// so the handler's tag and HTTP request are missing.
		skipsHandler bool
	}{
		{variant: "Start", invokes: okTwice},
		{variant: "StartWithContext", invokes: []invocation{{event: `{}`, body: `"from-context"`}}},
		{variant: "StartHandler", invokes: []invocation{{event: `{}`, body: "handler-ok"}}},
		{variant: "StartHandlerWithContext", invokes: []invocation{{event: `{}`, body: "from-context"}}},
		{variant: "StartHandlerFunc", invokes: []invocation{{event: `{}`, body: `"ok"`}}},
		{variant: "NewHandler", invokes: []invocation{{event: `{}`, body: `"ok"`}}},
		{variant: "UseNumber", invokes: []invocation{{event: `{"n":1}`, body: `"json.Number"`}}},
		{variant: "WithContext", invokes: []invocation{{event: `{}`, body: `"from-context"`}}},
		{variant: "DisallowUnknownFields", invokes: []invocation{{event: `{"Other":1}`, err: true}}, skipsHandler: true},
		{variant: "ResponseFormat", invokes: []invocation{{event: `{}`, body: "{\n \"html\": \"\\u003cb\\u003e\"\n}\n"}}},
		{variant: "Stream", invokes: []invocation{{event: `{}`, body: "streamed-body"}}},
		{variant: "Error", invokes: []invocation{{event: `{}`, err: true}}},
		{variant: "DecodeError", invokes: []invocation{{event: `{"N":"not a number"}`, err: true}}, skipsHandler: true},
		{variant: "ErrorThenOK", invokes: []invocation{{event: `{}`, err: true}, {event: `{}`, body: `"ok"`}}},
	}
	for _, tc := range tests {
		t.Run(tc.variant, func(t *testing.T) {
			t.Parallel()
			run := runLambda(t, bin, tc.variant, tc.invokes)
			require.False(t, run.exited, "the app exited:\n%s", run.output)
			requireInvocations(t, run, tc.variant, tc.invokes, !tc.skipsHandler)
		})
	}
}

// TestLambdaExtension runs mainapp as if the Datadog Extension were installed,
// against a fake extension. ddlambda's requests to the extension must not be
// traced: tracing them would report each call as a span, and overwrite the
// trace headers the extension reads with the request span's own context.
func TestLambdaExtension(t *testing.T) {
	skipUnlessLinux(t)
	ext := newFakeExtension(t)
	// The extension counts as running when this file exists.
	marker := filepath.Join(t.TempDir(), "datadog-agent")
	require.NoError(t, os.WriteFile(marker, nil, 0o600))

	const extPkg = "github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/extension"
	bin := maintest.BuildWithOrchestrion(t, "./aws_lambda/mainapp",
		"-ldflags=-X "+extPkg+".extensionURL="+ext.url+" -X "+extPkg+".extensionPath="+marker)

	agent := maintest.NewAgent(t)
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(downstream.Close)
	invokes := []invocation{{event: `{}`, body: `"ok"`}}
	rt := newFakeRuntime("Start", invokes)
	srv := httptest.NewServer(rt)
	t.Cleanup(srv.Close)

	lambdaSpans := func() []*agenttest.Span {
		var out []*agenttest.Span
		for _, s := range agent.Spans() {
			if s.Operation == "aws.lambda" {
				out = append(out, s)
			}
		}
		return out
	}
	stdout, stderr, exited := maintest.ExecUntil(t, bin, []string{
		"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(srv.URL, "http://"),
		"AWS_LAMBDA_FUNCTION_NAME=" + functionName,
		"LAMBDA_VARIANT=Start",
		"LAMBDA_HTTP_URL=" + downstream.URL,
		"DD_SERVICE=" + serviceName,
		"DD_TRACE_STARTUP_LOGS=false",
		// With the extension running, the tracer sends spans to an agent.
		"DD_TRACE_AGENT_URL=" + agent.URL(),
	}, func([]byte, []byte) bool {
		return len(rt.snapshot()) == len(invokes) && len(ext.requests(endInvocationPath)) == 1 && len(lambdaSpans()) == 1
	}, 45*time.Second)
	output := "stdout:\n" + string(stdout) + "\nstderr:\n" + string(stderr)
	require.False(t, exited, "the app exited:\n%s", output)

	starts := ext.requests(startInvocationPath)
	require.Len(t, starts, 1)
	for _, h := range []string{"x-datadog-trace-id", "x-datadog-parent-id", "traceparent"} {
		assert.Empty(t, starts[0].Get(h), "start-invocation carries an injected %s header", h)
	}

	root := lambdaSpans()[0]
	assert.Equal(t, uint64(extTraceID), root.TraceID, "aws.lambda continues the extension's trace")
	assert.Equal(t, uint64(extParentID), root.ParentID)

	end := ext.requests(endInvocationPath)[0]
	assert.Equal(t, strconv.Itoa(extTraceID), end.Get("x-datadog-trace-id"))
	assert.Equal(t, strconv.Itoa(extParentID), end.Get("x-datadog-parent-id"))
	assert.Equal(t, "1", end.Get("x-datadog-sampling-priority"))
	assert.Equal(t, strconv.FormatUint(root.SpanID, 10), end.Get("x-datadog-span-id"))

	var children int
	for _, s := range agent.Spans() {
		assert.NotContains(t, s.Meta["http.url"], ext.url, "a request to the extension was traced")
		assert.NotContains(t, s.Meta["http.url"], srv.URL, "a request to the Runtime API was traced")
		if s.Operation == "http.request" && s.ParentID == root.SpanID && s.Meta["http.url"] == downstream.URL {
			children++
		}
	}
	assert.Equal(t, 1, children, "the handler's own request is still traced")
}

const (
	startInvocationPath = "/lambda/start-invocation"
	endInvocationPath   = "/lambda/end-invocation"

	// The trace context the fake extension returns from start-invocation.
	extTraceID  = 1234567890
	extParentID = 987654321
)

// fakeExtension implements the Datadog Extension routes ddlambda calls, and
// records the headers of each request.
type fakeExtension struct {
	url string

	mu      sync.Mutex
	headers map[string][]http.Header
}

func newFakeExtension(t *testing.T) *fakeExtension {
	t.Helper()
	ext := &fakeExtension{headers: map[string][]http.Header{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		ext.mu.Lock()
		ext.headers[r.URL.Path] = append(ext.headers[r.URL.Path], r.Header.Clone())
		ext.mu.Unlock()
		if r.URL.Path == startInvocationPath {
			w.Header().Set("x-datadog-trace-id", strconv.Itoa(extTraceID))
			w.Header().Set("x-datadog-parent-id", strconv.Itoa(extParentID))
			w.Header().Set("x-datadog-sampling-priority", "1")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	ext.url = srv.URL
	return ext
}

func (e *fakeExtension) requests(path string) []http.Header {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]http.Header(nil), e.headers[path]...)
}

// requireInvocations checks the responses, spans, and enhanced metrics of a
// run that served invokes in order.
func requireInvocations(t *testing.T, run lambdaRun, variant string, invokes []invocation, handlerRuns bool) {
	t.Helper()
	require.Len(t, run.results, len(invokes))
	require.Len(t, run.spansNamed("aws.lambda"), len(invokes), run.output)

	errors := 0
	for i, want := range invokes {
		got := run.results[i]
		require.Equal(t, requestID(variant, i), got.requestID)
		if want.err {
			errors++
			require.True(t, got.err, "invocation %d: want an error, got response %q", i, got.body)
		} else {
			require.False(t, got.err, "invocation %d: want a response, got error %s", i, got.body)
			require.Equal(t, want.body, string(got.body), "invocation %d", i)
		}

		root := run.lambdaSpan(t, requestID(variant, i))
		assert.Equal(t, functionName, root.Resource)
		assert.Equal(t, serviceName, root.Service)
		assert.Equal(t, functionARN, root.Meta["function_arn"])
		assert.Equal(t, strconv.FormatBool(i == 0), root.Meta["cold_start"], "invocation %d", i)
		assert.Equal(t, want.err, root.Error != 0, "invocation %d", i)

		children := run.childrenOf(root, "http.request")
		if handlerRuns {
			assert.Equal(t, variant, root.Meta["lambda_variant"])
			if assert.Len(t, children, 1, "invocation %d", i) {
				assert.Equal(t, run.downstreamURL, children[0].Meta["http.url"])
			}
		} else {
			assert.Empty(t, children, "invocation %d", i)
		}
	}

	wantRequests := 0
	if handlerRuns {
		wantRequests = len(invokes)
	}
	assert.Equal(t, wantRequests, run.downstreamRequests)
	for _, s := range run.spans {
		assert.NotContains(t, s.Meta["http.url"], run.runtimeURL, "a request to the Runtime API was traced")
	}
	assert.Len(t, run.metrics("aws.lambda.enhanced.invocations"), len(invokes))
	assert.Len(t, run.metrics("aws.lambda.enhanced.errors"), errors)
}

func requestID(variant string, i int) string {
	return variant + "-" + strconv.Itoa(i)
}

type result struct {
	requestID string
	body      []byte
	err       bool
}

type span struct {
	TraceID  string            `json:"trace_id"`
	SpanID   string            `json:"span_id"`
	ParentID string            `json:"parent_id"`
	Name     string            `json:"name"`
	Resource string            `json:"resource"`
	Service  string            `json:"service"`
	Error    int32             `json:"error"`
	Meta     map[string]string `json:"meta"`
}

type metric struct {
	Name string   `json:"m"`
	Tags []string `json:"t"`
}

// lambdaRun is what one run of the function produced.
type lambdaRun struct {
	results            []result
	spans              []span
	metricLines        []metric
	output             string
	exited             bool
	downstreamURL      string
	downstreamRequests int
	runtimeURL         string
}

func (r lambdaRun) spansNamed(name string) []span {
	var out []span
	for _, s := range r.spans {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

func (r lambdaRun) lambdaSpan(t *testing.T, requestID string) span {
	t.Helper()
	for _, s := range r.spansNamed("aws.lambda") {
		if s.Meta["request_id"] == requestID {
			return s
		}
	}
	require.FailNow(t, "no aws.lambda span for request "+requestID, r.output)
	return span{}
}

func (r lambdaRun) childrenOf(parent span, name string) []span {
	var out []span
	for _, s := range r.spansNamed(name) {
		if s.TraceID == parent.TraceID && s.ParentID == parent.SpanID {
			out = append(out, s)
		}
	}
	return out
}

func (r lambdaRun) metrics(name string) []metric {
	var out []metric
	for _, m := range r.metricLines {
		if m.Name == name {
			out = append(out, m)
		}
	}
	return out
}

// runLambda runs bin with LAMBDA_VARIANT=variant against a fake Lambda
// Runtime API that serves invokes in order. It stops the function once every
// event has a response or an error and stdout has an aws.lambda span for each,
// or returns early if the function exits. The span check matters: the tracer
// can write a finished span after the response is sent.
func runLambda(t *testing.T, bin, variant string, invokes []invocation) lambdaRun {
	t.Helper()

	var downstreamRequests atomic.Int32
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downstreamRequests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(downstream.Close)

	rt := newFakeRuntime(variant, invokes)
	srv := httptest.NewServer(rt)
	t.Cleanup(srv.Close)

	stdout, stderr, exited := maintest.ExecUntil(t, bin, []string{
		"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(srv.URL, "http://"),
		"AWS_LAMBDA_FUNCTION_NAME=" + functionName,
		"LAMBDA_VARIANT=" + variant,
		"LAMBDA_HTTP_URL=" + downstream.URL,
		"DD_SERVICE=" + serviceName,
		"DD_TRACE_STARTUP_LOGS=false",
		// Send custom metrics to stdout too, instead of the Datadog API.
		"DD_FLUSH_TO_LOG=true",
	}, func(stdout, _ []byte) bool {
		if len(rt.snapshot()) < len(invokes) {
			return false
		}
		spans, _ := parseOutput(stdout)
		n := 0
		for _, s := range spans {
			if s.Name == "aws.lambda" {
				n++
			}
		}
		return n >= len(invokes)
	}, 45*time.Second)

	run := lambdaRun{
		results:            rt.snapshot(),
		output:             "stdout:\n" + string(stdout) + "\nstderr:\n" + string(stderr),
		exited:             exited,
		downstreamURL:      downstream.URL,
		downstreamRequests: int(downstreamRequests.Load()),
		runtimeURL:         srv.URL,
	}
	run.spans, run.metricLines = parseOutput(stdout, stderr)
	return run
}

// parseOutput reads the traces and metrics the function writes as JSON lines.
// With no Datadog Extension, that is where the tracer and ddlambda send them.
func parseOutput(outputs ...[]byte) ([]span, []metric) {
	var spans []span
	var metrics []metric
	for _, out := range outputs {
		for line := range bytes.SplitSeq(out, []byte("\n")) {
			var traces struct {
				Traces [][]span `json:"traces"`
			}
			if bytes.Contains(line, []byte(`"traces"`)) && json.Unmarshal(line, &traces) == nil {
				for _, tr := range traces.Traces {
					spans = append(spans, tr...)
				}
				continue
			}
			var m metric
			if json.Unmarshal(line, &m) == nil && m.Name != "" {
				metrics = append(metrics, m)
			}
		}
	}
	return spans, metrics
}

// fakeRuntime implements the parts of the Lambda Runtime API that
// aws-lambda-go uses: fetching the next event, and posting its response or
// error.
type fakeRuntime struct {
	variant string
	invokes []invocation

	mu      sync.Mutex
	served  int
	results []result
}

func newFakeRuntime(variant string, invokes []invocation) *fakeRuntime {
	return &fakeRuntime{variant: variant, invokes: invokes}
}

func (f *fakeRuntime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/2018-06-01/runtime/invocation/"
	path, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodGet && path == "next" {
		f.mu.Lock()
		i := f.served
		if i < len(f.invokes) {
			f.served++
		}
		f.mu.Unlock()
		if i >= len(f.invokes) {
			// Every event is served: block like the real API until the test
			// stops the function.
			<-r.Context().Done()
			return
		}
		h := w.Header()
		h.Set("Lambda-Runtime-Aws-Request-Id", requestID(f.variant, i))
		h.Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))
		h.Set("Lambda-Runtime-Invoked-Function-Arn", functionARN)
		h.Set("Lambda-Runtime-Trace-Id", "Root=1-00000000-000000000000000000000000")
		_, _ = io.WriteString(w, f.invokes[i].event)
		return
	}

	id, kind, ok := strings.Cut(path, "/")
	if r.Method != http.MethodPost || !ok || (kind != "response" && kind != "error") {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, result{requestID: id, body: body, err: kind == "error"})
}

func (f *fakeRuntime) snapshot() []result {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]result(nil), f.results...)
}
