// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCall is a call identity, like connect's unary requests and CallInfo.
type testCall struct{ n int }

func TestSameCall(t *testing.T) {
	first, second := &testCall{1}, &testCall{1}
	var nilCall *testCall
	slice := []int{1}
	for _, test := range []struct {
		name string
		a, b any
		want bool
	}{
		{name: "both nil", want: true},
		{name: "nil and a call", b: first},
		{name: "a call and nil", a: first},
		{name: "the same call", a: first, b: first, want: true},
		{name: "equal calls", a: first, b: second},
		// connect.v2 identifies a server call that has no CallInfo by a nil *CallInfo.
		{name: "nil pointers of one type", a: nilCall, b: nilCall, want: true},
		{name: "a nil pointer and nil", a: nilCall},
		{name: "a nil pointer and a call", a: nilCall, b: first},
		{name: "pointers of different types", a: first, b: new(int)},
		{name: "equal values", a: testCall{1}, b: testCall{1}},
		{name: "uncomparable values", a: slice, b: slice},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, sameCall(test.a, test.b))
		})
	}
}

func TestClaimCall(t *testing.T) {
	call := &testCall{1}
	for _, side := range sides {
		t.Run(side.name, func(t *testing.T) {
			t.Run("claims a new call", func(t *testing.T) {
				startMockTracer(t)
				ctx := context.Background()
				claimedCtx, traced := newTestConfig().ClaimCall(ctx, side.component, unaryProcedure, call)
				assert.True(t, traced)
				assert.Equal(t, ctx, claimedCtx)
			})

			t.Run("leaves an untraced procedure untraced for inner interceptors", func(t *testing.T) {
				startMockTracer(t)
				cfg := newTestConfig(func(c *Config) { c.SetUntracedMethods(MethodSet(unaryProcedure)) })
				ctx, traced := cfg.ClaimCall(context.Background(), side.component, unaryProcedure, call)
				assert.False(t, traced)
				_, traced = newTestConfig().ClaimCall(ctx, side.component, unaryProcedure, call)
				assert.False(t, traced)
				assert.False(t, cfg.PassedThroughTraced(ctx, side.component, unaryProcedure, call))
				_, traced = cfg.ClaimCall(context.Background(), side.component, bidiProcedure, nil)
				assert.True(t, traced)
			})

			for _, outer := range []struct {
				name   string
				handle func(t *testing.T, ctx context.Context, cfg *Config, id any) context.Context
			}{
				{
					name: "with a call span",
					handle: func(t *testing.T, ctx context.Context, cfg *Config, id any) context.Context {
						span, ctx := cfg.StartCallSpan(ctx, side.component, Spec{Procedure: unaryProcedure}, &connectProtocol, id)
						t.Cleanup(func() { span.Finish() })
						return ctx
					},
				},
				{
					name: "without a call span",
					handle: func(_ *testing.T, ctx context.Context, cfg *Config, id any) context.Context {
						return cfg.MarkHandled(ctx, side.component, unaryProcedure, id)
					},
				},
			} {
				t.Run("passes through a call that an outer interceptor traces "+outer.name, func(t *testing.T) {
					startMockTracer(t)
					cfg := newTestConfig()
					parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
					defer parent.Finish()
					for _, id := range []any{call, nil} {
						handled := outer.handle(t, ctx, cfg, id)
						claimedCtx, traced := cfg.ClaimCall(handled, side.component, unaryProcedure, id)
						assert.False(t, traced)
						assert.Equal(t, handled, claimedCtx)
						assert.True(t, cfg.PassedThroughTraced(handled, side.component, unaryProcedure, id))
					}
				})
			}

			t.Run("claims a call that the outer interceptor does not handle", func(t *testing.T) {
				startMockTracer(t)
				cfg := newTestConfig()
				otherSide := instrumentation.ComponentClient
				if side.component == instrumentation.ComponentClient {
					otherSide = instrumentation.ComponentServer
				}
				span, ctx := cfg.StartCallSpan(context.Background(), side.component, Spec{Procedure: unaryProcedure}, &connectProtocol, call)
				defer span.Finish()
				child, childCtx := tracer.StartSpanFromContext(ctx, "child")
				defer child.Finish()
				for _, test := range []struct {
					name      string
					ctx       context.Context
					side      instrumentation.Component
					procedure string
					id        any
				}{
					{name: "another procedure", ctx: ctx, side: side.component, procedure: bidiProcedure, id: call},
					{name: "another call", ctx: ctx, side: side.component, procedure: unaryProcedure, id: &testCall{1}},
					{name: "a stream", ctx: ctx, side: side.component, procedure: unaryProcedure},
					// An in-process server's context derives from its client's.
					{name: "the other side", ctx: ctx, side: otherSide, procedure: unaryProcedure, id: call},
					{name: "another active span", ctx: childCtx, side: side.component, procedure: unaryProcedure, id: call},
				} {
					_, traced := cfg.ClaimCall(test.ctx, test.side, test.procedure, test.id)
					assert.True(t, traced, test.name)
					assert.False(t, cfg.PassedThroughTraced(test.ctx, test.side, test.procedure, test.id), test.name)
				}
			})

			// The integrations of both connect major versions may run in one process, with calls of
			// one nested in calls of the other.
			t.Run("keeps the calls of another library apart", func(t *testing.T) {
				startMockTracer(t)
				cfg := newTestConfig()
				other := NewConfig(testInstr[instrumentation.PackageConnectRPCV2], instrumentation.PackageConnectRPCV2, new(Library))
				other.SetUntracedMethods(MethodSet(bidiProcedure))
				other.Init()
				span, ctx := cfg.StartCallSpan(context.Background(), side.component, Spec{Procedure: unaryProcedure}, &connectProtocol, call)
				defer span.Finish()
				_, traced := other.ClaimCall(ctx, side.component, unaryProcedure, call)
				assert.True(t, traced)
				// An untraced call of the other library, which keeps the active span, doesn't hide the
				// outer call from this library's interceptors.
				ctx, traced = other.ClaimCall(ctx, side.component, bidiProcedure, nil)
				require.False(t, traced)
				_, traced = cfg.ClaimCall(ctx, side.component, unaryProcedure, call)
				assert.False(t, traced)
			})
		})
	}
}

func TestMarkHandled(t *testing.T) {
	startMockTracer(t)
	cfg := newTestConfig()
	parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	defer parent.Finish()
	ctx = cfg.MarkHandled(ctx, instrumentation.ComponentServer, bidiProcedure, nil)
	marker := cfg.handledCall(ctx, instrumentation.ComponentServer, bidiProcedure, nil)
	require.NotNil(t, marker)
	assert.Equal(t, callMarker{procedure: bidiProcedure, span: parent, traced: true}, *marker)
}

// TestCallIdentityAllocs checks that call identities and context keys are compared and boxed
// without allocating.
func TestCallIdentityAllocs(t *testing.T) {
	startMockTracer(t)
	cfg := newTestConfig()
	call := &testCall{1}
	var id any = call
	span, handled := cfg.StartCallSpan(context.Background(), instrumentation.ComponentClient, Spec{Procedure: unaryProcedure}, &connectProtocol, id)
	defer span.Finish()
	assert.Zero(t, testing.AllocsPerRun(100, func() {
		_, _ = cfg.ClaimCall(context.Background(), instrumentation.ComponentClient, unaryProcedure, id)
		_, _ = cfg.ClaimCall(handled, instrumentation.ComponentClient, unaryProcedure, id)
		_ = cfg.PassedThroughTraced(handled, instrumentation.ComponentClient, unaryProcedure, id)
	}))
	// The marker and the context.
	assert.Equal(t, 2.0, testing.AllocsPerRun(100, func() {
		_ = cfg.MarkHandled(context.Background(), instrumentation.ComponentServer, unaryProcedure, id)
	}))
}
