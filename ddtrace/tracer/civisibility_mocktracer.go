// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

// setGlobalTracerPreservingCIVisibilityMockTracer installs globalTracer unless
// the current global tracer can keep ownership and route CI Visibility spans to it.
func setGlobalTracerPreservingCIVisibilityMockTracer(globalTracer Tracer, ciVisibilityEnabled bool) {
	if ciVisibilityEnabled {
		if current, ok := getGlobalTracer().(interface{ SetCIVisibilityTracer(Tracer) bool }); ok && current.SetCIVisibilityTracer(globalTracer) {
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
