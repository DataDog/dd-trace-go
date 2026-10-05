// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gosdk

import "context"

type config struct {
	intentCapturePredicate func(context.Context) bool
}

type Option func(*config)

// WithIntentCapture enables intent capture for all tool calls.
//
// For per-request control (e.g. driven by a feature flag), use
// WithIntentCapturePredicate instead.
func WithIntentCapture() Option {
	return WithIntentCapturePredicate(func(context.Context) bool { return true })
}

// WithIntentCapturePredicate enables intent capture and gates it per-request
// using the supplied predicate. The predicate is consulted on every tools/list
// and tools/call: when it returns false, the request behaves as if intent
// capture were disabled (no schema injection, no telemetry stripping, no
// intent annotation).
func WithIntentCapturePredicate(predicate func(context.Context) bool) Option {
	return func(cfg *config) {
		cfg.intentCapturePredicate = predicate
	}
}
