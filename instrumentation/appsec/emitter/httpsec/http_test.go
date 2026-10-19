// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package httpsec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/DataDog/go-libddwaf/v5"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	appsecwaf "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// panickingResponseWriter panics when the block response is written.
type panickingResponseWriter struct {
	header http.Header
}

func (w *panickingResponseWriter) Header() http.Header { return w.header }

func (*panickingResponseWriter) Write([]byte) (int, error) { panic("write failed") }

func (*panickingResponseWriter) WriteHeader(int) { panic("write failed") }

// headerPanickingResponseWriter panics when its headers are read.
type headerPanickingResponseWriter struct{ panickingResponseWriter }

func (*headerPanickingResponseWriter) Header() http.Header { panic("header failed") }

// wafRequestsCount returns the waf.requests count with the given block outcome.
func wafRequestsCount(client *telemetrytest.RecordClient, requestBlocked, blockFailure bool) float64 {
	return client.Count(telemetry.NamespaceAppSec, "waf.requests", []string{
		"request_blocked:" + strconv.FormatBool(requestBlocked),
		"block_failure:" + strconv.FormatBool(blockFailure),
		"rule_triggered:false",
		"waf_timeout:false",
		"rate_limited:false",
		"waf_error:false",
		"input_truncated:false",
		"event_rules_version:test",
		"waf_version:" + libddwaf.Version(),
	}).Get()
}

// requireBlockOutcome checks that client recorded exactly one waf.requests
// count, with the given block outcome.
func requireBlockOutcome(t *testing.T, client *telemetrytest.RecordClient, requestBlocked, blockFailure bool) {
	t.Helper()
	for _, outcome := range []struct{ requestBlocked, blockFailure bool }{
		{false, false},
		{true, false},
		{false, true},
		{true, true},
	} {
		var want float64
		if outcome.requestBlocked == requestBlocked && outcome.blockFailure == blockFailure {
			want = 1
		}
		if got := wafRequestsCount(client, outcome.requestBlocked, outcome.blockFailure); got != want {
			t.Errorf("waf.requests request_blocked:%v block_failure:%v = %v, want %v", outcome.requestBlocked, outcome.blockFailure, got, want)
		}
	}
}

// wafAction describes an action that the test WAF returns.
type wafAction struct {
	// actionType is the WAF action type, such as block_request.
	actionType string
	// params are the WAF action parameters.
	params map[string]any
	// reported is true for a WAF-scope block_request, whose outcome is the
	// waf.requests block outcome. It is false for a redirect or a RASP block.
	reported bool
}

var (
	wafBlock    = wafAction{actionType: "block_request", params: map[string]any{}, reported: true}
	raspBlock   = wafAction{actionType: "block_request", params: map[string]any{}}
	wafRedirect = wafAction{actionType: "redirect_request", params: map[string]any{"location": "/blocked"}}
)

// emit sends the action like the WAF listener does when the WAF returns it.
func (a wafAction) emit(t *testing.T, op *HandlerOperation) {
	t.Helper()
	if a.reported {
		op.ContextOperation.GetMetricsInstance().SetBlockRequested()
	}
	if !actions.SendActionEvents(op, map[string]any{a.actionType: a.params}, actions.Config{ReportBlockOutcome: a.reported}) && a.actionType == "block_request" {
		t.Fatalf("%s action was not built", a.actionType)
	}
}

// blockTest serves one request through WrapHandler. The test WAF returns
// onRequest when the request starts, and onResponse when the handler finishes.
// Like the WAF listener, it submits the request metrics when the WAF context
// finishes.
type blockTest struct {
	onRequest  []wafAction
	onResponse []wafAction
	onBlock    []func()
}

// serve serves a request on w. It returns true when the protected handler ran,
// and the value of the panic that the request raised, if any.
func (bt blockTest) serve(t *testing.T, w http.ResponseWriter) (handlerRan bool, panicked any) {
	t.Helper()
	root := dyngo.NewRootOperation()
	dyngo.SwapRootOperation(root)
	t.Cleanup(func() { dyngo.SwapRootOperation(nil) })

	contextFinished := false
	dyngo.On(root, func(op *appsecwaf.ContextOperation, _ appsecwaf.ContextArgs) {
		handleMetrics := appsecwaf.NewMetricsInstance(nil, "test")
		op.SetMetricsInstance(handleMetrics.NewContextMetrics())
	})
	dyngo.OnFinish(root, func(op *appsecwaf.ContextOperation, _ appsecwaf.ContextRes) {
		contextFinished = true
		op.GetMetricsInstance().Submit(libddwaf.Truncations{}, nil)
	})
	dyngo.On(root, func(op *HandlerOperation, _ HandlerOperationArgs) {
		for _, a := range bt.onRequest {
			a.emit(t, op)
		}
	})
	dyngo.OnFinish(root, func(op *HandlerOperation, _ HandlerOperationRes) {
		for _, a := range bt.onResponse {
			a.emit(t, op)
		}
	})

	handler := WrapHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		handlerRan = true
	}), tracelib.NoopTagSetter{}, &Config{OnBlock: bt.onBlock})
	func() {
		defer func() { panicked = recover() }()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	if !contextFinished {
		t.Fatal("the WAF context was not finished, so the request metrics were not submitted")
	}
	return handlerRan, panicked
}

// TestAppliedBlockHasPrecedenceOverLaterFailure checks that a block that was
// applied is still reported as blocked when a later block fails.
func TestAppliedBlockHasPrecedenceOverLaterFailure(t *testing.T) {
	client := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(client)()

	// The response-data block arrives after the request block response was sent.
	w := &committedResponseWriter{header: make(http.Header)}
	handlerRan, panicked := blockTest{onRequest: []wafAction{wafBlock}, onResponse: []wafAction{wafBlock}}.serve(t, w)
	if panicked != nil {
		t.Fatalf("unexpected panic: %v", panicked)
	}
	if handlerRan {
		t.Fatal("the blocked handler ran")
	}
	if w.status != http.StatusForbidden {
		t.Fatalf("response status = %d, want %d", w.status, http.StatusForbidden)
	}

	requireBlockOutcome(t, client, true, false)
}

// TestOtherBlockDoesNotHideFailedWAFBlock checks that a redirect or a RASP
// block that was applied does not report a later WAF block as applied when
// that WAF block fails.
func TestOtherBlockDoesNotHideFailedWAFBlock(t *testing.T) {
	for name, applied := range map[string]wafAction{"redirect": wafRedirect, "rasp block": raspBlock} {
		t.Run(name, func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(client)()

			// The WAF block arrives after the first response was sent, so it fails.
			w := &committedResponseWriter{header: make(http.Header)}
			handlerRan, panicked := blockTest{onRequest: []wafAction{applied}, onResponse: []wafAction{wafBlock}}.serve(t, w)
			if panicked != nil {
				t.Fatalf("unexpected panic: %v", panicked)
			}
			if handlerRan {
				t.Fatal("the blocked handler ran")
			}

			requireBlockOutcome(t, client, false, true)
		})
	}
}

// TestPanickingEarlyBlockFinishesRequest checks that a request block that
// panics is reported as failed, that the request metrics are still submitted,
// and that the panic is not hidden.
func TestPanickingEarlyBlockFinishesRequest(t *testing.T) {
	for _, tc := range []struct {
		name      string
		w         http.ResponseWriter
		onBlock   []func()
		wantPanic string
	}{
		{
			name:      "block callback",
			w:         httptest.NewRecorder(),
			onBlock:   []func(){func() { panic("callback failed") }},
			wantPanic: "callback failed",
		},
		{
			name:      "block response",
			w:         &panickingResponseWriter{header: make(http.Header)},
			wantPanic: "write failed",
		},
		{
			// The finalization must not read the headers again.
			name:      "block response headers",
			w:         &headerPanickingResponseWriter{},
			wantPanic: "header failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(client)()

			handlerRan, panicked := blockTest{onRequest: []wafAction{wafBlock}, onBlock: tc.onBlock}.serve(t, tc.w)
			if panicked != tc.wantPanic {
				t.Fatalf("recovered panic = %v, want %q", panicked, tc.wantPanic)
			}
			if handlerRan {
				t.Fatal("the blocked handler ran")
			}

			requireBlockOutcome(t, client, false, true)
		})
	}
}

// TestPanickingLateBlockFinishesRequest checks the same for a block that the
// response data produced, which afterHandle applies.
func TestPanickingLateBlockFinishesRequest(t *testing.T) {
	client := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(client)()

	handlerRan, panicked := blockTest{
		onResponse: []wafAction{wafBlock},
		onBlock:    []func(){func() { panic("callback failed") }},
	}.serve(t, httptest.NewRecorder())
	if panicked != "callback failed" {
		t.Fatalf("recovered panic = %v, want %q", panicked, "callback failed")
	}
	if !handlerRan {
		t.Fatal("the handler did not run before the response block")
	}

	requireBlockOutcome(t, client, false, true)
}

type committedResponseWriter struct {
	header    http.Header
	status    int
	committed bool
}

type writtenResponseWriter struct {
	http.ResponseWriter
	written bool
}

func (*writtenResponseWriter) Status() int {
	return http.StatusOK
}

func (w *writtenResponseWriter) Written() bool {
	return w.written
}

func (w *committedResponseWriter) Header() http.Header {
	return w.header
}

func (w *committedResponseWriter) Write(b []byte) (int, error) {
	w.committed = true
	return len(b), nil
}

func (w *committedResponseWriter) WriteHeader(status int) {
	w.status = status
	w.committed = true
}

func (w *committedResponseWriter) Status() int {
	return w.status
}

func (w *committedResponseWriter) Committed() bool {
	return w.committed
}

func TestCommittedResponseStillInterruptsBlockedRequest(t *testing.T) {
	contextOp, _ := appsecwaf.StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	op := &HandlerOperation{ContextOperation: contextOp}
	action := actions.NewBlockAction(map[string]any{})[0].(*actions.BlockHTTP)
	w := &committedResponseWriter{header: make(http.Header), status: http.StatusOK, committed: true}
	aborted := false

	handled := applyBlockAction(op, action, w, httptest.NewRequest(http.MethodGet, "/", nil), []func(){func() { aborted = true }})
	if !handled {
		t.Fatal("committed response allowed the protected handler to run")
	}
	if !aborted {
		t.Fatal("committed response did not run the block callback")
	}
	if action.Handler != nil {
		t.Fatal("failed block action was not consumed")
	}
}

func TestFinishConsumesUnappliedBlock(t *testing.T) {
	op, block, _ := StartOperation(context.Background(), HandlerOperationArgs{}, tracelib.NoopTagSetter{})
	if blocked := actions.SendActionEvents(op, map[string]any{
		"block_request": map[string]any{},
	}); !blocked {
		t.Fatal("valid block action did not request interruption")
	}
	if action := block.Load(); action == nil || action.Handler == nil {
		t.Fatal("block action was not tracked")
	}

	// Finish reports the block as failed, which consumes its handler so no later
	// caller can apply it after the request telemetry was submitted.
	op.Finish(HandlerOperationRes{})
	if action := block.Load(); action == nil || action.Handler != nil {
		t.Fatal("unapplied block action was not consumed")
	}
}

func TestResponseStartedPrefersCommitted(t *testing.T) {
	w := &committedResponseWriter{header: make(http.Header), status: http.StatusOK}
	if responseStarted(w) {
		t.Fatal("pre-seeded status must not imply that response headers were sent")
	}

	w.WriteHeader(http.StatusOK)
	if !responseStarted(w) {
		t.Fatal("committed response was not detected")
	}
}

func TestResponseStartedSupportsWritten(t *testing.T) {
	w := &writtenResponseWriter{ResponseWriter: httptest.NewRecorder()}
	if responseStarted(w) {
		t.Fatal("pre-seeded status must not imply that response headers were sent")
	}

	w.written = true
	if !responseStarted(w) {
		t.Fatal("written response was not detected")
	}
}
