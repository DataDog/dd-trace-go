// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelcguard marks the dial this contrib performs on its own behalf.
//
// The otelc rules hook the redis.DialContext definition, which the contrib itself
// calls. Without a way to tell the two apart, an application that dials through
// the contrib in an auto-instrumented build gets its connection wrapped twice,
// once by its own call and once by the hook, and every command reports two spans.
// Orchestrion never had this problem: it rewrites call sites in application code
// and leaves dd-trace-go's own calls alone.
//
// The mark costs one context value per dial, on connection setup rather than on
// any command path.
package otelcguard

import "context"

type key struct{}

// Mark returns ctx tagged as a dial the contrib is making itself.
func Mark(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, key{}, struct{}{})
}

// Marked reports whether ctx was tagged by [Mark].
func Marked(ctx context.Context) bool {
	return ctx != nil && ctx.Value(key{}) != nil
}
