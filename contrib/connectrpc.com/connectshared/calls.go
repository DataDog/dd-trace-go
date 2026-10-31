// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"
	"reflect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// The context keys of the calls that a side handles. They hold the Library, so that interceptors
// only see the calls of interceptors of the same connect major version, even when they are nested
// in each other's. A struct with a single pointer field is boxed without allocating.
type (
	clientCallKey struct{ lib *Library }
	serverCallKey struct{ lib *Library }
)

// callKey returns the context key of the calls that side handles. Per-side keys keep servers
// whose context derives from an in-memory or in-process client's traced.
func (c *Config) callKey(side instrumentation.Component) any {
	if side == instrumentation.ComponentClient {
		return clientCallKey{lib: c.lib}
	}
	return serverCallKey{lib: c.lib}
}

// callMarker identifies a call that an interceptor handles, with or without a call span, so that
// inner interceptors on the same side don't trace it again.
type callMarker struct {
	procedure string
	// id identifies the call among the calls to procedure; see ClaimCall.
	id any
	// span is the call span, or the span that was active when the call was handled without one.
	span *tracer.Span
	// traced is false for untraced procedures.
	traced bool
}

// ClaimCall reports whether to trace a call to procedure on side. id identifies the call: a
// pointer that every interceptor of a chain sees for it, such as connect's unary request or
// CallInfo, or nil if there is none, as for connect's streams. ClaimCall does not claim the call
// if an outer interceptor on that side already handles it, or if procedure is untraced: the
// returned context is then marked so that inner interceptors leave the call untraced too.
func (c *Config) ClaimCall(ctx context.Context, side instrumentation.Component, procedure string, id any) (context.Context, bool) {
	if c.handledCall(ctx, side, procedure, id) != nil {
		return ctx, false
	}
	if _, untraced := c.untracedMethods[procedure]; untraced {
		return c.markHandled(ctx, side, callMarker{procedure: procedure, id: id}), false
	}
	return ctx, true
}

// MarkHandled marks ctx as tracing the call to procedure that id identifies (see ClaimCall) on
// side, without a call span.
func (c *Config) MarkHandled(ctx context.Context, side instrumentation.Component, procedure string, id any) context.Context {
	return c.markHandled(ctx, side, callMarker{procedure: procedure, id: id, traced: true})
}

// PassedThroughTraced reports whether an outer interceptor on side traces the call to procedure
// that id identifies, which ClaimCall then passes through.
func (c *Config) PassedThroughTraced(ctx context.Context, side instrumentation.Component, procedure string, id any) bool {
	marker := c.handledCall(ctx, side, procedure, id)
	return marker != nil && marker.traced
}

// markHandled marks ctx as handling the call described by marker on side. A nil marker.span is
// replaced with the active span.
func (c *Config) markHandled(ctx context.Context, side instrumentation.Component, marker callMarker) context.Context {
	if marker.span == nil {
		marker.span, _ = tracer.SpanFromContext(ctx)
	}
	return context.WithValue(ctx, c.callKey(side), &marker)
}

// handledCall returns the marker of an outer interceptor on side that handles this call: same
// procedure, same id, and the span it recorded is still active.
func (c *Config) handledCall(ctx context.Context, side instrumentation.Component, procedure string, id any) *callMarker {
	marker, ok := ctx.Value(c.callKey(side)).(*callMarker)
	if !ok || marker.procedure != procedure || !sameCall(marker.id, id) {
		return nil
	}
	if active, _ := tracer.SpanFromContext(ctx); active != marker.span {
		return nil
	}
	return marker
}

// sameCall reports whether a and b identify the same call, or are both nil. Only pointers
// identify a call: ids of other dynamic types are never considered equal, since comparing them
// could panic.
func sameCall(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.ValueOf(a).Kind() == reflect.Pointer && a == b
}
