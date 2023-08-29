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

type CheckpointParams struct {
	PayloadSize int64
}

func NewCheckpointParams() CheckpointParams {
	return CheckpointParams{}
}

func (p CheckpointParams) WithPayloadSize(size int64) CheckpointParams {
	p.PayloadSize = size
	return p
}

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

// TextMapWriter allows setting key/value pairs of strings on the underlying
// data structure. Carriers implementing TextMapWriter are compatible to be
// used with Datadog's TextMapPropagator.
type TextMapWriter interface {
	// Set sets the given key/value pair.
	Set(key, val string)
}

// TextMapReader allows iterating over sets of key/value pairs. Carriers implementing
// TextMapReader are compatible to be used with Datadog's TextMapPropagator.
type TextMapReader interface {
	// ForeachKey iterates over all keys that exist in the underlying
	// carrier. It takes a callback function which will be called
	// using all key/value pairs as arguments. ForeachKey will return
	// the first error returned by the handler.
	ForeachKey(handler func(key, val string) error) error
}

// ExtractFromCarrier extracts the pathway context from a carrier to a context object
func ExtractFromCarrier(ctx context.Context, carrier TextMapReader) (outCtx context.Context) {
	outCtx = ctx
	carrier.ForeachKey(func(key, val string) error {
		if key == PropagationKey {
			_, outCtx, _ = Decode(ctx, []byte(val))
		}
		return nil
	})
	return outCtx
}

// InjectToCarrier injects a pathway context from a context object inta a carrier
func InjectToCarrier(ctx context.Context, carrier TextMapWriter) {
	p, ok := PathwayFromContext(ctx)
	if !ok {
		return
	}
	carrier.Set(PropagationKey, string(p.Encode()))
}
