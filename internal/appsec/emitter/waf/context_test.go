// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package waf

import (
	"context"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	appsectrace "github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
)

func TestContextOperationActionConfig(t *testing.T) {
	op := new(ContextOperation)
	op.SetStackTraceConfig(config.StackTraceConfig{Disabled: true, MaxDepth: 17})

	if got, want := op.actionConfig(), (actions.Config{StackTraceDisabled: true, StackTraceDepth: 17}); got != want {
		t.Fatalf("actionConfig() = %#v, want %#v", got, want)
	}
}

func TestBlockingUnavailableFromContext(t *testing.T) {
	ctx := ContextWithBlockingUnavailable(context.Background())
	op, _ := StartContextOperation(ctx, tracelib.NoopTagSetter{})
	if !op.BlockingUnavailable() {
		t.Fatal("ContextOperation did not preserve blocking-unavailable context")
	}
}

func TestSendActionEventsStackTraceConfig(t *testing.T) {
	op, _ := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	var received []*actions.StackTraceAction
	dyngo.OnData(op, func(action *actions.StackTraceAction) {
		received = append(received, action)
	})
	actionData := map[string]any{
		"generate_stack": map[string]any{"stack_id": "stack-id"},
	}

	op.SetStackTraceConfig(config.StackTraceConfig{Disabled: true})
	actions.SendActionEvents(op, actionData, op.actionConfig())
	if len(received) != 0 {
		t.Fatalf("received %d stack-trace actions while disabled, want 0", len(received))
	}

	op.SetStackTraceConfig(config.StackTraceConfig{MaxDepth: 1})
	actions.SendActionEvents(op, actionData, op.actionConfig())
	if len(received) != 1 {
		t.Fatalf("received %d stack-trace actions while enabled, want 1", len(received))
	}
	if depth := len(received[0].Event.Frames); depth != 1 {
		t.Fatalf("captured stack depth = %d, want 1", depth)
	}
}

func TestContextOperationFinishClearsServiceEntryGLS(t *testing.T) {
	t.Cleanup(orchestrion.MockGLS())

	const iterations = 1000
	baseline := orchestrion.GLSStackDepth()

	for range iterations {
		op, _ := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
		op.Finish()

		if depth := orchestrion.GLSStackDepth(); depth != baseline {
			t.Fatalf("GLS depth after ContextOperation.Finish() = %d, want baseline %d", depth, baseline)
		}
	}
}

func TestFinishedServiceEntrySpanDoesNotReceiveChildDataEvents(t *testing.T) {
	t.Cleanup(orchestrion.MockGLS())

	root := dyngo.NewRootOperation()
	dyngo.SwapRootOperation(root)
	t.Cleanup(func() { dyngo.SwapRootOperation(nil) })

	transport, err := appsectrace.NewAppsecSpanTransport(nil, root)
	if err != nil {
		t.Fatalf("failed to register AppSec span transport: %v", err)
	}
	t.Cleanup(transport.Stop)

	tags := newRecordingTagSetter()
	op, _ := StartContextOperation(context.Background(), tags)
	op.Finish()
	child := dyngo.NewOperation(op.ServiceEntrySpanOperation)

	dyngo.EmitData(child, tracelib.ServiceEntrySpanTag{
		Key:   "after.finish",
		Value: "should-not-be-seen",
	})

	if _, ok := tags.Get("after.finish"); ok {
		t.Fatal("finished service-entry operation still received data events from a child operation")
	}
}

func TestAbsorbDerivativesFirstWriteWins(t *testing.T) {
	op := &ContextOperation{}

	op.AbsorbDerivatives(map[string]any{"appsec.api.redirection.move_target": "/first", "count": 1})
	op.AbsorbDerivatives(map[string]any{"appsec.api.redirection.move_target": "/second", "count": 2, "added": true})

	got := op.Derivatives()
	if v := got["appsec.api.redirection.move_target"]; v != "/first" {
		t.Errorf("move_target = %v, want /first (first write must win)", v)
	}
	if v := got["count"]; v != 1 {
		t.Errorf("count = %v, want 1 (first write must win)", v)
	}
	if v, ok := got["added"]; !ok || v != true {
		t.Errorf("added = %v (present=%v), want true (a new key must still be absorbed)", v, ok)
	}
}

func TestAbsorbDerivativesBlockedResponseSchemaStillSkipped(t *testing.T) {
	op, _ := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	op.SetRequestBlocked()

	op.AbsorbDerivatives(map[string]any{
		"_dd.appsec.s.res.body":              "schema",
		"appsec.api.redirection.move_target": "/first",
	})

	got := op.Derivatives()
	if _, ok := got["_dd.appsec.s.res.body"]; ok {
		t.Error("response schema derivative must be skipped when the request is blocked")
	}
	if v := got["appsec.api.redirection.move_target"]; v != "/first" {
		t.Errorf("move_target = %v, want /first (non-schema derivatives must still be absorbed when blocked)", v)
	}
}

type recordingTagSetter struct {
	mu   sync.Mutex
	tags map[string]any
}

func newRecordingTagSetter() *recordingTagSetter {
	return &recordingTagSetter{tags: make(map[string]any)}
}

func (r *recordingTagSetter) SetTag(key string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tags[key] = value
}

func (r *recordingTagSetter) Get(key string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.tags[key]
	return value, ok
}

// TestAddEventsCap checks that AddEvents accepts no new batch once the request has 10 stored events,
// and that it never calls the limiter.
func TestAddEventsCap(t *testing.T) {
	t.Run("single-events", func(t *testing.T) {
		lim := new(countingLimiter)
		lim.deny.Store(true)
		op := &ContextOperation{limiter: lim}

		for range 20 {
			op.AddEvents("e")
		}

		if got := lim.calls.Load(); got != 0 {
			t.Errorf("limiter calls = %d, want 0", got)
		}
		if got := len(op.Events()); got != maxWAFEventsPerRequest {
			t.Errorf("stored events = %d, want %d", got, maxWAFEventsPerRequest)
		}
	})

	t.Run("batches", func(t *testing.T) {
		lim := new(countingLimiter)
		op := &ContextOperation{limiter: lim}

		op.AddEvents(1, 2, 3, 4, 5, 6, 7, 8, 9)
		// The cap works on the stored count: the batch of 2 is stored as a whole.
		op.AddEvents("a", "b")
		if got := len(op.Events()); got != 11 {
			t.Fatalf("stored events = %d, want 11", got)
		}
		op.AddEvents("c")
		if got := len(op.Events()); got != 11 {
			t.Errorf("stored events after the cap = %d, want 11 (no new batch is accepted)", got)
		}
		if got := lim.calls.Load(); got != 0 {
			t.Errorf("limiter calls = %d, want 0", got)
		}
	})

	t.Run("no-events", func(t *testing.T) {
		op := &ContextOperation{limiter: new(countingLimiter)}
		op.AddEvents()
		if got := len(op.Events()); got != 0 {
			t.Errorf("stored events = %d, want 0", got)
		}
	})
}

func TestAddEventsConcurrent(t *testing.T) {
	const goroutines = 64
	op := &ContextOperation{limiter: new(countingLimiter)}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			<-start
			op.AddEvents("e")
		})
	}
	close(start)
	wg.Wait()

	if got := len(op.Events()); got != maxWAFEventsPerRequest {
		t.Errorf("stored events = %d, want %d", got, maxWAFEventsPerRequest)
	}
}

func TestAllowKeep(t *testing.T) {
	t.Run("one-admission-per-request", func(t *testing.T) {
		lim := new(countingLimiter)
		op := &ContextOperation{limiter: lim}

		for i := range 5 {
			if !op.allowKeep() {
				t.Fatalf("allowKeep() #%d = false, want true", i)
			}
		}
		if got := lim.calls.Load(); got != 1 {
			t.Errorf("limiter calls = %d, want 1", got)
		}
	})

	t.Run("denied-calls-the-limiter-each-time", func(t *testing.T) {
		lim := new(countingLimiter)
		lim.deny.Store(true)
		op := &ContextOperation{limiter: lim}

		for i := range 3 {
			if op.allowKeep() {
				t.Fatalf("allowKeep() #%d = true, want false", i)
			}
		}
		if got := lim.calls.Load(); got != 3 {
			t.Errorf("limiter calls = %d, want 3", got)
		}

		lim.deny.Store(false)
		if !op.allowKeep() || !op.allowKeep() {
			t.Fatal("allowKeep() = false after the limiter allows, want true")
		}
		if got := lim.calls.Load(); got != 4 {
			t.Errorf("limiter calls = %d, want 4 (no call after the first admission)", got)
		}
	})
}

func TestAllowKeepConcurrent(t *testing.T) {
	const goroutines = 64
	lim := new(countingLimiter)
	op := &ContextOperation{limiter: lim}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			<-start
			if !op.allowKeep() {
				t.Error("allowKeep() = false, want true")
			}
		})
	}
	close(start)
	wg.Wait()

	if got := lim.calls.Load(); got != 1 {
		t.Errorf("limiter calls = %d, want 1", got)
	}
}
