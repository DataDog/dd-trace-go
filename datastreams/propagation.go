// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
)

// MergeContexts returns the first context which includes the pathway resulting from merging the pathways
// contained in all contexts.
// This function should be used in fan-in situations. The current implementation keeps only 1 Pathway.
// A future implementation could merge multiple Pathways together and put the resulting Pathway in the context.
func MergeContexts(ctxs ...context.Context) context.Context {
	return datastreams.MergeContexts(ctxs...)
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

// ExtractFromBase64Carrier extracts the pathway context from a carrier to a context object
func ExtractFromBase64Carrier(ctx context.Context, carrier TextMapReader) (outCtx context.Context) {
	return datastreams.ExtractFromBase64Carrier(ctx, carrier)
}

// InjectToBase64Carrier injects a pathway context from a context object inta a carrier
func InjectToBase64Carrier(ctx context.Context, carrier TextMapWriter) {
	datastreams.InjectToBase64Carrier(ctx, carrier)
}
