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
	bm, _ := ctx.Value(baggageKey{}).(map[string]string)
	return bm
}

// WithBaggage returns a new context with the given baggage map set.
func WithBaggage(ctx context.Context, baggage map[string]string) context.Context {
	return context.WithValue(ctx, baggageKey{}, baggage)
}

// SetBaggage sets or updates a single baggage key/value pair in the context.
func SetBaggage(ctx context.Context, key, value string) context.Context {
	bm := baggageMap(ctx)
	if bm == nil {
		bm = make(map[string]string)
	}
	bm[key] = value
	return WithBaggage(ctx, bm)
}

// Baggage retrieves the value associated with a baggage key.
// If the key isn't found, it returns an empty string.
func Baggage(ctx context.Context, key string) string {
	bm := baggageMap(ctx)
	if bm == nil {
		return ""
	}
	return bm[key]
}

// RemoveBaggage removes the specified key from the baggage (if present).
func RemoveBaggage(ctx context.Context, key string) context.Context {
	bm := baggageMap(ctx)
	if bm == nil {
		// nothing to remove
		return ctx
	}
	delete(bm, key)
	return WithBaggage(ctx, bm)
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
	return WithBaggage(ctx, nil)
}
