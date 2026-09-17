// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

// setGlobalTracerPreservingCIVisibilityMockTracer installs globalTracer unless
// the current global tracer can keep ownership and route it to the appropriate
// CI Visibility or application delegate.
//
// The historical name is retained because tracer.Start already calls this
// helper. Keeping the handoff here avoids coupling the normal tracer lifecycle
// to CI Visibility's process-global wrapper.
func setGlobalTracerPreservingCIVisibilityMockTracer(globalTracer Tracer, ciVisibilityEnabled bool) {
	current := getGlobalTracer()
	if ciVisibilityEnabled {
		if setter, ok := current.(interface{ SetCIVisibilityTracer(Tracer) bool }); ok && setter.SetCIVisibilityTracer(globalTracer) {
			return
		}
	} else {
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

		// A CI span may have been started before a CI-aware mock tracer was
		// installed, so it is absent from the mock's per-span registry. The span
		// type is available here without adding ownership state to the core Span.
		if len(spans) > 0 && isCIVisibilitySpanType(spans[0].spanType) {
			if ciProvider, ok := globalTracer.(interface {
				CIVisibilityTracer() Tracer
			}); ok {
				if ciTracer := ciProvider.CIVisibilityTracer(); ciTracer != nil {
					return ciTracer
				}
			}
		}
		return nil
	}
	return globalTracer
}
