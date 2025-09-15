package llmobs

import "context"

type (
	ctxKeyPropagatedMLApp          struct{}
	ctxKeyPropagatedLLMObsParentID struct{}
	ctxKeyPropagatedLLMObsTraceID  struct{}
)

func WithPropagatedMLApp(ctx context.Context, s string) context.Context {
	return context.WithValue(ctx, ctxKeyPropagatedMLApp{}, s)
}

func PropagatedMLAppFromContext(ctx context.Context) (string, bool) {
	if val, ok := ctx.Value(ctxKeyPropagatedMLApp{}).(string); ok {
		return val, true
	}
	return "", false
}

func PropagatedLLMObsParentIDFromContext(ctx context.Context) (string, bool) {
	if val, ok := ctx.Value(ctxKeyPropagatedLLMObsParentID{}).(string); ok {
		return val, true
	}
	return "", false
}

func PropagatedLLMObsTraceIDFromContext(ctx context.Context) (string, bool) {
	if val, ok := ctx.Value(ctxKeyPropagatedLLMObsTraceID{}).(string); ok {
		return val, true
	}
	return "", false
}
