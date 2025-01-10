// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"context"
)

// baggageKey is an unexported type used as a context key. It is used to store baggage in the context.
// We use a struct{} so it won't conflict with keys from other packages.
type baggageKey struct{}

// baggageMap retrieves the map that holds baggage from the context.
// Returns nil if no baggage map is stored in the context.
func baggageMap(ctx context.Context) map[string]string {
	val := ctx.Value(baggageKey{})
	bm, ok := val.(map[string]string)
	if !ok {
		// bm is not a map[string]string or val was nil
		return nil
	}
	return bm
}

// withBaggage returns a new context with the given baggage map set.
func withBaggage(ctx context.Context, baggage map[string]string) context.Context {
	return context.WithValue(ctx, baggageKey{}, baggage)
}

// SetBaggage sets or updates a single baggage key/value pair in the context.
// If the key already exists, this function overwrites the existing value.
func SetBaggage(ctx context.Context, key, value string) context.Context {
	bm := baggageMap(ctx)
	if bm == nil {
		bm = make(map[string]string)
	}
	bm[key] = value
	return withBaggage(ctx, bm)
}

// Baggage retrieves the value associated with a baggage key.
// If the key isn't found, it returns an empty string.
func Baggage(ctx context.Context, key string) (string, bool) {
	bm := baggageMap(ctx)
	if bm == nil {
		return "", false
	}
	value, ok := bm[key]
	return value, ok
}

// RemoveBaggage removes the specified key from the baggage (if present).
func RemoveBaggage(ctx context.Context, key string) context.Context {
	bm := baggageMap(ctx)
	if bm == nil {
		// nothing to remove
		return ctx
	}
	delete(bm, key)
	return withBaggage(ctx, bm)
}

// AllBaggage returns a **copy** of all baggage items in the context,
func AllBaggage(ctx context.Context) map[string]string {
	bm := baggageMap(ctx)
	if bm == nil {
		return nil
	}
	copyMap := make(map[string]string, len(bm))
	for k, v := range bm {
		copyMap[k] = v
	}
	return copyMap
}

// ClearBaggage completely removes all baggage items from the context.
func ClearBaggage(ctx context.Context) context.Context {
	return withBaggage(ctx, nil)
}
