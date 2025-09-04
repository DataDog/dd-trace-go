// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package baggage

import (
	"context"
	"maps"
)

// Special context keys for W3C baggage operations on SpanContext
type (
	W3CBaggageSetKey     struct{ Key, Value string }
	W3CBaggageGetKey     struct{ Key string }
	W3CBaggageRemoveKey  struct{ Key string }
	W3CBaggageAllKey     struct{}
	W3CBaggageClearKey   struct{}
	W3CBaggageIterateKey struct{ Handler func(string, string) bool }
)

// baggageKey is an unexported type used as a context key. It is used to store baggage in the context.
// We use a struct{} so it won't conflict with keys from other packages.
type baggageKey struct{}

// baggageMap returns the baggage map from the given context and a bool indicating
// whether the baggage exists or not. If the bool is false, the returned map is nil.
func baggageMap(ctx context.Context) (map[string]string, bool) {
	val := ctx.Value(baggageKey{})
	bm, ok := val.(map[string]string)
	if !ok {
		// val was nil or not a map[string]string
		return nil, false
	}
	return bm, true
}

// withBaggage returns a new context with the given baggage map set.
func withBaggage(ctx context.Context, baggage map[string]string) context.Context {
	return context.WithValue(ctx, baggageKey{}, baggage)
}

// Set sets or updates a single baggage key/value pair in the context.
// If the key already exists, this function overwrites the existing value.
// This function now works with both regular context.Context and SpanContext.
func Set(ctx context.Context, key, value string) context.Context {
	// Try to use SpanContext W3C baggage via context.Value
	if result := ctx.Value(W3CBaggageSetKey{Key: key, Value: value}); result != nil {
		if updatedCtx, ok := result.(context.Context); ok {
			return updatedCtx
		}
	}

	// Fallback to the original context-based baggage implementation
	bm, ok := baggageMap(ctx)
	if !ok || bm == nil {
		// If there's no baggage map yet, or it's nil, create one
		bm = make(map[string]string)
	} else {
		bm = maps.Clone(bm)
	}
	bm[key] = value
	return withBaggage(ctx, bm)
}

// Get retrieves the value associated with a baggage key.
// If the key isn't found, it returns an empty string.
// This function now works with both regular context.Context and SpanContext.
func Get(ctx context.Context, key string) (string, bool) {
	// Try to get from SpanContext W3C baggage via context.Value
	if result := ctx.Value(W3CBaggageGetKey{Key: key}); result != nil {
		if value, ok := result.(string); ok {
			return value, true
		}
	}

	// Fallback to the original context-based baggage implementation
	bm, ok := baggageMap(ctx)
	if !ok {
		return "", false
	}
	value, ok := bm[key]
	return value, ok
}

// Remove removes the specified key from the baggage (if present).
// This function now works with both regular context.Context and SpanContext.
func Remove(ctx context.Context, key string) context.Context {
	// Try to remove from SpanContext W3C baggage via context.Value
	if result := ctx.Value(W3CBaggageRemoveKey{Key: key}); result != nil {
		if updatedCtx, ok := result.(context.Context); ok {
			return updatedCtx
		}
	}

	// Fallback to the original context-based baggage implementation
	bm, ok := baggageMap(ctx)
	if !ok || bm == nil {
		// nothing to remove
		return ctx
	}
	bmCopy := maps.Clone(bm)
	delete(bmCopy, key)
	return withBaggage(ctx, bmCopy)
}

// All returns a **copy** of all baggage items in the context,
// This function now works with both regular context.Context and SpanContext.
func All(ctx context.Context) map[string]string {
	// Try to get all from SpanContext W3C baggage via context.Value
	if result := ctx.Value(W3CBaggageAllKey{}); result != nil {
		if baggage, ok := result.(map[string]string); ok {
			return baggage
		}
	}

	// Fallback to the original context-based baggage implementation
	bm, ok := baggageMap(ctx)
	if !ok {
		return nil
	}
	return maps.Clone(bm)
}

// Clear completely removes all baggage items from the context.
// This function now works with both regular context.Context and SpanContext.
func Clear(ctx context.Context) context.Context {
	// Try to clear SpanContext W3C baggage via context.Value
	if result := ctx.Value(W3CBaggageClearKey{}); result != nil {
		if updatedCtx, ok := result.(context.Context); ok {
			return updatedCtx
		}
	}

	// Fallback to the original context-based baggage implementation
	return withBaggage(ctx, nil)
}

// ForeachBaggageItem iterates over W3C baggage items from a context.
// Since this package is dedicated to W3C baggage, this only iterates over W3C baggage.
func ForeachBaggageItem(ctx context.Context, handler func(k, v string) bool) {
	// Try to iterate over SpanContext W3C baggage via context.Value
	if result := ctx.Value(W3CBaggageIterateKey{Handler: handler}); result != nil {
		return // SpanContext handled the iteration
	}

	// Fallback for regular context.Context - iterate over context-stored baggage
	if bm, ok := baggageMap(ctx); ok && bm != nil {
		for k, v := range bm {
			if !handler(k, v) {
				break
			}
		}
	}
}
