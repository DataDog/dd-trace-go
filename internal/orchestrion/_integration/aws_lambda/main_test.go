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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestLambda(t *testing.T) {
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
		{variant: "Metric", invokes: []invocation{{event: `{}`, body: `"ok"`}}},
	}
	for _, tc := range tests {
		t.Run(tc.variant, func(t *testing.T) {
			t.Parallel()
			run := runLambda(t, bin, tc.variant, tc.invokes)
			require.False(t, run.exited, "the app exited:\n%s", run.output)
			requireInvocations(t, run, tc.variant, tc.invokes, !tc.skipsHandler)
			if tc.variant == "Metric" {
				m := run.metrics("lambda.test.metric")
				require.Len(t, m, 1, run.output)
				require.Contains(t, m[0].Tags, "variant:metric")
			}
		})
	}
}

func TestLambdaNoRPC(t *testing.T) {
	bin := maintest.BuildWithOrchestrion(t, "./aws_lambda/mainapp", "-tags", "lambda.norpc")
	invokes := []invocation{{event: `{}`, body: `"ok"`}}
	run := runLambda(t, bin, "Start", invokes)
	require.False(t, run.exited, "the app exited:\n%s", run.output)
	requireInvocations(t, run, "Start", invokes, true)
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
// event has a response or an error, or returns early if the function exits.
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
	}, rt.done, 45*time.Second)

	run := lambdaRun{
		results:            rt.snapshot(),
		output:             "stdout:\n" + string(stdout) + "\nstderr:\n" + string(stderr),
		exited:             exited,
		downstreamURL:      downstream.URL,
		downstreamRequests: int(downstreamRequests.Load()),
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
	done    chan struct{}

	mu      sync.Mutex
	served  int
	results []result
}

func newFakeRuntime(variant string, invokes []invocation) *fakeRuntime {
	return &fakeRuntime{variant: variant, invokes: invokes, done: make(chan struct{})}
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
	if len(f.results) == len(f.invokes) {
		close(f.done)
	}
}

func (f *fakeRuntime) snapshot() []result {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]result(nil), f.results...)
}
