// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package httpsec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	appsecwaf "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
)

type writtenResponseWriter struct {
	header  http.Header
	status  int
	written bool
}

func (w *writtenResponseWriter) Header() http.Header {
	return w.header
}

func (w *writtenResponseWriter) Write(b []byte) (int, error) {
	w.written = true
	return len(b), nil
}

func (w *writtenResponseWriter) WriteHeader(status int) {
	w.status = status
	w.written = true
}

func (w *writtenResponseWriter) Status() int {
	return w.status
}

func (w *writtenResponseWriter) Written() bool {
	return w.written
}

func TestCommittedResponseStillInterruptsBlockedRequest(t *testing.T) {
	contextOp, _ := appsecwaf.StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	op := &HandlerOperation{ContextOperation: contextOp}
	action := actions.NewBlockAction(map[string]any{})[0].(*actions.BlockHTTP)
	w := &writtenResponseWriter{header: make(http.Header), status: http.StatusOK, written: true}
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

func TestFinishReportsUnappliedBlock(t *testing.T) {
	op, block, _ := StartOperation(context.Background(), HandlerOperationArgs{}, tracelib.NoopTagSetter{})
	failed := 0
	cfg := actions.Config{}.WithBlockRequestOutcome(nil, func() { failed++ })
	if blocked := actions.SendActionEvents(op, map[string]any{
		"block_request": map[string]any{},
	}, cfg); !blocked {
		t.Fatal("valid block action did not request interruption")
	}
	if action := block.Load(); action == nil || action.Handler == nil {
		t.Fatal("block action was not tracked")
	}

	op.Finish(HandlerOperationRes{})
	if failed != 1 {
		t.Fatalf("failed callback count = %d, want 1", failed)
	}
	if action := block.Load(); action == nil || action.Handler != nil {
		t.Fatal("unapplied block action was not consumed")
	}
}

func TestResponseStartedPrefersWritten(t *testing.T) {
	w := &writtenResponseWriter{header: make(http.Header), status: http.StatusOK}
	if responseStarted(w) {
		t.Fatal("pre-seeded status must not imply that response headers were sent")
	}

	w.WriteHeader(http.StatusOK)
	if !responseStarted(w) {
		t.Fatal("committed response was not detected")
	}
}
