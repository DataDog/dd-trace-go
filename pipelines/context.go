package pipelines

import (
	"context"
)

type contextKey struct{}

var activePipelineKey = contextKey{}

// ToContext returns a copy of the given context which includes the pipeline p.
func ToContext(ctx context.Context, p Pipeline) context.Context {
	return context.WithValue(ctx, activePipelineKey, p)
}

// FromContext returns the pipeline contained in the given context.
func FromContext(ctx context.Context) (p Pipeline, ok bool) {
	if ctx == nil {
		return Pipeline{}, false
	}
	v := ctx.Value(activePipelineKey)
	if s, ok := v.(Pipeline); ok {
		return s, true
	}
	return Pipeline{}, false
}

// SetCheckpoint sets a checkpoint on the pipeline in the context.
func SetCheckpoint(ctx context.Context, edge string) (Pipeline, context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	p, ok := FromContext(ctx)
	if ok {
		p = p.SetCheckpoint(edge)
	} else {
		// skip the edge if there is nothing before this node.
		p = New()
	}
	ctx = ToContext(ctx, p)
	return p, ctx
}

// MergeContexts returns the first context with a pipeline that is the combination of the pipelines
// from all the contexts.
func MergeContexts(ctxs ...context.Context) context.Context {
	if len(ctxs) == 0 {
		return context.Background()
	}
	pipelines := make([]Pipeline, 0, len(ctxs))
	for _, ctx := range ctxs {
		if p, ok := FromContext(ctx); ok {
			pipelines = append(pipelines, p)
		}
	}
	if len(pipelines) == 0 {
		return ctxs[0]
	}
	return ToContext(ctxs[0], Merge(pipelines))
}
