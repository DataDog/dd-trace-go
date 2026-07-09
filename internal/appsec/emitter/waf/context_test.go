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
	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	appsectrace "github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
)

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
	op := &ContextOperation{}
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
