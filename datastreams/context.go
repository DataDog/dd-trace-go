// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
)

type contextKey struct{}

var activePathwayKey = contextKey{}

// ContextWithPathway returns a copy of the given context which includes the pathway p.
func ContextWithPathway(ctx context.Context, p Pathway) context.Context {
	return context.WithValue(ctx, activePathwayKey, p)
}

// PathwayFromContext returns the pathway contained in the given context, and whether a
// pathway is found in ctx.
func PathwayFromContext(ctx context.Context) (p Pathway, ok bool) {
	if ctx == nil {
		return p, false
	}
	v := ctx.Value(activePathwayKey)
	if p, ok := v.(Pathway); ok {
		return p, true
	}
	return p, false
}

// MergeContexts returns the first context which includes the pathway resulting from merging the pathways
// contained in all contexts.
// This function should be used in fan-in situations. The current implementation keeps only 1 Pathway.
// A future implementation could merge multiple Pathways together and put the resulting Pathway in the context.
func MergeContexts(ctxs ...context.Context) context.Context {
	if len(ctxs) == 0 {
		return context.Background()
	}
	pathways := make([]Pathway, 0, len(ctxs))
	for _, ctx := range ctxs {
		if p, ok := PathwayFromContext(ctx); ok {
			pathways = append(pathways, p)
		}
	}
	if len(pathways) == 0 {
		return ctxs[0]
	}
	return ContextWithPathway(ctxs[0], Merge(pathways))
}
