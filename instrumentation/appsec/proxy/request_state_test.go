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
	writer.armBlockDelivery(context.Background(), func(context.Context, BlockActionOptions) error { return wantErr })

	root := dyngo.NewRootOperation()
	var action *actions.BlockHTTP
	dyngo.OnData(root, func(got *actions.BlockHTTP) { action = got })
	actions.SendActionEvents(root, map[string]any{"block_request": map[string]any{}})
	if action == nil {
		t.Fatal("block_request did not emit an HTTP action")
	}

	action.Handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := actions.CommitBlockResponse(writer); !errors.Is(err, wantErr) {
		t.Fatalf("commit error = %v, want %v", err, wantErr)
	}
	if err := writer.blockResponseError(); !errors.Is(err, wantErr) {
		t.Fatalf("block response error = %v, want %v", err, wantErr)
	}
}

func TestFakeResponseWriterRequiresDelivery(t *testing.T) {
	t.Run("never sent", func(t *testing.T) {
		writer := newFakeResponseWriter()
		writer.armBlockDelivery(context.Background(), func(context.Context, BlockActionOptions) error { return nil })
		if err := writer.blockResponseError(); !errors.Is(err, errBlockResponseNotSent) {
			t.Fatalf("block response error = %v, want %v", err, errBlockResponseNotSent)
		}
	})

	t.Run("no message in flight", func(t *testing.T) {
		writer := newFakeResponseWriter()
		// Armed for one message, then disarmed: a later block cannot be delivered.
		writer.armBlockDelivery(context.Background(), func(context.Context, BlockActionOptions) error { return nil })()
		if err := writer.AppSecCommitBlockResponse(); !errors.Is(err, errBlockResponseNotSent) {
			t.Fatalf("commit error = %v, want %v", err, errBlockResponseNotSent)
		}
		if err := writer.blockResponseError(); !errors.Is(err, errBlockResponseNotSent) {
			t.Fatalf("block response error = %v, want %v", err, errBlockResponseNotSent)
		}
	})

	t.Run("sent once", func(t *testing.T) {
		calls := 0
		writer := newFakeResponseWriter()
		writer.armBlockDelivery(context.Background(), func(context.Context, BlockActionOptions) error {
			calls++
			return nil
		})
		for range 2 {
			if err := writer.AppSecCommitBlockResponse(); err != nil {
				t.Fatalf("commit error = %v, want nil", err)
			}
		}
		if calls != 1 {
			t.Fatalf("block message deliveries = %d, want 1", calls)
		}
		if err := writer.blockResponseError(); err != nil {
			t.Fatalf("block response error = %v, want nil", err)
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
