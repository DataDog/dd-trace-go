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

type PropagatedLLMSpan struct {
	MLApp   string
	TraceID string
	SpanID  string
}

func PropagatedLLMSpanFromContext(ctx context.Context) (*PropagatedLLMSpan, bool) {
	if val, ok := ctx.Value(ctxKeyPropagatedLLMSpan{}).(*PropagatedLLMSpan); ok {
		return val, true
	}
	return nil, false
}

func ContextWithPropagatedLLMSpan(ctx context.Context, span *PropagatedLLMSpan) context.Context {
	return context.WithValue(ctx, ctxKeyPropagatedLLMSpan{}, span)
}

func ActiveLLMSpanFromContext(ctx context.Context) (*Span, bool) {
	if span, ok := ctx.Value(ctxKeyActiveLLMSpan{}).(*Span); ok {
		return span, true
	}
	return nil, false
}

func contextWithActiveLLMSpan(ctx context.Context, span *Span) context.Context {
	return context.WithValue(ctx, ctxKeyActiveLLMSpan{}, span)
}
