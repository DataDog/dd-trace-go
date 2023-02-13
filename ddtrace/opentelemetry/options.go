// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

type contextOptionsKey struct{}

var startOptsKey = contextOptionsKey{}

// ContextWithStartOptions returns a copy of the given context which includes the span s.
func ContextWithStartOptions(ctx context.Context, opts ...ddtrace.StartSpanOption) context.Context {
	if len(opts) == 0 {
		return ctx
	}
	return context.WithValue(ctx, startOptsKey, opts)
}

// spanOptionsFromContext returns the span start configuration options contained in the given context.
// If no configuration is found, nil is returned.
func spanOptionsFromContext(ctx context.Context) ([]ddtrace.StartSpanOption, bool) {
	if ctx == nil {
		return nil, false
	}
	v := ctx.Value(startOptsKey)
	if s, ok := v.([]ddtrace.StartSpanOption); ok {
		return s, true
	}
	return nil, false
}

// withContext associates the ctx with the span.
func withInnerOptions(opts ...ddtrace.StartSpanOption) ddtrace.StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		for _, fn := range opts {
			fn(cfg)
		}
	}
}
