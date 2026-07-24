// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

// swapGlobalTracerPreservingCIVisibilityMockTracer installs globalTracer
// without stopping the displaced tracer. A CI Visibility mock may preserve
// process-global ownership and return only its displaced real delegate.
func swapGlobalTracerPreservingCIVisibilityMockTracer(globalTracer Tracer, ciVisibilityEnabled bool) Tracer {
	if ciVisibilityEnabled {
		current := getGlobalTracer()
		if swapper, ok := current.(interface {
			SwapCIVisibilityTracer(Tracer) (Tracer, bool)
		}); ok {
			if old, accepted := swapper.SwapCIVisibilityTracer(globalTracer); accepted {
				return old
			}
		} else if setter, ok := current.(interface{ SetCIVisibilityTracer(Tracer) bool }); ok && setter.SetCIVisibilityTracer(globalTracer) {
			// Backwards-compatible fallback for third-party wrappers. Such a
			// wrapper owns any displaced delegate's lifecycle.
			return nil
		}
	}
	return swapGlobalTracer(globalTracer)
}

// setGlobalTracerPreservingCIVisibilityMockTracer preserves the historical
// synchronous replacement behavior for callers outside the startup handoff.
func setGlobalTracerPreservingCIVisibilityMockTracer(globalTracer Tracer, ciVisibilityEnabled bool) {
	if old := swapGlobalTracerPreservingCIVisibilityMockTracer(globalTracer, ciVisibilityEnabled); old != nil {
		old.Stop()
	}
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
