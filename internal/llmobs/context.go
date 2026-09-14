// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import "context"

type (
	ctxKeyActiveLLMSpan     struct{}
	ctxKeyPropagatedLLMSpan struct{}
)

// PropagatedLLMSpan represents LLMObs span context that can be propagated across process boundaries.
type PropagatedLLMSpan struct {
	// MLApp is the ML application name.
	MLApp string
	// TraceID is the LLMObs trace ID.
	TraceID string
	// SpanID is the span ID.
	SpanID string
	// SessionID is the session ID.
	SessionID string
	// ParentAgentName is the name of the nearest agent ancestor, propagated across
	// process boundaries. Empty when the upstream hop sent an id-only attribution.
	ParentAgentName string
	// ParentAgentSpanID is the span ID of the nearest agent ancestor, propagated
	// across process boundaries. Empty when there is no agent ancestor.
	ParentAgentSpanID string
}

// PropagatedLLMSpanFromContext retrieves a PropagatedLLMSpan from the context.
// Returns the span and true if found, nil and false otherwise.
func PropagatedLLMSpanFromContext(ctx context.Context) (*PropagatedLLMSpan, bool) {
	if val, ok := ctx.Value(ctxKeyPropagatedLLMSpan{}).(*PropagatedLLMSpan); ok {
		return val, true
	}
	return nil, false
}

// ContextWithPropagatedLLMSpan returns a new context with the given PropagatedLLMSpan attached.
func ContextWithPropagatedLLMSpan(ctx context.Context, span *PropagatedLLMSpan) context.Context {
	return context.WithValue(ctx, ctxKeyPropagatedLLMSpan{}, span)
}

// ActiveLLMSpanFromContext retrieves the active LLMObs span from the context.
// Returns the span and true if found, nil and false otherwise.
func ActiveLLMSpanFromContext(ctx context.Context) (*Span, bool) {
	if span, ok := ctx.Value(ctxKeyActiveLLMSpan{}).(*Span); ok {
		return span, true
	}
	return nil, false
}

func contextWithActiveLLMSpan(ctx context.Context, span *Span) context.Context {
	return context.WithValue(ctx, ctxKeyActiveLLMSpan{}, span)
}

// AgentNameWireSafe reports whether name can safely be written as a propagating-tag value.
//
//   - reject any byte outside the printable ASCII range [0x20, 0x7E]
//   - reject comma (x-datadog-tags entry delimiter)
//   - reject semicolon and tilde (W3C tracestate characters that composeTracestate sanitizes
//     to "_", which would corrupt the attribution name seen by the downstream service)
//
// Note: dd-trace-js does not reject semicolons or tildes — it relies on the x-datadog-tags
// encoder alone. Go applies the stricter rule because names may also travel via W3C tracestate.
//
// The length check is delegated to callers.
func AgentNameWireSafe(name string) bool {
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b < 0x20 || b > 0x7E {
			return false
		}
		if b == ',' || b == ';' || b == '~' {
			return false
		}
	}
	return true
}
