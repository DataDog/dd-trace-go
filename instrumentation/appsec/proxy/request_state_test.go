// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
)

func TestFakeResponseWriterReportsBlockMessageFailure(t *testing.T) {
	wantErr := errors.New("block message failure")
	writer := newFakeResponseWriter()
	writer.setBlockMessageFunc(func(context.Context, BlockActionOptions) error {
		return wantErr
	})
	writer.enableBlockMessages(context.Background())

	root := dyngo.NewRootOperation()
	var action *actions.BlockHTTP
	dyngo.OnData(root, func(got *actions.BlockHTTP) { action = got })
	applied := 0
	failed := 0
	cfg := actions.Config{}.WithBlockRequestOutcome(func() { applied++ }, func() { failed++ })
	actions.SendActionEvents(root, map[string]any{"block_request": map[string]any{}}, cfg)
	if action == nil {
		t.Fatal("block_request did not emit an HTTP action")
	}

	action.Handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
	if applied != 0 || failed != 1 {
		t.Fatalf("outcome callbacks = (applied %d, failed %d), want (0, 1)", applied, failed)
	}
	sent, err := writer.blockResponseResult()
	if !sent || !errors.Is(err, wantErr) {
		t.Fatalf("block response result = (%v, %v), want (true, %v)", sent, err, wantErr)
	}
}

func TestBlockResponseErrorRequiresDelivery(t *testing.T) {
	reqState := RequestState{fakeResponseWriter: newFakeResponseWriter()}
	if err := blockResponseError(&reqState); !errors.Is(err, errBlockResponseNotSent) {
		t.Fatalf("block response error = %v, want %v", err, errBlockResponseNotSent)
	}
}

func TestFakeResponseWriterRejectsUnavailableCommit(t *testing.T) {
	t.Run("missing function", func(t *testing.T) {
		writer := newFakeResponseWriter()
		writer.enableBlockMessages(context.Background())
		err := writer.AppSecCommitBlockResponse()
		if !errors.Is(err, errBlockMessageFuncUnavailable) {
			t.Fatalf("commit error = %v, want %v", err, errBlockMessageFuncUnavailable)
		}
		sent, resultErr := writer.blockResponseResult()
		if sent || !errors.Is(resultErr, errBlockMessageFuncUnavailable) {
			t.Fatalf("block response result = (%v, %v), want (false, %v)", sent, resultErr, errBlockMessageFuncUnavailable)
		}
	})

	t.Run("outside message processing", func(t *testing.T) {
		writer := newFakeResponseWriter()
		writer.setBlockMessageFunc(func(context.Context, BlockActionOptions) error { return nil })
		err := writer.AppSecCommitBlockResponse()
		if !errors.Is(err, errBlockResponseNotDeliverable) {
			t.Fatalf("commit error = %v, want %v", err, errBlockResponseNotDeliverable)
		}
		sent, resultErr := writer.blockResponseResult()
		if sent || !errors.Is(resultErr, errBlockResponseNotDeliverable) {
			t.Fatalf("block response result = (%v, %v), want (false, %v)", sent, resultErr, errBlockResponseNotDeliverable)
		}
	})
}

func TestCloseAfterCloseBeforeResponseDoesNotFinalizeResponse(t *testing.T) {
	finalized := 0
	state := RequestState{
		Mu:      new(sync.Mutex),
		Context: context.Background(),
		State:   MessageTypeRequestHeaders,
		afterHandle: func() {
			finalized++
		},
	}

	state.CloseBeforeResponse()
	if err := state.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if finalized != 0 {
		t.Fatalf("afterHandle called %d times, want 0", finalized)
	}
}
