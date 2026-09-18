// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
)

// setGlobalTracerPreservingCIVisibilityMockTracer installs globalTracer unless the
// current CI Visibility router can install it as one of its delegates. Keeping
// the handoff here avoids coupling the normal tracer lifecycle to the router's
// implementation details.
func setGlobalTracerPreservingCIVisibilityMockTracer(globalTracer Tracer, ciVisibilityEnabled bool) {
	current := getGlobalTracer()
	state := civisibility.GetState()
	ciVisibilityStarting := ciVisibilityEnabled &&
		(state == civisibility.StateUninitialized || state == civisibility.StateInitializing)
	if ciVisibilityStarting {
		if setter, ok := current.(interface{ SetCIVisibilityTracer(Tracer) bool }); ok {
			candidate := globalTracer
			if _, ok := candidate.(*ciVisibilityTracerRouter); !ok {
				candidate = newCIVisibilityTracerRouter(candidate, false)
			}
			if setter.SetCIVisibilityTracer(candidate) {
				return
			}
		}
	} else {
		// DD_CIVISIBILITY_ENABLED remains set after Test Optimization starts. A
		// later tracer.Start is therefore an application tracer start even though
		// its freshly built config still has CI Visibility enabled. Remove the
		// transient router before installing its concrete tracer as the
		// application delegate.
		if router, ok := globalTracer.(*ciVisibilityTracerRouter); ok {
			globalTracer = router.ciVisibilityTracer()
		}
		if setter, ok := current.(interface{ SetApplicationTracer(Tracer) bool }); ok && setter.SetApplicationTracer(globalTracer) {
			return
		}
		if ciTracer, ok := current.(*tracer); ok && state == civisibility.StateInitialized {
			router := newCIVisibilityTracerRouter(ciTracer, false)
			router.SetApplicationTracer(globalTracer)
			storeCIVisibilityRouterWithoutStoppingCurrent(router)
			return
		}
	}

	setGlobalTracer(globalTracer)
}

func storeCIVisibilityRouterWithoutStoppingCurrent(router *ciVisibilityTracerRouter) {
	internal.StoreGlobalTracer[*ciVisibilityTracerRouter, Tracer](router)
}

// attachMockTracerToCIVisibility installs mockTracer as a temporary ordinary-
// span destination. It is linked from mocktracer to keep this lifecycle hook
// private to the CI Visibility implementation.
func attachMockTracerToCIVisibility(mockTracer Tracer) Tracer {
	if mockTracer == nil {
		return nil
	}
	current := getGlobalTracer()
	if router, ok := current.(*ciVisibilityTracerRouter); ok {
		if router.SetMockTracer(mockTracer) {
			return router
		}
		return nil
	}
	state := civisibility.GetState()
	if state != civisibility.StateInitializing && state != civisibility.StateInitialized {
		return nil
	}
	ciTracer, ok := current.(*tracer)
	if !ok {
		return nil
	}
	router := newCIVisibilityTracerRouter(ciTracer, false)
	if !router.SetMockTracer(mockTracer) {
		return nil
	}
	storeCIVisibilityRouterWithoutStoppingCurrent(router)
	return router
}

// submitTracerForFinishedChunk returns the concrete tracer that should receive
// a finished chunk for the current global tracer snapshot.
func submitTracerForFinishedChunk(globalTracer Tracer, spans []*Span) Tracer {
	if provider, ok := globalTracer.(interface {
		TracerForFinishedChunk([]*Span) (Tracer, bool)
	}); ok {
		if submitTracer, ok := provider.TracerForFinishedChunk(spans); ok {
			return submitTracer
		}
		return nil
	}
	return globalTracer
}
