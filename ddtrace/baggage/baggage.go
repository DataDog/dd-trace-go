// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package baggage

import (
	"context"
	"maps"
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
func Set(ctx context.Context, key, value string) context.Context {
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
func Get(ctx context.Context, key string) (string, bool) {
	bm, ok := baggageMap(ctx)
	if !ok {
		return "", false
	}
	value, ok := bm[key]
	return value, ok
}

// Remove removes the specified key from the baggage (if present).
func Remove(ctx context.Context, key string) context.Context {
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
func All(ctx context.Context) map[string]string {
	bm, ok := baggageMap(ctx)
	if !ok {
		return nil
	}
	return maps.Clone(bm)
}

// Clear completely removes all baggage items from the context.
func Clear(ctx context.Context) context.Context {
	return withBaggage(ctx, nil)
}
