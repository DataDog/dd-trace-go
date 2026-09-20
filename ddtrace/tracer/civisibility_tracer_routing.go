// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal"
	globalinternal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
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
		candidate := globalTracer
		if _, ok := candidate.(*ciVisibilityTracerRouter); !ok {
			candidate = newCIVisibilityTracerRouter(candidate, false)
		}
		if setter, ok := current.(interface{ SetCIVisibilityTracer(Tracer) bool }); ok {
			if setter.SetCIVisibilityTracer(candidate) {
				return
			}
		}
		setGlobalTracer(candidate)
		return
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

// startOptionsForCIVisibilityLifecycle keeps later tracer.Start calls aligned
// with their router role. Once CI Visibility is initialized, a new tracer is an
// application delegate even when the process-level enablement variable remains
// set for the test instrumentation.
func startOptionsForCIVisibilityLifecycle(opts []StartOption) []StartOption {
	if civisibility.GetState() != civisibility.StateInitialized {
		return opts
	}
	applicationOpts := append([]StartOption(nil), opts...)
	return append(applicationOpts, func(c *config) {
		c.internalConfig.SetCIVisibilityEnabled(false, internalconfig.OriginCode)
	})
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

type ciVisibilityTraceRouter interface {
	TracerForTrace(string, string) Tracer
}

type tracerStatsdClientProvider interface {
	tracerStatsdClient() globalinternal.StatsdClient
}

func (t *tracer) tracerStatsdClient() globalinternal.StatsdClient {
	return t.statsd
}

// tracerStatsdClient returns the client for the active ordinary tracer. Mock
// tracers do not own tracer health metrics; without an application tracer the
// CI tracer remains the owner.
func (t *ciVisibilityTracerRouter) tracerStatsdClient() globalinternal.StatsdClient {
	t.delegatesMu.RLock()
	target := t.applicationTracer
	if target == nil {
		target = t.Tracer
	}
	t.delegatesMu.RUnlock()
	if provider, ok := target.(tracerStatsdClientProvider); ok {
		return provider.tracerStatsdClient()
	}
	return nil
}

func statsdClientForTracer(t Tracer) globalinternal.StatsdClient {
	if provider, ok := t.(tracerStatsdClientProvider); ok {
		return provider.tracerStatsdClient()
	}
	return nil
}

// concreteTracerForTrace returns the current concrete tracer for the routing
// type recorded on the local trace. Tracers without CI Visibility routing
// continue to use the process-global tracer without reading trace metadata.
func concreteTracerForTrace(globalTracer Tracer, localTrace *trace, fallbackSpanType string) Tracer {
	router, ok := globalTracer.(ciVisibilityTraceRouter)
	if !ok {
		return globalTracer
	}
	return concreteTracerForType(router, ciVisibilityTracerType(localTrace), fallbackSpanType)
}

// concreteTracerForLockedTrace resolves routing while the caller already owns
// localTrace.mu. Tracers without CI Visibility routing return before reading
// trace metadata, preserving the normal finish hot path.
func concreteTracerForLockedTrace(globalTracer Tracer, localTrace *trace, fallbackSpanType string) Tracer {
	router, ok := globalTracer.(ciVisibilityTraceRouter)
	if !ok {
		return globalTracer
	}
	var tracerType string
	if localTrace != nil {
		tracerType = localTrace.tags[ciVisibilityTracerTypeTag] // +checklocksignore — Caller holds localTrace.mu; checklocks does not propagate locks across this helper.
	}
	return concreteTracerForType(router, tracerType, fallbackSpanType)
}

// concreteTracerForSpan avoids reading span or trace metadata unless CI
// Visibility routing is active. This keeps the normal tracer hot path
// unchanged.
func concreteTracerForSpan(globalTracer Tracer, span *Span) Tracer {
	router, ok := globalTracer.(ciVisibilityTraceRouter)
	if !ok || span == nil {
		return globalTracer
	}
	var localTrace *trace
	if span.context != nil {
		localTrace = span.context.trace
	}
	return concreteTracerForType(router, ciVisibilityTracerType(localTrace), span.spanTypeForRouting())
}

func concreteTracerForType(router ciVisibilityTraceRouter, tracerType, fallbackSpanType string) Tracer {
	return router.TracerForTrace(tracerType, fallbackSpanType)
}
