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

// stopGlobalTracerPreservingCIVisibility keeps CI spans routable while an
// application delegate drains. The global tracer may be the router itself or
// its public mock handle; both support clearing the application delegate.
func stopGlobalTracerPreservingCIVisibility() {
	state := civisibility.GetState()
	if state == civisibility.StateInitializing || state == civisibility.StateInitialized {
		current := getGlobalTracer()
		if setter, ok := current.(interface{ SetApplicationTracer(Tracer) bool }); ok && setter.SetApplicationTracer(nil) {
			// The router must stay alive if a new mock adopts it while the
			// application drains. Only stop the captured public mock handle.
			if _, isRouter := current.(*ciVisibilityTracerRouter); !isRouter {
				current.Stop()
			}
			return
		}
	}
	setGlobalTracer(&NoopTracer{})
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
	// Delegate calls stay outside delegatesMu so they can re-enter the router.
	return statsdClientForTracer(target)
}

func statsdClientForTracer(t Tracer) globalinternal.StatsdClient {
	if provider, ok := t.(tracerStatsdClientProvider); ok {
		return provider.tracerStatsdClient()
	}
	return nil
}

// concreteTracerForTrace returns the current concrete tracer for the routing
// type recorded on the local trace. The caller must not hold localTrace.mu:
// this helper takes and releases its read lock to read the routing marker.
// It never locks the span, so Span.finish can call it while holding span.mu.
//
// Keep it separate from concreteTracerForLockedTrace: re-locking a trace already
// locked by the caller can deadlock, while skipping the lock here would race.
// Tracers without CI Visibility routing use the process-global tracer without
// reading trace metadata or acquiring either metadata lock.
func concreteTracerForTrace(globalTracer Tracer, localTrace *trace, fallbackSpanType string) Tracer {
	router, ok := globalTracer.(ciVisibilityTraceRouter)
	if !ok {
		return globalTracer
	}
	return router.TracerForTrace(ciVisibilityTracerType(localTrace), fallbackSpanType)
}

// concreteTracerForSpanContext routes without reading mutable span fields.
// Format can run while finish holds the span lock, so it must not fall back to
// reading the span type and re-enter that lock. Like concreteTracerForTrace,
// it requires the caller not to hold context.trace.mu.
func concreteTracerForSpanContext(globalTracer Tracer, context *SpanContext) Tracer {
	if context == nil {
		return globalTracer
	}
	return concreteTracerForTrace(globalTracer, context.trace, "")
}

// concreteTracerForLockedTrace resolves routing while the caller already owns
// localTrace.mu. It neither acquires nor releases that lock and never locks a
// span. trace.finishedOneLocked calls it with span.mu then trace.mu held, passing
// the span type read under span.mu; calling concreteTracerForTrace there would
// try to re-lock trace.mu and deadlock.
// Tracers without CI Visibility routing return before reading trace metadata,
// preserving the normal finish hot path.
func concreteTracerForLockedTrace(globalTracer Tracer, localTrace *trace, fallbackSpanType string) Tracer {
	router, ok := globalTracer.(ciVisibilityTraceRouter)
	if !ok {
		return globalTracer
	}
	var tracerType string
	if localTrace != nil {
		tracerType = localTrace.tags[ciVisibilityTracerTypeTag] // +checklocksignore — Caller holds localTrace.mu; checklocks does not propagate locks across this helper.
	}
	return router.TracerForTrace(tracerType, fallbackSpanType)
}

// concreteTracerForSpan avoids reading span or trace metadata unless CI
// Visibility routing is active. This keeps the normal tracer hot path
// unchanged. The caller must hold neither span.mu nor span.context.trace.mu.
// With CI routing active, each metadata read takes and releases its own read
// lock; neither is held by this helper when it asks the router for a delegate.
func concreteTracerForSpan(globalTracer Tracer, span *Span) Tracer {
	router, ok := globalTracer.(ciVisibilityTraceRouter)
	if !ok || span == nil {
		return globalTracer
	}
	var localTrace *trace
	if span.context != nil {
		localTrace = span.context.trace
	}
	return router.TracerForTrace(ciVisibilityTracerType(localTrace), span.spanTypeForRouting())
}

// spanTypeForRouting returns the current span type for selecting a concrete
// tracer when the caller does not already hold s.mu.
func (s *Span) spanTypeForRouting() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.spanType
}
