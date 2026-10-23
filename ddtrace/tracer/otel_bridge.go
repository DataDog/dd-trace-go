// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import "context"

// These functions are used by github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry via
// go:linkname. Native code paths of the tracer do not call them.

const (
	bridgeOpen     = 0
	bridgePinned   = 1
	bridgeReleased = 2
	bridgePinnedOK = 3
)

// spanContextWithOwner records s as the owner of sc and returns sc. The woven spanStart calls it
// before the span is shared. Tests call it to make a span borrowable in builds without Orchestrion.
func spanContextWithOwner(sc *SpanContext, s *Span) *SpanContext {
	sc.bridgeOwner = s
	return sc
}

// borrowSpanFromContext returns the span that ctx carries through ContextWithSpan, and pins it:
// the span pool does not reuse a pinned span. It returns nil when ctx has no snapshot, when the
// span has no owner, when the owner is exclude, or when the span pool already released the span.
func borrowSpanFromContext(ctx context.Context, exclude *Span) (*Span, *SpanContext) {
	sc := spanContextSnapshot(ctx)
	if sc == nil || sc.bridgeOwner == nil || sc.bridgeOwner == exclude {
		return nil, nil
	}
	// Most lookups find the span already pinned. The load avoids an atomic write for them.
	switch sc.bridgeState.Load() {
	case bridgePinned, bridgePinnedOK:
	case bridgeOpen:
		if !sc.bridgeState.CompareAndSwap(bridgeOpen, bridgePinned) && sc.bridgeState.Load() == bridgeReleased {
			return nil, nil
		}
	default:
		return nil, nil
	}
	return sc.bridgeOwner, sc
}
