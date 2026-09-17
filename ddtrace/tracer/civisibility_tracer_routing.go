// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import "github.com/DataDog/dd-trace-go/v2/internal/civisibility"

// installGlobalTracerWithCIVisibilityRouter installs globalTracer unless the
// current CI Visibility router can install it as one of its delegates. Keeping
// the handoff here avoids coupling the normal tracer lifecycle to the router's
// implementation details.
func installGlobalTracerWithCIVisibilityRouter(globalTracer Tracer, ciVisibilityEnabled bool) {
	current := getGlobalTracer()
	state := civisibility.GetState()
	ciVisibilityStarting := ciVisibilityEnabled &&
		(state == civisibility.StateUninitialized || state == civisibility.StateInitializing)
	if ciVisibilityStarting {
		if _, ok := globalTracer.(*ciVisibilityTracerRouter); !ok {
			globalTracer = newCIVisibilityTracerRouter(globalTracer, false)
		}
		if setter, ok := current.(interface{ SetCIVisibilityTracer(Tracer) bool }); ok && setter.SetCIVisibilityTracer(globalTracer) {
			return
		}
	} else {
		// DD_CIVISIBILITY_ENABLED remains set after Test Optimization starts. A
		// later tracer.Start is therefore an application tracer start even though
		// its freshly built config still has CI Visibility enabled. Remove the
		// transient router before installing its concrete tracer as the
		// application delegate.
		if router, ok := globalTracer.(*ciVisibilityTracerRouter); ok {
			globalTracer = router.CIVisibilityTracer()
		}
		if setter, ok := current.(interface{ SetApplicationTracer(Tracer) bool }); ok && setter.SetApplicationTracer(globalTracer) {
			return
		}
	}

	setGlobalTracer(globalTracer)
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
