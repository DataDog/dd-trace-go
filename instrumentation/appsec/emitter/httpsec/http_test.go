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

// newBlockMetricsOperation returns a handler operation with request metrics in
// which the WAF requested a block.
func newBlockMetricsOperation() (*HandlerOperation, *appsecwaf.ContextMetrics) {
	contextOp, _ := appsecwaf.StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	handleMetrics := appsecwaf.NewMetricsInstance(nil, "test")
	metrics := handleMetrics.NewContextMetrics()
	contextOp.SetMetricsInstance(metrics)
	metrics.SetBlockRequested()
	return &HandlerOperation{ContextOperation: contextOp}, metrics
}

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

func newBlockAction() *actions.BlockHTTP {
	return actions.NewBlockAction(map[string]any{})[0].(*actions.BlockHTTP)
}

// TestAppliedBlockHasPrecedenceOverLaterFailure checks that a block that was
// applied is still reported as blocked when a later action fails.
func TestAppliedBlockHasPrecedenceOverLaterFailure(t *testing.T) {
	client := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(client)()
	op, metrics := newBlockMetricsOperation()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if !applyBlockAction(op, newBlockAction(), httptest.NewRecorder(), req, nil) {
		t.Fatal("first block was not applied")
	}
	// Simulate a later block action that arrives after the response was sent.
	started := &committedResponseWriter{header: make(http.Header), status: http.StatusForbidden, committed: true}
	applyBlockAction(op, newBlockAction(), started, req, nil)
	metrics.Submit(libddwaf.Truncations{}, nil)

	if got := wafRequestsCount(client, true, false); got != 1 {
		t.Fatalf("waf.requests request_blocked:true block_failure:false = %v, want 1", got)
	}
	if got := wafRequestsCount(client, false, true); got != 0 {
		t.Fatalf("waf.requests request_blocked:false block_failure:true = %v, want 0", got)
	}
}

// TestPanickingBlockCallbackReportsFailure checks that a block callback that
// panics is reported as failed, and that the panic is not hidden.
func TestPanickingBlockCallbackReportsFailure(t *testing.T) {
	client := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(client)()
	op, metrics := newBlockMetricsOperation()
	action := newBlockAction()

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the block callback panic was not propagated")
			}
		}()
		applyBlockAction(op, action, httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), []func(){func() { panic("callback failed") }})
	}()
	if action.Handler != nil {
		t.Fatal("block action was not consumed")
	}
	metrics.Submit(libddwaf.Truncations{}, nil)

	if got := wafRequestsCount(client, false, true); got != 1 {
		t.Fatalf("waf.requests request_blocked:false block_failure:true = %v, want 1", got)
	}
}

// TestPanickingBlockResponseReportsFailure checks that a block response that
// panics is reported as failed, and that the panic is not hidden.
func TestPanickingBlockResponseReportsFailure(t *testing.T) {
	client := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(client)()
	op, metrics := newBlockMetricsOperation()
	action := newBlockAction()

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the block response panic was not propagated")
			}
		}()
		applyBlockAction(op, action, &panickingResponseWriter{header: make(http.Header)}, httptest.NewRequest(http.MethodGet, "/", nil), nil)
	}()
	if action.Handler != nil {
		t.Fatal("block action was not consumed")
	}
	metrics.Submit(libddwaf.Truncations{}, nil)

	if got := wafRequestsCount(client, false, true); got != 1 {
		t.Fatalf("waf.requests request_blocked:false block_failure:true = %v, want 1", got)
	}
	if got := wafRequestsCount(client, true, false); got != 0 {
		t.Fatalf("waf.requests request_blocked:true block_failure:false = %v, want 0", got)
	}
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
