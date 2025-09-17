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

func ContextWithActiveLLMSpan(ctx context.Context, span *Span) context.Context {
	return context.WithValue(ctx, ctxKeyActiveLLMSpan{}, span)
}
