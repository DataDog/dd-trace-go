package llmobs

import "context"

type (
	CtxKeyPropagatedMLApp    struct{}
	CtxKeyPropagatedParentID struct{}
	CtxKeyPropagatedTraceID  struct{}
)

func PropagatedMLAppFromContext(ctx context.Context) (string, bool) {
	if val, ok := ctx.Value(CtxKeyPropagatedMLApp{}).(string); ok {
		return val, true
	}
	return "", false
}

func PropagatedParentIDFromContext(ctx context.Context) (string, bool) {
	if val, ok := ctx.Value(CtxKeyPropagatedParentID{}).(string); ok {
		return val, true
	}
	return "", false
}

func PropagatedTraceIDFromContext(ctx context.Context) (string, bool) {
	if val, ok := ctx.Value(CtxKeyPropagatedTraceID{}).(string); ok {
		return val, true
	}
	return "", false
}
