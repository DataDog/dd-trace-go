// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package baggage

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
)

// Set sets or updates a single baggage key/value pair in the context.
// If the key already exists, this function overwrites the existing value.
func Set(ctx context.Context, key, value string) context.Context {
	return v2.Set(ctx, key, value)
}

// Get retrieves the value associated with a baggage key.
// If the key isn't found, it returns an empty string.
func Get(ctx context.Context, key string) (string, bool) {
	return v2.Get(ctx, key)
}

// Remove removes the specified key from the baggage (if present).
func Remove(ctx context.Context, key string) context.Context {
	return v2.Remove(ctx, key)
}

// All returns a **copy** of all baggage items in the context,
func All(ctx context.Context) map[string]string {
	return v2.All(ctx)
}

// Clear completely removes all baggage items from the context.
func Clear(ctx context.Context) context.Context {
	return v2.Clear(ctx)
}
