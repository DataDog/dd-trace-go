package pipelines

import (
	"context"
)

type contextKey struct{}

var activePipelineKey = contextKey{}

// ContextWithPipeline returns a copy of the given context which includes the pipeline p.
func ContextWithPipeline(ctx context.Context, p Pipeline) context.Context {
	return context.WithValue(ctx, activePipelineKey, p)
}

// PipelineFromContext returns the pipeline contained in the given context.
func PipelineFromContext(ctx context.Context) (p Pipeline, ok bool) {
	if ctx == nil {
		return Pipeline{}, false
	}
	v := ctx.Value(activePipelineKey)
	if s, ok := v.(Pipeline); ok {
		return s, true
	}
	return Pipeline{}, false
}

// SetCheckpointOnContext sets a checkpoint on the pipeline in the context.
func SetCheckpointOnContext(ctx context.Context, edgeName string) (Pipeline, context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	p, ok := PipelineFromContext(ctx)
	if ok {
		p = p.SetCheckpoint(edgeName)
	} else {
		// skip edgeName if there is nothing before this node.
		p = New()
	}
	ctx = ContextWithPipeline(ctx, p)
	return p, ctx
}

// MergePipelineContexts returns the first context with a pipeline that is the combination of the pipelines
// from all the contexts.
func MergePipelineContexts(ctxs ...context.Context) context.Context {
	if len(ctxs) == 0 {
		return context.Background()
	}
	pipelines := make([]Pipeline, 0, len(ctxs))
	for _, ctx := range ctxs {
		if p, ok := PipelineFromContext(ctx); ok {
			pipelines = append(pipelines, p)
		}
	}
	if len(pipelines) == 0 {
		return ctxs[0]
	}
	return ContextWithPipeline(ctxs[0], Merge(pipelines))
}